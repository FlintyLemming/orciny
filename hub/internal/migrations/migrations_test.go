package migrations_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
)

// newApp 起一个跑完全部迁移的临时 PocketBase。
// tests.NewTestApp 会克隆给定目录并执行 RunAllMigrations，
// 我们的迁移在 init() 里已注册进 core.AppMigrations，因此自动被应用。
func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestCollectionsExist(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{"machines", "enroll_tokens", "events"} {
		c, err := app.FindCollectionByNameOrId(name)
		require.NoError(t, err, "collection %s 必须存在", name)
		require.Nil(t, c.ListRule, "%s 的 list rule 必须是 nil（仅 superuser）", name)
		require.Nil(t, c.ViewRule, "%s 的 view rule 必须是 nil", name)
		require.Nil(t, c.CreateRule, "%s 的 create rule 必须是 nil", name)
		require.Nil(t, c.UpdateRule, "%s 的 update rule 必须是 nil", name)
		require.Nil(t, c.DeleteRule, "%s 的 delete rule 必须是 nil", name)
	}
}

func TestMachinesFields(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	for _, f := range []string{
		"name", "fingerprint", "pub_key", "hostname", "os", "arch",
		"agent_version", "tool_versions", "status", "last_seen", "created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "machines.%s 缺失", f)
	}

	status, ok := c.Fields.GetByName("status").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"online", "offline", "paused"}, status.Values)
}

func TestMachinesFingerprintIsUnique(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("fingerprint", "same-fp")
		r.Set("pub_key", "k")
		r.Set("status", "offline")
		return r
	}
	require.NoError(t, app.Save(mk()))
	require.Error(t, app.Save(mk()), "同一 fingerprint 必须被唯一索引挡住")
}

func TestEnrollTokensAndEventsFields(t *testing.T) {
	app := newApp(t)

	tk, err := app.FindCollectionByNameOrId("enroll_tokens")
	require.NoError(t, err)
	for _, f := range []string{"token_hash", "expires_at", "used_at", "machine"} {
		require.NotNil(t, tk.Fields.GetByName(f), "enroll_tokens.%s 缺失", f)
	}

	ev, err := app.FindCollectionByNameOrId("events")
	require.NoError(t, err)
	for _, f := range []string{"kind", "machine", "detail", "created"} {
		require.NotNil(t, ev.Fields.GetByName(f), "events.%s 缺失", f)
	}

	rel, ok := ev.Fields.GetByName("machine").(*core.RelationField)
	require.True(t, ok)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	require.Equal(t, machines.Id, rel.CollectionId)
	require.False(t, rel.Required, "机器被删后事件仍要保留，因此 machine 可空")
}

func TestM1CollectionsExist(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{
		"blobs", "config_sets", "revisions", "assignments",
		"credentials", "variables", "drift_events", "ignore_rules",
	} {
		c, err := app.FindCollectionByNameOrId(name)
		require.NoError(t, err, "collection %s 必须存在", name)
		require.Nil(t, c.ListRule, "%s 的 list rule 必须是 nil（仅 superuser）", name)
		require.Nil(t, c.ViewRule, "%s 的 view rule 必须是 nil", name)
		require.Nil(t, c.CreateRule, "%s 的 create rule 必须是 nil", name)
		require.Nil(t, c.UpdateRule, "%s 的 update rule 必须是 nil", name)
		require.Nil(t, c.DeleteRule, "%s 的 delete rule 必须是 nil", name)
	}
}

func TestConfigSetHeadIsRelationToRevisions(t *testing.T) {
	app := newApp(t)
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)

	head, ok := sets.Fields.GetByName("head").(*core.RelationField)
	require.True(t, ok, "config_sets.head 必须是 relation")
	require.Equal(t, revs.Id, head.CollectionId)
}

func TestRevisionsSeqIsUniquePerConfigSet(t *testing.T) {
	app := newApp(t)
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "主力配置")
	require.NoError(t, app.Save(set))

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	mk := func(setID string, seq int) *core.Record {
		r := core.NewRecord(revs)
		r.Set("config_set", setID)
		r.Set("seq", seq)
		r.Set("files", []any{})
		r.Set("manifest", map[string]any{"version": 1})
		r.Set("checksum", "deadbeef")
		r.Set("source", "publish")
		return r
	}
	require.NoError(t, app.Save(mk(set.Id, 1)))
	require.Error(t, app.Save(mk(set.Id, 1)), "同一配置集内 seq 必须唯一")

	other := core.NewRecord(sets)
	other.Set("name", "另一份")
	require.NoError(t, app.Save(other))
	require.NoError(t, app.Save(mk(other.Id, 1)), "不同配置集的 seq 互不干扰")
}

func TestAssignmentsOneConfigSetPerMachine(t *testing.T) {
	app := newApp(t)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(machines)
	m.Set("fingerprint", "fp-1")
	m.Set("pub_key", "pk-1")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	mkSet := func(name string) string {
		r := core.NewRecord(sets)
		r.Set("name", name)
		require.NoError(t, app.Save(r))
		return r.Id
	}

	as, err := app.FindCollectionByNameOrId("assignments")
	require.NoError(t, err)
	mk := func(setID string) *core.Record {
		r := core.NewRecord(as)
		r.Set("machine", m.Id)
		r.Set("config_set", setID)
		r.Set("mode", "apply")
		r.Set("state", "pending")
		return r
	}
	require.NoError(t, app.Save(mk(mkSet("a"))))
	require.Error(t, app.Save(mk(mkSet("b"))), "一机一配置集由唯一索引强制")
}

// 同一路径同一时刻只能有一条待处理漂移；已解决的不占位（部分唯一索引）。
func TestDriftEventsOpenPathIsUnique(t *testing.T) {
	app := newApp(t)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(machines)
	m.Set("fingerprint", "fp-2")
	m.Set("pub_key", "pk-2")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	de, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	mk := func(state string) *core.Record {
		r := core.NewRecord(de)
		r.Set("machine", m.Id)
		r.Set("path", ".claude/CLAUDE.md")
		r.Set("kind", "modified")
		r.Set("state", state)
		return r
	}
	first := mk("open")
	require.NoError(t, app.Save(first))
	require.Error(t, app.Save(mk("open")), "同一路径只能有一条 open 漂移")

	first.Set("state", "adopted")
	require.NoError(t, app.Save(first))
	require.NoError(t, app.Save(mk("open")), "旧的已收编，新的 open 应当可以建")
}

func TestProvidersCollectionExists(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	require.Nil(t, c.ListRule, "providers 的 list rule 必须是 nil（仅 superuser）")
	require.Nil(t, c.ViewRule)
	require.Nil(t, c.CreateRule)
	require.Nil(t, c.UpdateRule)
	require.Nil(t, c.DeleteRule)

	for _, f := range []string{
		"name", "preset", "base_url", "auth_field", "credential",
		"models", "defaults", "note", "created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "字段 %s 必须存在", f)
	}

	sel, ok := c.Fields.GetByName("auth_field").(*core.SelectField)
	require.True(t, ok)
	require.Equal(t, []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}, sel.Values)

	rel, ok := c.Fields.GetByName("credential").(*core.RelationField)
	require.True(t, ok)
	// 必填由 003 立、004 撤（key 内联之后没有凭据可指了），
	// 「必不必填」的断言归 TestProviderEndpointFieldsExist 管。
	require.False(t, rel.CascadeDelete, "删凭据不得连带删掉服务配置")
}

func TestProviderNameIsUnique(t *testing.T) {
	app := newApp(t)
	credID := seedCredential(t, app, "zhipu_key")

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("name", "智谱 GLM · 个人")
		r.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
		r.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
		r.Set("credential", credID)
		return r
	}
	require.NoError(t, app.Save(mk()))
	require.Error(t, app.Save(mk()), "同名服务配置必须被唯一索引拒绝")
}

func TestBindingFieldsExist(t *testing.T) {
	app := newApp(t)

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	require.NotNil(t, revs.Fields.GetByName("binding"))

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	require.NotNil(t, sets.Fields.GetByName("draft_binding"))
	hp, ok := sets.Fields.GetByName("head_provider").(*core.RelationField)
	require.True(t, ok, "head_provider 必须是 relation，前端要 expand 它")
	require.False(t, hp.CascadeDelete)

	drifts, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	require.NotNil(t, drifts.Fields.GetByName("binding_drift"))
	require.NotNil(t, drifts.Fields.GetByName("binding_url"))
}

// seedCredential 建一条最小可用的凭据记录，返回 id。
func seedCredential(t *testing.T, app core.App, name string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", "x")
	r.Set("last4", "1234")
	require.NoError(t, app.Save(r))
	return r.Id
}

func TestProviderEndpointFieldsExist(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)

	for _, f := range []string{
		"key_cipher", "key_last4",
		"claude_key_cipher", "openai_key_cipher",
		"claude", "openai",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "providers.%s 缺失", f)
	}

	// 密文一律 Hidden：PocketBase 的列表查询与 realtime 都不会带上它
	// （M1.6 spec §5.2「密文永不回传前端」）。
	for _, f := range []string{"key_cipher", "claude_key_cipher", "openai_key_cipher"} {
		tf, ok := c.Fields.GetByName(f).(*core.TextField)
		require.True(t, ok, "%s 必须是 TextField", f)
		require.True(t, tf.Hidden, "%s 必须 Hidden", f)
	}

	// last4 是给 UI 回显的，正是要回传的东西。
	l4, ok := c.Fields.GetByName("key_last4").(*core.TextField)
	require.True(t, ok)
	require.False(t, l4.Hidden)

	// credential 在 004 之后必须是非必填，否则新建 provider 会被挡住。
	rel, ok := c.Fields.GetByName("credential").(*core.RelationField)
	require.True(t, ok)
	require.False(t, rel.Required, "004 之后 credential 不再必填")

	// base_url 同理：它搬进 claude 子结构之后没有写入方了，
	// 留着必填会让 004 与 005 之间新建的 provider 存不进去。
	bu, ok := c.Fields.GetByName("base_url").(*core.TextField)
	require.True(t, ok)
	require.False(t, bu.Required, "004 之后顶层 base_url 不再必填")
}

// 密文**直接搬、不解密**：同一把主密钥、同一套 AES-GCM，secretbox 只是换了
// 包名（M1.6 spec §6.1 第 2 步）。这条测试是这个前提的证明。
func TestMigration004MovesCipherIntoProvider(t *testing.T) {
	app := newApp(t)

	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	cipher, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)

	credID := seedCredentialWith(t, app, "zhipu_key", cipher, "3456")
	provID := seedLegacyProvider(t, app, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", credID)

	require.NoError(t, runUp004(t, app))

	p, err := app.FindRecordById("providers", provID)
	require.NoError(t, err)

	require.Equal(t, cipher, p.GetString("key_cipher"), "密文必须原样搬过来")
	require.Equal(t, "3456", p.GetString("key_last4"))

	pt, err := secretbox.Decrypt(key, p.GetString("key_cipher"))
	require.NoError(t, err, "搬过来的密文必须还解得开")
	require.Equal(t, "sk-zhipu-abcdef123456", pt)

	var claude struct {
		BaseURL   string   `json:"base_url"`
		AuthField string   `json:"auth_field"`
		Models    []string `json:"models"`
		Defaults  struct {
			Main, Opus, Sonnet, Haiku string
		} `json:"defaults"`
	}
	require.NoError(t, p.UnmarshalJSONField("claude", &claude))
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", claude.BaseURL)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", claude.AuthField)
	require.Equal(t, []string{"glm-5.2", "glm-4.7"}, claude.Models)
	require.Equal(t, "glm-5.2", claude.Defaults.Main)

	// openai 端点没有来源，必须是「未配置」。
	var openai struct {
		BaseURL string `json:"base_url"`
	}
	require.NoError(t, p.UnmarshalJSONField("openai", &openai))
	require.Empty(t, openai.BaseURL)
}

func TestMigration004IsIdempotentOnEmptyProviderTable(t *testing.T) {
	app := newApp(t) // 一条 provider 都没有
	require.NoError(t, runUp004(t, app), "空表上重跑迁移不得报错")
}

// ---------- helpers ----------

// runUp004 重跑 004 的数据搬运部分。字段已在 newApp 时加好，
// 搬运对已搬过的记录是幂等的（见 up004 的实现）。
func runUp004(t *testing.T, app core.App) error {
	t.Helper()
	return migrations.BackfillProviderEndpoints(app)
}

// seedCredentialWith 建一条带指定密文的凭据，返回 id。
func seedCredentialWith(t *testing.T, app core.App, name, cipher, last4 string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", cipher)
	r.Set("last4", last4)
	require.NoError(t, app.Save(r))
	return r.Id
}

// seedLegacyProvider 用 003 的老字段建一条 provider，返回 id。
func seedLegacyProvider(t *testing.T, app core.App, name, baseURL, credID string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("base_url", baseURL)
	r.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	r.Set("credential", credID)
	r.Set("models", []string{"glm-5.2", "glm-4.7"})
	r.Set("defaults", map[string]string{
		"main": "glm-5.2", "opus": "glm-5.2", "sonnet": "glm-5.2", "haiku": "glm-4.7",
	})
	require.NoError(t, app.Save(r))
	return r.Id
}
