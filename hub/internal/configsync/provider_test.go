package configsync_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

// seedProvider 建一条只配了 claude 端点的服务配置，返回 provider 记录 id。
// key 内联在平台级（M1.6 spec §2.3）。
func (r *rig) seedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	return r.seedProviderWith(t, name, baseURL, key, "", "")
}

// seedProviderWith 可以额外配上 openai 端点。openaiURL 为空即不配。
func (r *rig) seedProviderWith(
	t *testing.T, name, baseURL, key, openaiURL, openaiModel string,
) string {
	t.Helper()
	in := providers.Input{
		Name: name,
		Key:  &key,
		Claude: providers.ClaudeEndpointInput{
			BaseURL:   baseURL,
			AuthField: providers.AuthToken,
			Models:    []providers.ClaudeModel{{Name: "glm-5.1"}},
		},
	}
	if openaiURL != "" {
		in.OpenAI = providers.OpenAIEndpointInput{
			BaseURL:      openaiURL,
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{openaiModel},
			DefaultModel: openaiModel,
		}
	}
	rec, err := r.provs.Create(in)
	require.NoError(t, err)
	return rec.Id
}

// seedProviderWithTwoKeys 建一条平台级与 claude 端点级各有一把 key 的 provider。
func (r *rig) seedProviderWithTwoKeys(
	t *testing.T, name, platformKey, claudeKey string,
) string {
	t.Helper()
	rec, err := r.provs.Create(providers.Input{
		Name: name,
		Key:  &platformKey,
		Claude: providers.ClaudeEndpointInput{
			BaseURL:   "https://relay.example/anthropic",
			AuthField: providers.AuthToken,
			Models:    []providers.ClaudeModel{{Name: "relay-max"}},
			Key:       &claudeKey,
		},
		OpenAI: providers.OpenAIEndpointInput{
			BaseURL:      "https://relay.example/v1",
			AuthField:    providers.DefaultOpenAIAuthField,
			Models:       []string{"relay-max"},
			DefaultModel: "relay-max",
		},
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

// inputFor 读回一条 Provider 的当前值，只把 claude 端点的 base_url 换成新的。
// 三处 Key 都留 nil = 不修改。
func (r *rig) inputFor(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	rec, err := r.provs.Get(providerID)
	require.NoError(t, err)
	cl := providers.ClaudeOf(rec)
	oa := providers.OpenAIOf(rec)
	return providers.Input{
		Name:   rec.GetString("name"),
		Preset: rec.GetString("preset"),
		Note:   rec.GetString("note"),
		Claude: providers.ClaudeEndpointInput{
			BaseURL: baseURL, AuthField: cl.AuthField,
			Models: cl.Models, Defaults: cl.Defaults,
		},
		OpenAI: providers.OpenAIEndpointInput{
			BaseURL: oa.BaseURL, AuthField: oa.AuthField,
			Models: oa.Models, DefaultModel: oa.DefaultModel,
		},
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
			`"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.claude.model}}"}}`,
	}, &providers.Binding{Provider: provID, Models: fullSlots("glm-5.1")})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"claude.base_url":   "https://open.bigmodel.cn/api/anthropic",
		"claude.auth_token": "sk-zhipu-abcdefghij",
		"claude.model":      "glm-5.1",
	}, snap.Provider, "只发被引用到的三个键，四槽里没被引用的不发")
}

// 没引用的键一个都不下发——少一个键少一处泄露面（spec §5.1）。
func TestSnapshotOmitsUnreferencedProviderKeys(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-2")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "主模型是 {{provider.claude.model}}",
	}, &providers.Binding{Provider: provID, Models: fullSlots("glm-5.1")})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"claude.model": "glm-5.1"}, snap.Provider)
	require.NotContains(t, snap.Provider, "claude.auth_token", "没引用就绝不下发 key")
}

// 纵深防御：这种状态本应被发布校验挡住（spec §5.1 / §7）。
func TestSnapshotRefusesProviderRefsWithoutBinding(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-prov-3")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
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
		".claude/settings.json": `{"env":{"ANTHROPIC_MODEL":"{{provider.claude.model}}"}}`,
	}, &providers.Binding{Provider: provID}) // 四槽全空 = 透传
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrEmptyModelSlot)
	require.Contains(t, err.Error(), "provider.claude.model")
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
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrAgentTooOld)
	require.Contains(t, err.Error(), "0.1.0")
	require.Contains(t, err.Error(), "0.3.0")
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
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
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
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
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
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4",
		snap.Provider["claude.base_url"])
}

func TestNotifyProviderSkipsPausedSets(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-notify-2")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
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

// git describe 注入的版本形如 v0.2.0-38-g3161fd4：语义上是「v0.2.0 之后
// 第 38 个提交」，但按 semver 带 pre-release 的版本小于同号正式版。
// 不丢掉 pre-release 就会把每一个非 tag 构建都判成过老。
func TestSnapshotAcceptsGitDescribeVersion(t *testing.T) {
	for _, ver := range []string{"v0.3.0-38-g3161fd4", "0.3.0", "v0.3.0", "v0.4.0-1-gabcdef"} {
		t.Run(ver, func(t *testing.T) {
			r := newRig(t)
			m := r.machine(t, "fp-ver-"+ver)
			r.setAgentVersion(t, m, ver)
			provID := r.seedProvider(t, "智谱 GLM · 个人",
				"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
			setID := r.seedSetWithBinding(t, map[string]string{
				".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
			}, &providers.Binding{Provider: provID})
			_, err := r.sets.Assign(m, setID, "apply")
			require.NoError(t, err)

			_, err = r.svc.Snapshot(m)
			require.NoError(t, err, "%s 应当被接受", ver)
		})
	}
}

// 真正过老的仍然要被拦住，包括它的 pre-release 形态。
func TestSnapshotStillRefusesOldPrereleaseVersion(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-ver-old")
	r.setAgentVersion(t, m, "v0.2.0-5-gdeadbee")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrAgentTooOld)
}

// ---------- M1.6：九键取值与端点缺失 ----------

func TestProviderValuesMapsAllNineKeys(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-nine")
	provID := r.seedProviderWith(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-5.2")

	// 一个文件把九个键全引一遍。
	var sb strings.Builder
	for _, k := range protocol.ProviderKeys {
		sb.WriteString("{{provider." + k + "}}\n")
	}
	setID := r.seedSetWithBinding(t, map[string]string{"CLAUDE.md": sb.String()},
		&providers.Binding{Provider: provID, Models: providers.ModelSlots{
			Main: "glm-5.2[1m]", Opus: "glm-5.2[1m]",
			Sonnet: "glm-5.2[1m]", Haiku: "glm-4.7",
		}})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"claude.base_url":     "https://open.bigmodel.cn/api/anthropic",
		"claude.auth_token":   "sk-zhipu-abcdef123456",
		"claude.model":        "glm-5.2[1m]",
		"claude.model_opus":   "glm-5.2[1m]",
		"claude.model_sonnet": "glm-5.2[1m]",
		"claude.model_haiku":  "glm-4.7",
		"openai.base_url":     "https://open.bigmodel.cn/api/paas/v4",
		"openai.api_key":      "sk-zhipu-abcdef123456",
		"openai.model":        "glm-5.2",
	}, snap.Provider)
}

// openai.model 取的是 provider 的 default_model，不是 binding（spec §3.4）。
func TestOpenAIModelComesFromProviderDefault(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-oa-model")
	provID := r.seedProviderWith(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-4.7")

	setID := r.seedSetWithBinding(t,
		map[string]string{"CLAUDE.md": "{{provider.openai.model}}"},
		&providers.Binding{Provider: provID, Models: fullSlots("glm-5.2")})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, "glm-4.7", snap.Provider["openai.model"],
		"binding 的四槽只管 claude 端点")
}

// 端点级 key 覆盖平台级，快照里两个端点各拿各的。
func TestProviderValuesUsesPerEndpointKeys(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-two-keys")
	provID := r.seedProviderWithTwoKeys(t, "两把 key 的中转",
		"sk-platform-000000", "sk-claude-side-111111")

	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "{{provider.claude.auth_token}} {{provider.openai.api_key}}",
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-side-111111", snap.Provider["claude.auth_token"])
	require.Equal(t, "sk-platform-000000", snap.Provider["openai.api_key"])
}

// 纵深防御：本应被发布校验挡住，走到这里说明有路径绕过了它（spec §4.3）。
func TestProviderValuesRejectsUnconfiguredEndpoint(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-ep-missing")
	provID := r.seedProvider(t, "只有 claude",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456")

	setID := r.seedSetWithBinding(t,
		map[string]string{".codex/config.toml": "u = \"{{provider.openai.base_url}}\"\n"},
		&providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	_, err = r.svc.Snapshot(m)
	require.ErrorIs(t, err, configsync.ErrEndpointMissing)
	require.Contains(t, err.Error(), "openai")
}

// 端点缺失走到 Pull：指派置 failed 且**不置** degraded——
// 机器上什么都没被改过，不需要人工解除（spec §4.3）。
func TestPullMarksAssignmentFailedOnEndpointMissing(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-ep-fail")
	provID := r.seedProvider(t, "只有 claude",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456")
	setID := r.seedSetWithBinding(t,
		map[string]string{".codex/config.toml": "u = \"{{provider.openai.base_url}}\"\n"},
		&providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	r.svc.Pull(m, protocol.ConfigPull{Have: ""})

	assign, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "failed", assign.GetString("state"))
	require.NotEqual(t, "degraded", assign.GetString("state"),
		"机器上什么都没被改过，不需要人工解除")
	require.Contains(t, assign.GetString("last_error"), "openai")
	require.Empty(t, r.sender.of(protocol.KindConfigSnapshot), "不许下发任何快照")
}

// 轮换 provider 的 key → 重注入全机队、不产生新 Revision。
// 这是 M1.5「改 Provider 不产生新 Revision」不变量在 key 内联之后的延续。
func TestRotatingProviderKeyReinjectsWithoutNewRevision(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp-rotate")
	provID := r.seedProvider(t, "智谱 GLM",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-old-000000")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"}}`,
	}, &providers.Binding{Provider: provID})
	_, err := r.sets.Assign(m, setID, "apply")
	require.NoError(t, err)

	before := r.revisionCount(t, setID)

	in := r.inputFor(t, provID, "https://open.bigmodel.cn/api/anthropic")
	newKey := "sk-zhipu-new-999999"
	in.Key = &newKey
	_, err = r.provs.Update(provID, in)
	require.NoError(t, err)
	require.NoError(t, r.svc.NotifyProvider(provID))

	require.Equal(t, before, r.revisionCount(t, setID),
		"轮换 key 绝不产生新 Revision")

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-new-999999", snap.Provider["claude.auth_token"])

	// 收到的是不带 RevisionID 的 ConfigNotify（= 仅 secrets 变更）。
	notifies := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, notifies, 1)
	n := notifies[0].payload.(protocol.ConfigNotify)
	require.Empty(t, n.RevisionID)
	require.Equal(t, protocol.ReasonRotated, n.Reason)
}
