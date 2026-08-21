package revisions_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

func newBoth(t *testing.T) (*tests.TestApp, *configsets.Service, *revisions.Service) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	b := blobs.New(app)
	ev := events.NewWriter(app)
	return app, configsets.NewService(app, b, ev), revisions.NewService(app, b, ev)
}

func TestPublishFreezesDraft(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v1 {{cred.k}}"), 0o600, nil)
	require.NoError(t, err)

	rev, err := rs.Publish(set.Id, "第一版", "publish")
	require.NoError(t, err)
	require.EqualValues(t, 1, rev.GetInt("seq"))
	require.Equal(t, "publish", rev.GetString("source"))
	require.NotEmpty(t, rev.GetString("manifest"), "manifest 必须随版本冻结")

	files, err := rs.Files(rev.Id)
	require.NoError(t, err)
	require.Equal(t, protocol.Checksum(files), rev.GetString("checksum"))

	var refs configsets.Refs
	require.NoError(t, rev.UnmarshalJSONField("refs", &refs))
	require.Equal(t, []string{"k"}, refs.Creds)

	head, err := rs.Head(set.Id)
	require.NoError(t, err)
	require.Equal(t, rev.Id, head.Id)

	updated, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Equal(t, rev.Id, updated.GetString("head"))
}

func TestPublishIncrementsSeq(t *testing.T) {
	_, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		_, err := cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte(fmt.Sprintf("v%d", i)), 0o644, nil)
		require.NoError(t, err)
		rev, err := rs.Publish(set.Id, "", "publish")
		require.NoError(t, err)
		require.EqualValues(t, i, rev.GetInt("seq"))
	}
}

// 并发发布不能产生重复 seq：唯一索引会拦下，Publish 必须重试。
func TestConcurrentPublishSeqIsUnique(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = rs.Publish(set.Id, "", "publish")
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	recs, err := app.FindRecordsByFilter("revisions", "config_set = {:s}", "seq", 0, 0,
		map[string]any{"s": set.Id})
	require.NoError(t, err)
	require.Len(t, recs, n)
	for i, r := range recs {
		require.EqualValues(t, i+1, r.GetInt("seq"))
	}
}

// 回滚生成新版本，绝不改历史（spec §7.2）。
func TestRollbackCreatesNewRevision(t *testing.T) {
	_, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)

	_, err = cs.SetDraftFile(set.Id, "a", []byte("v1"), 0o644, nil)
	require.NoError(t, err)
	v1, err := rs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	_, err = cs.SetDraftFile(set.Id, "a", []byte("v2"), 0o644, nil)
	require.NoError(t, err)
	v2, err := rs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	v3, err := rs.Rollback(set.Id, v1.Id)
	require.NoError(t, err)
	require.EqualValues(t, 3, v3.GetInt("seq"))
	require.Equal(t, "rollback", v3.GetString("source"))
	require.Contains(t, v3.GetString("note"), "v1")
	require.Equal(t, v1.GetString("checksum"), v3.GetString("checksum"),
		"回滚后的内容必须与被回滚到的版本一致")

	// 历史仍在
	for _, id := range []string{v1.Id, v2.Id} {
		_, err := rs.Files(id)
		require.NoError(t, err)
	}

	// 草稿也要跟着回到 v1，否则用户下一次发布会把 v2 的内容又推上去
	draft, err := cs.Draft(set.Id)
	require.NoError(t, err)
	v1Files, err := rs.Files(v1.Id)
	require.NoError(t, err)
	require.Equal(t, v1Files, draft)
}

func TestDiff(t *testing.T) {
	from := []protocol.FileEntry{
		{Path: "keep", Hash: "1", Mode: 0o644},
		{Path: "changed", Hash: "2", Mode: 0o644},
		{Path: "removed", Hash: "3", Mode: 0o644},
	}
	to := []protocol.FileEntry{
		{Path: "keep", Hash: "1", Mode: 0o644},
		{Path: "changed", Hash: "9", Mode: 0o644},
		{Path: "added", Hash: "4", Mode: 0o644},
	}
	got := revisions.Diff(from, to)
	require.Equal(t, []revisions.FileChange{
		{Path: "added", Kind: "added", FromHash: "", ToHash: "4"},
		{Path: "changed", Kind: "modified", FromHash: "2", ToHash: "9"},
		{Path: "removed", Kind: "removed", FromHash: "3", ToHash: ""},
	}, got)
}

// mode 变了也算改动：它进 checksum，也决定落盘权限。
func TestDiffDetectsModeChange(t *testing.T) {
	from := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o644}}
	to := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o600}}
	got := revisions.Diff(from, to)
	require.Len(t, got, 1)
	require.Equal(t, "modified", got[0].Kind)
}

func TestPublishFreezesProviderKeys(t *testing.T) {
	_, cs, rs := newBoth(t)
	set, err := cs.Create("主力配置", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/settings.json", []byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",`+
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",`+
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`), 0o600, nil)
	require.NoError(t, err)

	rev, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)

	var refs configsets.Refs
	require.NoError(t, rev.UnmarshalJSONField("refs", &refs))
	require.Equal(t, []string{"auth_token", "base_url", "model"}, refs.ProviderKeys)
	require.Empty(t, refs.Creds)
}

// seedProvider 建一条最小可用 Provider（含一条凭据），返回记录 id。
func seedProvider(t *testing.T, app core.App, name, credName string) string {
	t.Helper()
	creds, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	cred := core.NewRecord(creds)
	cred.Set("name", credName)
	cred.Set("cipher_value", "x")
	cred.Set("last4", "1234")
	require.NoError(t, app.Save(cred))

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", name)
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", providers.AuthToken)
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))
	return p.Id
}

func fullSlots(id string) providers.ModelSlots {
	return providers.ModelSlots{Main: id, Opus: id, Sonnet: id, Haiku: id}
}

func TestPublishFreezesBindingAndSyncsHeadProvider(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("主力配置", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("内容"), 0o644, nil)
	require.NoError(t, err)

	provID := seedProvider(t, app, "智谱 GLM · 个人", "zhipu_key")
	require.NoError(t, cs.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provID, Models: fullSlots("glm-5.1"),
	}))

	rev, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)

	got, err := rs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, provID, got.Provider)
	require.Equal(t, "glm-5.1", got.Models.Haiku)

	reloaded, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Equal(t, provID, reloaded.GetString("head_provider"),
		"head_provider 必须与 head 同写——重注入的反查链靠它")
}

func TestPublishWithoutBindingClearsHeadProvider(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("主力配置", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("内容"), 0o644, nil)
	require.NoError(t, err)

	provID := seedProvider(t, app, "智谱 GLM · 个人", "zhipu_key")
	require.NoError(t, cs.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provID, Models: fullSlots("glm-5.1"),
	}))
	_, err = rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)

	require.NoError(t, cs.SetDraftBinding(set.Id, nil))
	_, err = rs.Publish(set.Id, "v2", "publish")
	require.NoError(t, err)

	reloaded, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Empty(t, reloaded.GetString("head_provider"))
}

// 回滚回到旧版本时，绑定也要跟着回去——否则 v3 会用 v2 的绑定
// 渲染 v1 的文件，谁也解释不了这个版本。
func TestRollbackRestoresBinding(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("主力配置", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v1"), 0o644, nil)
	require.NoError(t, err)

	provA := seedProvider(t, app, "智谱 GLM · 个人", "zhipu_key")
	provB := seedProvider(t, app, "Kimi · 个人", "kimi_key")

	require.NoError(t, cs.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provA, Models: fullSlots("glm-5.1"),
	}))
	rev1, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)

	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v2"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, cs.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provB, Models: fullSlots("kimi-k2"),
	}))
	_, err = rs.Publish(set.Id, "v2", "publish")
	require.NoError(t, err)

	rev3, err := rs.Rollback(set.Id, rev1.Id)
	require.NoError(t, err)

	got, err := rs.BindingOf(rev3.Id)
	require.NoError(t, err)
	require.Equal(t, provA, got.Provider)

	reloaded, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Equal(t, provA, reloaded.GetString("head_provider"))

	// 草稿也要拉回去，否则用户下一次发布会把绑定又切回 B。
	draft, err := cs.DraftBinding(set.Id)
	require.NoError(t, err)
	require.Equal(t, provA, draft.Provider)
}
