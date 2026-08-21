package configsync_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

// seedProvider 建一条凭据 + 一条服务配置，返回 provider 记录 id。
// 凭据名固定为 zhipu_key，轮换测试要按名字找它。
func (r *rig) seedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	_, err := r.creds.Create("zhipu_key", key, "")
	require.NoError(t, err)
	cred, err := r.app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	rec, err := r.provs.Create(providers.Input{
		Name: name, BaseURL: baseURL, AuthField: providers.AuthToken,
		Credential: cred.Id, Models: []string{"glm-5.1"},
	})
	require.NoError(t, err)
	return rec.Id
}

// seedSetWithBinding 建配置集、写草稿文件、设绑定（可为 nil）再发布。
func (r *rig) seedSetWithBinding(
	t *testing.T, files map[string]string, b *providers.Binding,
) string {
	t.Helper()
	set, err := r.sets.Create("主力配置", "")
	require.NoError(t, err)
	for path, content := range files {
		_, err = r.sets.SetDraftFile(set.Id, path, []byte(content), 0o644, nil)
		require.NoError(t, err)
	}
	if b != nil {
		require.NoError(t, r.sets.SetDraftBinding(set.Id, b))
	}
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	return set.Id
}

func (r *rig) setAgentVersion(t *testing.T, machineID, v string) {
	t.Helper()
	m, err := r.app.FindRecordById("machines", machineID)
	require.NoError(t, err)
	m.Set("agent_version", v)
	require.NoError(t, r.app.Save(m))
}

func (r *rig) revisionCount(t *testing.T, setID string) int {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("revisions", "config_set = {:s}", "", 0, 0,
		map[string]any{"s": setID})
	require.NoError(t, err)
	return len(recs)
}

// inputFor 读回一条 Provider 的当前值，只把 base_url 换成新的。
func (r *rig) inputFor(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	rec, err := r.provs.Get(providerID)
	require.NoError(t, err)
	var models []string
	_ = rec.UnmarshalJSONField("models", &models)
	var defaults providers.ModelSlots
	_ = rec.UnmarshalJSONField("defaults", &defaults)
	return providers.Input{
		Name: rec.GetString("name"), Preset: rec.GetString("preset"),
		BaseURL: baseURL, AuthField: rec.GetString("auth_field"),
		Credential: rec.GetString("credential"), Models: models,
		Defaults: defaults, Note: rec.GetString("note"),
	}
}

func fullSlots(id string) providers.ModelSlots {
	return providers.ModelSlots{Main: id, Opus: id, Sonnet: id, Haiku: id}
}

// ---------- Task 1：快照组装与裁剪 ----------

func TestSnapshotFillsProviderValues(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-1")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")

	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`,
	}, &providers.Binding{Provider: provID, Models: fullSlots("glm-5.1")})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"base_url":   "https://open.bigmodel.cn/api/anthropic",
		"auth_token": "sk-zhipu-abcdefghij",
		"model":      "glm-5.1",
	}, snap.Provider, "只发被引用到的三个键，四槽里没被引用的不发")
}

// 没引用的键一个都不下发——少一个键少一处泄露面（spec §5.1）。
func TestSnapshotOmitsUnreferencedProviderKeys(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-2")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "主模型是 {{provider.model}}",
	}, &providers.Binding{Provider: provID, Models: fullSlots("glm-5.1")})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"model": "glm-5.1"}, snap.Provider)
	require.NotContains(t, snap.Provider, "auth_token", "没引用就绝不下发 key")
}

// 纵深防御：这种状态本应被发布校验挡住（spec §5.1 / §7）。
func TestSnapshotRefusesProviderRefsWithoutBinding(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-3")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, nil)
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrBindingMissing)
}

// 透传模式下引用了模型槽 → 拒绝下发并归因，而不是发一个空串下去
// 让 Claude Code 去打一个空模型名。
func TestSnapshotRefusesEmptyModelSlot(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-4")
	provID := r.seedProvider(t, "Anthropic 官方",
		"https://api.anthropic.com", "sk-ant-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_MODEL":"{{provider.model}}"}}`,
	}, &providers.Binding{Provider: provID}) // 四槽全空 = 透传
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrEmptyModelSlot)
	require.Contains(t, err.Error(), "provider.model")
}

// 绑了但没用：警告级，照常下发，Provider 为空。
func TestSnapshotWithBindingButNoRefs(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-5")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "没有任何占位符",
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Empty(t, snap.Provider)
}

// ---------- Task 2：定向版本门槛 ----------

func TestSnapshotRefusesOldAgentWhenBound(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-old-1")
	r.setAgentVersion(t, m, "0.1.0")

	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrAgentTooOld)
	require.Contains(t, err.Error(), "0.1.0")
	require.Contains(t, err.Error(), "0.2.0")
}

// 没绑定的配置集不受门槛影响——惩罚面不该扩大到无关机器。
func TestSnapshotAllowsOldAgentWhenUnbound(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-old-2")
	r.setAgentVersion(t, m, "0.1.0")
	setID := r.seedSetWithBinding(t, map[string]string{"CLAUDE.md": "无占位符"}, nil)
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.NoError(t, err)
}

// Pull 遇到门槛不下发，且把原因写进 assignment——UI 在机器行与配置集页
// 都要显示得出来。
func TestPullMarksAssignmentFailedOnOldAgent(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-old-3")
	r.setAgentVersion(t, m, "0.1.0")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	r.svc.Pull(m, protocol.ConfigPull{Have: ""})

	assign, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "failed", assign.GetString("state"))
	require.Contains(t, assign.GetString("last_error"), "版本过低")
	require.Empty(t, r.sender.of(protocol.KindConfigSnapshot), "不许下发任何快照")
}

// ---------- Task 3：重注入反查链 ----------

// 本设计的核心不变量：改 Provider → 重注入 → 不产生新 Revision、
// checksum 不变（spec §2.2）。
func TestNotifyProviderDoesNotCreateRevision(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-notify-1")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	before, err := r.revs.Head(setID)
	require.NoError(t, err)
	beforeCount := r.revisionCount(t, setID)

	_, err = r.provs.Update(provID,
		r.inputFor(t, provID, "https://open.bigmodel.cn/api/coding/paas/v4"))
	require.NoError(t, err)
	require.NoError(t, r.svc.NotifyProvider(provID))

	after, err := r.revs.Head(setID)
	require.NoError(t, err)
	require.Equal(t, before.Id, after.Id, "改 Provider 绝不产生新 Revision")
	require.Equal(t, before.GetString("checksum"), after.GetString("checksum"))
	require.Equal(t, beforeCount, r.revisionCount(t, setID))

	notifies := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, notifies, 1)
	n := notifies[0].payload.(protocol.ConfigNotify)
	require.Equal(t, setID, n.ConfigSetID)
	require.Empty(t, n.RevisionID, "仅 secrets 变更，不带 RevisionID")
	require.Equal(t, protocol.ReasonRotated, n.Reason)

	// 新快照里的值确实变了。
	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4", snap.Provider["base_url"])
}

func TestNotifyProviderSkipsPausedSets(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-notify-2")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	set.Set("paused", true)
	require.NoError(t, r.app.Save(set))

	require.NoError(t, r.svc.NotifyProvider(provID))
	require.Empty(t, r.sender.of(protocol.KindConfigNotify),
		"暂停下发的配置集不该收到重注入通知")
}

// 轮换 Provider 用的凭据也要重注入——它落在现有的凭据轮换通道上，
// 但那条通道只认 refs.creds，认不出 relation 引用（spec §5.2 / §5.3）。
func TestNotifyCredentialReachesProviderBoundMachines(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-notify-3")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	require.NoError(t, r.creds.Rotate("zhipu_key", "sk-zhipu-newvalue123"))
	require.NoError(t, r.svc.NotifyCredential("zhipu_key"))

	notifies := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, notifies, 1, "配置集本身没有 cred.* 引用，但 Provider 引用了它")
	n := notifies[0].payload.(protocol.ConfigNotify)
	require.Equal(t, setID, n.ConfigSetID)
	require.Empty(t, n.RevisionID)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-newvalue123", snap.Provider["auth_token"])
}
