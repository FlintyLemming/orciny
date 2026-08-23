package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func newService(t *testing.T) (*tests.TestApp, *configsets.Service) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app, configsets.NewService(app, blobs.New(app), events.NewWriter(app))
}

func TestCreateSeedsDefaultManifest(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("主力配置", "从 mac 采集")
	require.NoError(t, err)
	require.Equal(t, "主力配置", set.GetString("name"))
	require.Empty(t, set.GetString("head"), "新建的配置集处于草稿态，head 为空")

	m, err := s.Manifest(set.Id)
	require.NoError(t, err)
	require.Equal(t, manifest.Default(), m)
}

func TestSetDraftFileStoresBlobAndEntry(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	content := []byte("# CLAUDE.md\n")
	e, err := s.SetDraftFile(set.Id, ".claude/CLAUDE.md", content, 0o644, nil)
	require.NoError(t, err)
	require.Equal(t, blobs.Hash(content), e.Hash)
	require.Equal(t, uint32(len(content)), e.Size)
	require.Equal(t, uint32(0o644), e.Mode)

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Len(t, draft, 1)
	require.Equal(t, e, draft[0])

	got, err := blobs.New(app).Get(e.Hash)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestSetDraftFileReplacesSamePath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v1"), 0o644, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v2"), 0o644, nil)
	require.NoError(t, err)

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Len(t, draft, 1, "同一路径只该有一条")
	require.Equal(t, blobs.Hash([]byte("v2")), draft[0].Hash)
}

func TestDraftIsSortedByPath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	for _, p := range []string{".claude/z.md", ".claude.json", ".claude/a.md"} {
		_, err := s.SetDraftFile(set.Id, p, []byte("x"), 0o644, nil)
		require.NoError(t, err)
	}
	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Equal(t, []string{".claude.json", ".claude/a.md", ".claude/z.md"},
		[]string{draft[0].Path, draft[1].Path, draft[2].Path})
}

func TestDraftRefsAreRecomputedOnEveryChange(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, "a", []byte(`{{var.key_a}} {{var.ws}}`), 0o600, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, "b", []byte(`{{var.key_b}}`), 0o644, nil)
	require.NoError(t, err)

	var refs configsets.Refs
	rec, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Equal(t, []string{"key_a", "key_b", "ws"}, refs.Vars)

	// 删掉引用 key_b 的文件之后，refs 必须跟着缩
	require.NoError(t, s.RemoveDraftFile(set.Id, "b"))
	rec, err = app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Equal(t, []string{"key_a", "ws"}, refs.Vars)
}

func TestSetDraftFileRejectsOversizeAndBadPath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/big", make([]byte, protocol.MaxFileSize+1), 0o644, nil)
	require.ErrorContains(t, err, "512")

	_, err = s.SetDraftFile(set.Id, "../escape", []byte("x"), 0o644, nil)
	require.Error(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/.credentials.json", []byte("x"), 0o600, nil)
	require.ErrorContains(t, err, "恒排除")
}

func TestSetDraftFileEmptyContent(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	e, err := s.SetDraftFile(set.Id, ".claude/settings.json", []byte{}, 0o644, nil)
	require.NoError(t, err)
	require.Equal(t, uint32(0), e.Size)
	require.NotEmpty(t, e.Hash)
}

func TestDraftRefsCollectsProviderKeys(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/settings.json", []byte(
		`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",`+
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}",`+
			`"ANTHROPIC_MODEL":"{{provider.claude.model}}","X":"{{var.k}}"}}`), 0o600, nil)
	require.NoError(t, err)

	rec, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	var refs configsets.Refs
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))

	require.Equal(t, []string{"claude.auth_token", "claude.base_url", "claude.model"},
		refs.ProviderKeys, "去重后按名字升序")
	require.Equal(t, []string{"k"}, refs.Vars)
}

// 转义的 {{{{provider.x}}}} 是字面文本，不算引用（spec §11）。
func TestDraftRefsIgnoresEscapedProviderRefs(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("文档配置", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, "CLAUDE.md",
		[]byte("写法是 {{{{provider.claude.base_url}}"), 0o644, nil)
	require.NoError(t, err)

	rec, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	var refs configsets.Refs
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Empty(t, refs.ProviderKeys)
}

// provider.* 的「已定义」由绑定与端点校验判定，不走未定义引用那条路——
// 否则每个绑定了服务的配置集都会报一堆假的未定义引用。
func TestValidateDoesNotReportProviderAsUndefinedRef(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemUndefinedRef, p.Kind,
			"provider.* 不该被当成未定义引用：%+v", p)
	}
}

// seedVariable 建一台机器并给它定义一个机器变量。
// Validate 的「已定义」判定是全库汇总，与哪台机器无关（见 knownVars）。
func seedVariable(t *testing.T, app core.App, key, value string) {
	t.Helper()
	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp-"+key)
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	vc, err := app.FindCollectionByNameOrId("variables")
	require.NoError(t, err)
	v := core.NewRecord(vc)
	v.Set("machine", m.Id)
	v.Set("key", key)
	v.Set("value", value)
	require.NoError(t, app.Save(v))
}
