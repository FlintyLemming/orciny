//go:build testing

package testsupport_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

// 全链路：建 Provider → 绑定 → 发布 → agent 落盘 → 改 base_url →
// 重注入 → 落盘内容变了而 Revision 没变（spec §11 / §2.2）。
func TestProviderBindingEndToEnd(t *testing.T) {
	th := testsupport.NewTestHub(t)
	home := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, home)

	provID := th.SeedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")

	setID, _ := th.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.claude.model}}"}}`,
	})
	// SeedConfigSet 发的 v1 还没有绑定，绑上再发 v2。
	revID := th.BindConfigSet(t, setID, provID, "glm-5.1")
	require.NoError(t, th.Hub.AssignConfigSet(ta.MachineID, setID, "apply"))

	_, err := agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})
	require.NoError(t, err)

	settings := filepath.Join(home, ".claude", "settings.json")
	got, err := os.ReadFile(settings)
	require.NoError(t, err)
	require.Contains(t, string(got), "https://open.bigmodel.cn/api/anthropic")
	require.Contains(t, string(got), "sk-zhipu-abcdefghij")
	require.Contains(t, string(got), "glm-5.1")
	require.NotContains(t, string(got), "{{provider.")

	// blob 里必须仍然只有占位符——Revision 不可变，写进去就洗不掉。
	blob := th.RevisionBlob(t, revID, ".claude/settings.json")
	require.Contains(t, string(blob), "{{provider.claude.auth_token}}")
	require.NotContains(t, string(blob), "sk-zhipu")

	// —— 改 Provider 的 base_url ——
	require.NoError(t, th.Hub.UpdateProvider(provID, th.ProviderInputOf(t, provID,
		"https://open.bigmodel.cn/api/coding/paas/v4")))

	_, err = agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})
	require.NoError(t, err)

	got, err = os.ReadFile(settings)
	require.NoError(t, err)
	require.Contains(t, string(got), "https://open.bigmodel.cn/api/coding/paas/v4",
		"落盘内容必须跟着变")

	set, err := th.App.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, revID, set.GetString("head"),
		"改 Provider 绝不产生新 Revision（spec §2.2 的核心不变量）")

	// 也不产生漂移（spec §4.3）：state.json 记的是渲染**后**的 hash，
	// 重注入后重渲染会把它一并更新。这条是 M1 设计正确性的红利，
	// 没有一行新代码支撑它——所以必须有测试盯着。
	items, err := agent.LocalDrift(ta.Dir)
	require.NoError(t, err)
	require.Empty(t, items, "重注入之后不该有任何漂移")
}

// 低版本 agent 收不到含绑定的快照，指派置 failed 且错误信息可归因（spec §10）。
func TestOldAgentGetsAttributableFailure(t *testing.T) {
	th := testsupport.NewTestHub(t)
	home := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, home)

	// 把 agent 上报的版本改老，模拟一台还没升级的机器。
	//
	// 不能直接改 machines.agent_version：握手后的首条 MachineInfo 会用
	// agent 自报的版本覆盖它（machines.UpdateInfo）。orciny.Version 是 var，
	// 改它才是「这台机器真的是 0.1.0」的忠实模拟。
	// MinProviderAgentVersion 是另一个变量，门槛仍然是 0.2.0。
	realVersion := orciny.Version
	orciny.Version = "0.1.0"
	t.Cleanup(func() { orciny.Version = realVersion })

	provID := th.SeedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID, _ := th.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`,
	})
	th.BindConfigSet(t, setID, provID, "glm-5.1")
	require.NoError(t, th.Hub.AssignConfigSet(ta.MachineID, setID, "apply"))

	_, _ = agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})

	th.RequireAssignmentState(t, ta.MachineID, "failed")
	recs, err := th.App.FindRecordsByFilter("assignments", "machine = {:m}", "", 1, 0,
		map[string]any{"m": ta.MachineID})
	require.NoError(t, err)
	require.Contains(t, recs[0].GetString("last_error"), "版本过低")

	// 机器上什么都不该被写。
	_, err = os.Stat(filepath.Join(home, ".claude", "settings.json"))
	require.True(t, os.IsNotExist(err))
}

// 双端点：claude 端点注入到 settings.json，openai 端点注入到另一个文件。
// 本期 openai 端点没有消费者，但「记下来的配置」要能一路走通到落盘。
func TestBothEndpointsReachDisk(t *testing.T) {
	th := testsupport.NewTestHub(t)
	home := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, home)

	provID := th.SeedProviderWithOpenAI(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdef123456",
		"https://open.bigmodel.cn/api/paas/v4", "glm-5.2")

	setID, _ := th.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"}}`,
		".codex/config.toml": "base_url = \"{{provider.openai.base_url}}\"\n" +
			"model = \"{{provider.openai.model}}\"\n",
	})
	revID := th.BindConfigSet(t, setID, provID, "glm-5.2")
	require.NoError(t, th.Hub.AssignConfigSet(ta.MachineID, setID, "apply"))

	_, err := agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})
	require.NoError(t, err)

	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	require.NoError(t, err)
	require.Contains(t, string(settings), "https://open.bigmodel.cn/api/anthropic")
	require.Contains(t, string(settings), "sk-zhipu-abcdef123456")
	require.NotContains(t, string(settings), "{{provider.")

	codex, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	require.NoError(t, err)
	require.Contains(t, string(codex), "https://open.bigmodel.cn/api/paas/v4")
	require.Contains(t, string(codex), "glm-5.2")
	require.NotContains(t, string(codex), "{{provider.")

	// 库里仍然只有占位符——Revision 不可变，写进去就洗不掉。
	blob := th.RevisionBlob(t, revID, ".claude/settings.json")
	require.Contains(t, string(blob), "{{provider.claude.auth_token}}")
	require.NotContains(t, string(blob), "sk-zhipu")
}
