package drift_test

import (
	"fmt"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

const bindingBase = `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`

func TestDetectBindingDriftFindsLiteralURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.moonshot.cn/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	url, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://api.moonshot.cn/anthropic", url)
}

// 基线里没有 provider.base_url → 这条漂移与绑定无关。
func TestDetectBindingDriftIgnoresUnboundBaseline(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`
	_, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.False(t, ok)
}

// 那一行没被改（还是占位符），改的是别处 → 不是绑定漂移。
func TestDetectBindingDriftIgnoresOtherChanges(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}",
    "MY_VAR": "1"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 改成了一段不像 URL 的东西 → 不乱报，走普通漂移。
func TestDetectBindingDriftIgnoresNonURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "待填",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 紧凑写法（无缩进、单行）也要认得。
func TestDetectBindingDriftHandlesCompactJSON(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://zenmux.ai/api/anthropic"}}`
	url, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://zenmux.ai/api/anthropic", url)
}

func TestDetectBindingDriftHandlesDeletedContent(t *testing.T) {
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), nil)
	require.False(t, ok)
}

// ---------- 落库标记 ----------

// assignWith 建一个含给定文件的配置集、发布并指派给 rig 的机器。
// 与 rig.assign 同形，只是文件由调用方给。
func (r *rig) assignWith(t *testing.T, files map[string]string) string {
	t.Helper()
	set, err := r.sets.Create("绑定配置", "")
	require.NoError(t, err)
	for path, content := range files {
		_, err = r.sets.SetDraftFile(set.Id, path, []byte(content), 0o600, nil)
		require.NoError(t, err)
	}
	rev, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, configsets.ModeApply)
	require.NoError(t, err)
	r.setID = set.Id
	r.revID = rev.Id
	return set.Id
}

func (r *rig) driftAt(t *testing.T, path string) *core.Record {
	t.Helper()
	rec, ok := r.driftsByPath(t)[path]
	require.True(t, ok, "没有 %s 的漂移", path)
	return rec
}

func TestHandleReportMarksBindingDrift(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Content: []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`),
		Mode:    0o600,
	})

	rec := r.driftAt(t, ".claude/settings.json")
	require.True(t, rec.GetBool("binding_drift"))
	require.Equal(t, "https://api.moonshot.cn/anthropic", rec.GetString("binding_url"))
}

// 普通漂移不该被误标——误标会把「收编」置灰，用户会以为工具坏了。
func TestHandleReportLeavesNormalDriftUnmarked(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{"CLAUDE.md": "原文"})

	r.report(t, protocol.DriftItem{
		Path: "CLAUDE.md", Kind: protocol.DriftModified,
		Content: []byte("改过的原文"), Mode: 0o644,
	})

	rec := r.driftAt(t, "CLAUDE.md")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 用户又把那一行改回来了 → 重复上报时标记要被清掉，不能粘住。
func TestHandleReportClearsBindingDriftWhenReverted(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"1"}}`,
	})

	report := func(content string) {
		r.report(t, protocol.DriftItem{
			Path: ".claude/settings.json", Kind: protocol.DriftModified,
			Content: []byte(content), Mode: 0o600,
		})
	}
	report(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic","X":"1"}}`)
	require.True(t, r.driftAt(t, ".claude/settings.json").GetBool("binding_drift"))

	report(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"2"}}`)
	rec := r.driftAt(t, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 内容没上来（Truncated）时无从判断，一律不标。
func TestHandleReportDoesNotMarkTruncated(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Truncated: true, Mode: 0o600, // Content 故意为空
	})

	rec := r.driftAt(t, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// ---------- 收编禁用 ----------

// reportBindingDrift 造一条 binding_drift = true 的 open 漂移，返回 event id。
func (r *rig) reportBindingDrift(t *testing.T, path, url string) string {
	t.Helper()
	r.report(t, protocol.DriftItem{
		Path: path, Kind: protocol.DriftModified,
		Content: []byte(`{"env":{"ANTHROPIC_BASE_URL":"` + url + `"}}`),
		Mode:    0o600,
	})
	rec := r.driftAt(t, path)
	require.True(t, rec.GetBool("binding_drift"), "%s 应当被标记为绑定漂移", path)
	return rec.Id
}

func TestAdoptRefusesBindingDrift(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	id := r.reportBindingDrift(t, ".claude/settings.json", "https://api.moonshot.cn/anthropic")

	_, err := r.svc.Adopt([]string{id})
	require.ErrorIs(t, err, drift.ErrBindingDrift)
	require.Contains(t, err.Error(), ".claude/settings.json")

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"), "拒绝之后什么都不该被改")
}

// 恢复与忽略不受影响——它们不会破坏绑定。
func TestRestoreAndIgnoreAllowBindingDrift(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
		".claude/other.json":    `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	a := r.reportBindingDrift(t, ".claude/settings.json", "https://a.test/v1")
	b := r.reportBindingDrift(t, ".claude/other.json", "https://b.test/v1")

	require.NoError(t, r.svc.Restore([]string{a}))
	require.NoError(t, r.svc.Ignore([]string{b}, false))
}

// 一批里混进一条绑定漂移 → 整批拒绝。
// 冲突检查已经是「要么整批成，要么什么都不动」，这条沿用同一立场。
func TestAdoptRefusesMixedBatchWithBindingDrift(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
		"CLAUDE.md":             "原文",
	})
	r.report(t, protocol.DriftItem{
		Path: "CLAUDE.md", Kind: protocol.DriftModified,
		Content: []byte("改过的原文"), Mode: 0o644,
	})
	normal := r.driftAt(t, "CLAUDE.md").Id
	bound := r.reportBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic")

	_, err := r.svc.Adopt([]string{normal, bound})
	require.ErrorIs(t, err, drift.ErrBindingDrift)

	rec, err := r.app.FindRecordById("drift_events", normal)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"), "整批拒绝，普通那条也不动")
}

// ---------- 反查（子计划 07） ----------

func (r *rig) seedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	return r.seedProviderWithDefaults(t, name, baseURL, key, providers.ModelSlots{})
}

func (r *rig) seedProviderWithDefaults(
	t *testing.T, name, baseURL, key string, defaults providers.ModelSlots,
) string {
	t.Helper()
	// 凭据名只允许 [A-Za-z0-9_-]+，不能直接用中文显示名。
	credName := fmt.Sprintf("cred_%d", len(name)*31+len(baseURL))
	_, err := r.creds.Create(credName, key, "")
	require.NoError(t, err)
	cred, err := r.app.FindFirstRecordByData("credentials", "name", credName)
	require.NoError(t, err)

	rec, err := r.provs.Create(providers.Input{
		Name: name, BaseURL: baseURL, AuthField: providers.AuthToken,
		Credential: cred.Id, Models: []string{defaults.Main}, Defaults: defaults,
	})
	require.NoError(t, err)
	return rec.Id
}

// seedBoundSet 建一个绑到给定 Provider 的配置集并发布，指派给 rig 的机器。
func (r *rig) seedBoundSet(t *testing.T, providerID string, files map[string]string) string {
	t.Helper()
	setID := r.assignWith(t, files)
	require.NoError(t, r.sets.SetDraftBinding(setID, &providers.Binding{Provider: providerID}))
	rev, err := r.revs.Publish(setID, "绑定", "publish")
	require.NoError(t, err)
	r.revID = rev.Id
	return setID
}

// seedBindingDrift 造一条绑定漂移。key 为空时那一行留占位符。
func (r *rig) seedBindingDrift(t *testing.T, path, url, key string) string {
	t.Helper()
	r.assignWith(t, map[string]string{
		path: `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`,
	})
	return r.reportBindingDriftWithKey(t, path, url, key)
}

// seedBindingDriftIn 在一个既有配置集上造绑定漂移。
func (r *rig) seedBindingDriftIn(t *testing.T, setID, path, url, key string) string {
	t.Helper()
	require.Equal(t, r.setID, setID, "rig 当前指派的就是这个配置集")
	return r.reportBindingDriftWithKey(t, path, url, key)
}

func (r *rig) reportBindingDriftWithKey(t *testing.T, path, url, key string) string {
	t.Helper()
	token := "{{provider.auth_token}}"
	if key != "" {
		token = key
	}
	r.report(t, protocol.DriftItem{
		Path: path, Kind: protocol.DriftModified,
		Content: []byte(`{"env":{"ANTHROPIC_BASE_URL":"` + url + `",` +
			`"ANTHROPIC_AUTH_TOKEN":"` + token + `"}}`),
		Mode: 0o600,
	})
	rec := r.driftAt(t, path)
	require.True(t, rec.GetBool("binding_drift"), "%s 应当被标记为绑定漂移", path)
	return rec.Id
}

func (r *rig) seedNormalDrift(t *testing.T, path string) string {
	t.Helper()
	r.assignWith(t, map[string]string{path: "原文"})
	r.report(t, protocol.DriftItem{
		Path: path, Kind: protocol.DriftModified,
		Content: []byte("改过的原文"), Mode: 0o644,
	})
	return r.driftAt(t, path).Id
}

// 第一档：字面 URL 精确命中已有 Provider。
func TestMatchBindingHitsExistingProvider(t *testing.T) {
	r := newRig(t)
	kimi := r.seedProvider(t, "Kimi 官方", "https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij")
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, "https://api.moonshot.cn/anthropic", m.URL)
	require.Equal(t, providers.MatchProvider, m.Match.Kind)
	require.True(t, m.Match.Exact)
	require.Equal(t, kimi, m.Match.ProviderID)
}

// 第二档：库里没有，内置预设里有。
func TestMatchBindingHitsPreset(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Match.Kind)
	require.Equal(t, "kimi", m.Match.PresetID)
}

// 第三档：都不命中。
func TestMatchBindingNoHit(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://某个没人听说过的中转.test/v1", "")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Match.Kind)
}

// 第二档还要顺手找出机器上手写的那把 key，供新建向导抽成凭据。
func TestMatchBindingFindsHandWrittenKey(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "sk-kimi-QWERTYUIOPasdfghjkl1234")

	m, err := r.svc.MatchBinding(eventID)
	require.NoError(t, err)
	require.Equal(t, "env.ANTHROPIC_AUTH_TOKEN", m.KeyLocation)
	require.Contains(t, m.KeyMasked, "…")
	require.NotContains(t, m.KeyMasked, "QWERTYUIOPasdfghjkl",
		"掩码里不许出现完整的 key")
}

func TestMatchBindingRejectsNonBindingDrift(t *testing.T) {
	r := newRig(t)
	eventID := r.seedNormalDrift(t, "CLAUDE.md")
	_, err := r.svc.MatchBinding(eventID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "不是绑定漂移")
}

func TestExtractKeyCreatesCredential(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "sk-kimi-QWERTYUIOPasdfghjkl1234")

	credID, err := r.svc.ExtractKey(eventID, "env.ANTHROPIC_AUTH_TOKEN", "kimi_key")
	require.NoError(t, err)

	v, err := r.creds.ValueByID(credID)
	require.NoError(t, err)
	require.Equal(t, "sk-kimi-QWERTYUIOPasdfghjkl1234", v)
}

// 太短的值抽成凭据会在还原时到处误匹配（M1 spec §6.4），一律拒绝。
func TestExtractKeyRejectsShortValue(t *testing.T) {
	r := newRig(t)
	eventID := r.seedBindingDrift(t, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "abc")
	_, err := r.svc.ExtractKey(eventID, "env.ANTHROPIC_AUTH_TOKEN", "kimi_key")
	require.Error(t, err)
}

// ---------- Rebind ----------

func TestRebindProducesNewRevisionAndKeepsBindingAlive(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "Zhipu", "https://open.bigmodel.cn/api/anthropic",
		"sk-zhipu-abcdefghij")
	kimi := r.seedProvider(t, "KimiOfficial", "https://api.moonshot.cn/anthropic",
		"sk-kimi-abcdefghij")

	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	before, err := r.revs.Head(setID)
	require.NoError(t, err)

	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	rev, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)
	require.NotEqual(t, before.Id, rev.Id, "换绑定必须产生新 Revision")

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, kimi, got.Provider)

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, rev.Id, set.GetString("head"))
	require.Equal(t, kimi, set.GetString("head_provider"))

	// 草稿绑定也要跟着走，否则下一次发布会把绑定切回智谱。
	draft, err := r.sets.DraftBinding(setID)
	require.NoError(t, err)
	require.Equal(t, kimi, draft.Provider)

	// 文件内容不变——改的只是绑定。
	files, err := r.revs.Files(rev.Id)
	require.NoError(t, err)
	oldFiles, err := r.revs.Files(before.Id)
	require.NoError(t, err)
	require.Equal(t, oldFiles, files)
}

// 新绑定的模型槽取新 Provider 的 defaults——换了家供应商，
// 旧供应商的模型 id 在新 endpoint 上没有意义。
func TestRebindTakesNewProviderDefaults(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "Zhipu", "https://open.bigmodel.cn/api/anthropic",
		"sk-zhipu-abcdefghij")
	kimi := r.seedProviderWithDefaults(t, "KimiOfficial",
		"https://api.moonshot.cn/anthropic", "sk-kimi-abcdefghij",
		providers.ModelSlots{
			Main: "kimi-k2", Opus: "kimi-k2", Sonnet: "kimi-k2", Haiku: "kimi-k2",
		})

	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	rev, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)

	got, err := r.revs.BindingOf(rev.Id)
	require.NoError(t, err)
	require.Equal(t, providers.ModelSlots{
		Main: "kimi-k2", Opus: "kimi-k2", Sonnet: "kimi-k2", Haiku: "kimi-k2",
	}, got.Models)
}

// 漂移不在这里被关掉——ApplyAck 的既有路径会把它标成 superseded。
// 抢先标记等于撒谎：那时机器上还没变。
func TestRebindLeavesDriftOpen(t *testing.T) {
	r := newRig(t)
	zhipu := r.seedProvider(t, "Zhipu", "https://open.bigmodel.cn/api/anthropic",
		"sk-zhipu-abcdefghij")
	kimi := r.seedProvider(t, "KimiOfficial", "https://api.moonshot.cn/anthropic",
		"sk-kimi-abcdefghij")
	setID := r.seedBoundSet(t, zhipu, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	eventID := r.seedBindingDriftIn(t, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic", "")

	_, err := r.svc.Rebind(eventID, kimi)
	require.NoError(t, err)

	rec, err := r.app.FindRecordById("drift_events", eventID)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"))
}

func TestRebindRefusesNonBindingDrift(t *testing.T) {
	r := newRig(t)
	eventID := r.seedNormalDrift(t, "CLAUDE.md")
	_, err := r.svc.Rebind(eventID, "p1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "不是绑定漂移")
}
