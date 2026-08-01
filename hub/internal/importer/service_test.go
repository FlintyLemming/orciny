package importer_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/protocol"
)

type fakeSender struct {
	sent []protocol.CollectRequest
}

func (f *fakeSender) SendTo(_ string, kind protocol.Kind, payload any) error {
	if kind == protocol.KindCollectRequest {
		f.sent = append(f.sent, payload.(protocol.CollectRequest))
	}
	return nil
}
func (f *fakeSender) Online(string) bool { return true }

type rig struct {
	app    *tests.TestApp
	sets   *configsets.Service
	creds  *credentials.Store
	blobs  *blobs.Store
	sender *fakeSender
	svc    *importer.Service
	m      string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	ev := events.NewWriter(app)
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	r := &rig{
		app: app, blobs: b,
		sets:   configsets.NewService(app, b, ev),
		creds:  credentials.NewStore(app, key, ev),
		sender: &fakeSender{},
	}
	r.svc = importer.NewService(app, b, r.sets, r.creds, ev, r.sender)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	rec := core.NewRecord(mc)
	rec.Set("fingerprint", "fp")
	rec.Set("pub_key", "pk")
	rec.Set("status", "online")
	rec.Set("name", "主力机")
	require.NoError(t, app.Save(rec))
	r.m = rec.Id
	return r
}

func TestStartSendsCollectRequestWithDefaultManifest(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.NotEmpty(t, setID)
	require.Len(t, r.sender.sent, 1)
	require.Equal(t, token, r.sender.sent[0].Token)
	require.Contains(t, string(r.sender.sent[0].Manifest), ".claude/settings.json")

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Empty(t, set.GetString("head"), "导入中的配置集处于草稿态")
}

func TestHandleResultFillsDraft(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)

	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token,
		Files: []protocol.CollectedFile{
			{Path: ".claude/CLAUDE.md", Content: []byte("# 规矩"), Mode: 0o644},
		},
	}))
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json", Content: []byte(`{"env":{"K":"sk-ant-abcdefghij"}}`), Mode: 0o600},
		},
		Skipped: []protocol.SkippedFile{{Path: ".claude/big.md", Reason: protocol.SkipTooLarge}},
		Final:   true,
	}))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	require.Len(t, draft, 2, "两批都要进同一份草稿")

	kinds := map[string]bool{}
	recs, err := r.app.FindAllRecords("events")
	require.NoError(t, err)
	for _, rec := range recs {
		kinds[rec.GetString("kind")] = true
	}
	require.True(t, kinds[events.KindImportCompleted], "Final 之后才写完成事件")
}

// token 对不上的结果直接丢弃：防串批（spec §5.2）。
func TestHandleResultRejectsWrongToken(t *testing.T) {
	r := newRig(t)
	_, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)

	require.Error(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: "别的批次",
		Files: []protocol.CollectedFile{{Path: "a", Content: []byte("x"), Mode: 0o644}},
	}))
	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	require.Empty(t, draft)
}

func TestFindingsScanDraft(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json",
				Content: []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-ant-abcdefghijkl"}}`), Mode: 0o600},
		},
	}))

	found, err := r.svc.Findings(setID)
	require.NoError(t, err)
	require.NotEmpty(t, found)
	require.Equal(t, ".claude/settings.json", found[0].Path)
	require.Equal(t, "anthropic_auth_token", found[0].Suggested)
}

// 抽取：建凭据 + 把值替成占位符 + 重写草稿。库里此后查不到明文。
func TestExtractReplacesValueWithPlaceholder(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	secret := "sk-ant-abcdefghijklmnop"
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json",
				Content: []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"` + secret + `"}}`), Mode: 0o600},
		},
	}))

	require.NoError(t, r.svc.Extract(setID, ".claude/settings.json",
		"env.ANTHROPIC_AUTH_TOKEN", "anthropic_auth_token"))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	content, err := r.blobs.Get(draft[0].Hash)
	require.NoError(t, err)
	require.Contains(t, string(content), "{{cred.anthropic_auth_token}}")
	require.NotContains(t, string(content), secret)

	v, err := r.creds.Value("anthropic_auth_token")
	require.NoError(t, err)
	require.Equal(t, secret, v)

	// 抽取之后重扫，这一条不该再出现
	found, err := r.svc.Findings(setID)
	require.NoError(t, err)
	for _, f := range found {
		require.NotEqual(t, "env.ANTHROPIC_AUTH_TOKEN", f.Location)
	}
}

// 同一个值出现在多处：一次抽取全部替掉，否则漏一处就等于没脱敏。
func TestExtractReplacesEveryOccurrence(t *testing.T) {
	r := newRig(t)
	token, setID, err := r.svc.Start(r.m)
	require.NoError(t, err)
	secret := "sk-ant-abcdefghijklmnop"
	body := `{"env":{"A":"` + secret + `","B":"` + secret + `"}}`
	require.NoError(t, r.svc.HandleResult(r.m, protocol.CollectResult{
		Token: token, Final: true,
		Files: []protocol.CollectedFile{
			{Path: ".claude/settings.json", Content: []byte(body), Mode: 0o600},
		},
	}))

	require.NoError(t, r.svc.Extract(setID, ".claude/settings.json", "env.A", "k"))

	draft, err := r.sets.Draft(setID)
	require.NoError(t, err)
	content, err := r.blobs.Get(draft[0].Hash)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(content), "{{cred.k}}"))
	require.NotContains(t, string(content), secret)
}
