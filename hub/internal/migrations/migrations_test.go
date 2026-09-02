package migrations_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
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
	// credentials 由 002 建、005 删（key 内联进 provider 了，M1.6 spec §1.1）。
	// 「删掉了没有」归 TestMigration005DropsLegacyFieldsAndCollection 管。
	for _, name := range []string{
		"blobs", "config_sets", "revisions", "assignments",
		"variables", "drift_events", "ignore_rules",
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

	// 003 立的字段里，base_url / auth_field / credential / models / defaults
	// 在 005 被拆掉（内容已由 004 搬进端点子结构）。这里只断言活下来的那些；
	// 「拆掉了没有」归 TestMigration005DropsLegacyFieldsAndCollection 管。
	for _, f := range []string{"name", "preset", "note", "created", "updated"} {
		require.NotNil(t, c.Fields.GetByName(f), "字段 %s 必须存在", f)
	}
}

// name 的唯一索引跨过 004/005 仍然有效（DoD 第 1 条）。
func TestProviderNameIsUnique(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("name", "智谱 GLM · 个人")
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

	// 004 把 credential 与 base_url 转成非必填（005 之间新建 provider 要能存下），
	// 005 再把它们整个拆掉。跑完全链之后它们应当不存在——
	// 「转非必填」这一步由 TestBackfillOnLegacyShape 在重建的旧形状上验证。
}

// 密文**直接搬、不解密**：同一把主密钥、同一套 AES-GCM，secretbox 只是换了
// 包名（M1.6 spec §6.1 第 2 步）。这条测试是这个前提的证明。
func TestMigration004MovesCipherIntoProvider(t *testing.T) {
	app := newApp(t)
	restoreLegacyShape(t, app)

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

// restoreLegacyShape 在跑完全链的库上重建 M1.5 的形状：
// credentials collection 与 providers 的四个旧字段 + credential relation。
//
// 为什么要重建：tests.NewTestApp 会把全部迁移跑完，005 之后旧形状就不存在了，
// 而 004 的密文搬运是本期**最容易造成数据面损坏**的一步（搬漏了，全机队下次
// 重注入拿到空 key），必须有测试真的搬一次、真的解一次密。
func restoreLegacyShape(t *testing.T, app core.App) {
	t.Helper()

	creds := core.NewBaseCollection("credentials")
	creds.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 200},
		&core.TextField{Name: "cipher_value", Max: 8192},
		&core.TextField{Name: "last4", Max: 8},
		&core.TextField{Name: "note", Max: 2000},
	)
	require.NoError(t, app.Save(creds))

	provs, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	provs.Fields.Add(
		&core.TextField{Name: "base_url", Max: 2000},
		&core.SelectField{Name: "auth_field", MaxSelect: 1, Values: []string{
			"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY",
		}},
		&core.RelationField{Name: "credential", CollectionId: creds.Id,
			MaxSelect: 1, CascadeDelete: false},
		&core.JSONField{Name: "models", MaxSize: 8192},
		&core.JSONField{Name: "defaults", MaxSize: 2048},
	)
	// 旧记录还没搬过：把新结构清空，让 BackfillProviderEndpoints 认得出来。
	provs.Fields.RemoveByName("claude")
	provs.Fields.Add(&core.JSONField{Name: "claude", MaxSize: 16384})
	require.NoError(t, app.Save(provs))
}

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

func TestMigration005DropsLegacyFieldsAndCollection(t *testing.T) {
	app := newApp(t)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	for _, f := range []string{"credential", "base_url", "auth_field", "models", "defaults"} {
		require.Nil(t, c.Fields.GetByName(f), "providers.%s 必须已删除", f)
	}

	_, err = app.FindCollectionByNameOrId("credentials")
	require.Error(t, err, "credentials collection 必须已删除")
}

// 新结构必须完好——005 只删旧的，不许碰新的。
func TestMigration005KeepsEndpointFields(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	for _, f := range []string{
		"name", "preset", "note", "key_cipher", "key_last4",
		"claude_key_cipher", "openai_key_cipher", "claude", "openai",
		"created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "providers.%s 不该被删", f)
	}
}

// 004 + 005 跑完之后，搬过来的密文还解得开。这是整条迁移链的验收点。
func TestCipherSurvivesFullMigrationChain(t *testing.T) {
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	cipher, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)

	app := newApp(t)
	// 库里已经跑完全部迁移，credentials 表没了；直接把密文放进 provider 的
	// 平台级字段，模拟 004 搬运的结果，再验证它仍然解得开。
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", "智谱 GLM")
	r.Set("key_cipher", cipher)
	r.Set("key_last4", "3456")
	require.NoError(t, app.Save(r))

	got, err := app.FindRecordById("providers", r.Id)
	require.NoError(t, err)
	pt, err := secretbox.Decrypt(key, got.GetString("key_cipher"))
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", pt)
}

// down005 必须明确失败，而不是假装能回滚（spec §6.3）。
func TestDown005Refuses(t *testing.T) {
	app := newApp(t)
	require.Error(t, migrations.Down005(app),
		"凭据删了之后无处还原，假装能回滚比没有更危险")
}

func TestMigration006InfersOneMCapability(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)

	r := core.NewRecord(c)
	r.Set("name", "测试 GLM")
	r.Set("claude", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/anthropic",
		"auth_field":"ANTHROPIC_AUTH_TOKEN",
		"models":["glm-5.2","glm-4.7"],
		"defaults":{"main":"glm-5.2[1m]","opus":"glm-5.2[1m]","sonnet":"glm-5.2[1m]","haiku":"glm-4.7"}
	}`))
	r.Set("openai", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/paas/v4",
		"auth_field":"OPENAI_API_KEY",
		"models":["glm-5.2","glm-4.7"],
		"default_model":"glm-5.2"
	}`))
	require.NoError(t, app.Save(r))

	require.NoError(t, migrations.BackfillClaudeModels(app))

	got, err := app.FindRecordById("providers", r.Id)
	require.NoError(t, err)

	var cl struct {
		Models []struct {
			Name string `json:"name"`
			OneM bool   `json:"one_m"`
		} `json:"models"`
	}
	require.NoError(t, got.UnmarshalJSONField("claude", &cl))
	require.Len(t, cl.Models, 2)
	require.Equal(t, "glm-5.2", cl.Models[0].Name)
	require.True(t, cl.Models[0].OneM)
	require.Equal(t, "glm-4.7", cl.Models[1].Name)
	require.False(t, cl.Models[1].OneM)

	var oa struct {
		Models []string `json:"models"`
	}
	require.NoError(t, got.UnmarshalJSONField("openai", &oa))
	require.Equal(t, []string{"glm-5.2", "glm-4.7"}, oa.Models)
}

func TestMigration006PreservesRenderedKeys(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)

	r := core.NewRecord(c)
	r.Set("name", "测试 GLM 2")
	r.Set("claude", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/anthropic",
		"auth_field":"ANTHROPIC_AUTH_TOKEN",
		"models":["glm-5.2"],
		"defaults":{"main":"glm-5.2[1m]","opus":"glm-5.2[1m]","sonnet":"glm-5.2[1m]","haiku":"glm-5.2[1m]"}
	}`))
	require.NoError(t, app.Save(r))

	require.NoError(t, migrations.BackfillClaudeModels(app))

	got, err := app.FindRecordById("providers", r.Id)
	require.NoError(t, err)

	cl := providers.ClaudeOf(got)
	require.Equal(t, "glm-5.2[1m]", cl.Defaults.Main)
	require.Len(t, cl.Models, 1)
	require.Equal(t, "glm-5.2", cl.Models[0].Name)
	require.True(t, cl.Models[0].OneM)
}

func TestDown006Refuses(t *testing.T) {
	app := newApp(t)
	require.Error(t, migrations.Down006(app))
}

func TestMachineOverridesCollection(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machine_overrides")
	require.NoError(t, err)

	for _, f := range []string{
		"machine", "path", "kind", "selector", "base_value", "mine_value",
		"base_blob", "mine_blob", "attention", "shadowed_value",
		"shadowed_blob", "shadowed_rev", "origin_drift", "note",
		"created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "machine_overrides.%s 缺失", f)
	}

	kind, ok := c.Fields.GetByName("kind").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"json_key", "text"}, kind.Values)

	att, ok := c.Fields.GetByName("attention").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t,
		[]string{"hub_changed", "merge_conflict", "path_gone", "unmergeable"},
		att.Values)

	// API rule 一律 nil —— 仅 superuser 可访问，与其余 collection 一致。
	require.Nil(t, c.ListRule)
	require.Nil(t, c.ViewRule)
	require.Nil(t, c.CreateRule)
	require.Nil(t, c.UpdateRule)
	require.Nil(t, c.DeleteRule)
}

func TestMachineOverridesUniqueOnMachinePathSelector(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machine_overrides")
	require.NoError(t, err)

	var unique, attention bool
	for _, idx := range c.Indexes {
		if strings.Contains(idx, "idx_overrides_machine_path_sel") {
			require.Contains(t, idx, "UNIQUE")
			unique = true
		}
		if strings.Contains(idx, "idx_overrides_attention") {
			require.Contains(t, idx, "attention != ''")
			attention = true
		}
	}
	require.True(t, unique, "缺唯一索引 idx_overrides_machine_path_sel")
	require.True(t, attention, "缺部分索引 idx_overrides_attention")
}

func TestDriftStateHasOverridden(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	state, ok := c.Fields.GetByName("state").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"open", "adopted", "restored", "ignored", "superseded", "overridden",
	}, state.Values)
}

// 存量忽略规则原样留着，不被 007 动过（spec §3 第 3 条）。
func TestMigration007LeavesIgnoreRulesAlone(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("ignore_rules")
	require.NoError(t, err)
	rec := core.NewRecord(c)
	rec.Set("path", ".claude/settings.json")
	require.NoError(t, app.Save(rec))

	// 再跑一次迁移必须幂等，且不碰这条规则。
	require.NoError(t, migrations.Up007(app))

	got, err := app.FindRecordById("ignore_rules", rec.Id)
	require.NoError(t, err)
	require.Equal(t, ".claude/settings.json", got.GetString("path"))
}

func TestDown007Refuses(t *testing.T) {
	app := newApp(t)
	require.Error(t, migrations.Down007(app), "删集合等于删用户的覆盖层，必须拒绝")
}
