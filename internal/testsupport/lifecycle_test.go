//go:build testing

package testsupport_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestConnectMarksMachineOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	require.Equal(t, "offline", th.MachineStatus(t, ta.MachineID))

	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")
	th.RequireEvent(t, ta.MachineID, "machine.connected")
}

func TestMachineInfoLandsInRecord(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.Eventually(t, func() bool {
		r, err := th.App.FindRecordById("machines", ta.MachineID)
		return err == nil && r.GetString("agent_version") != ""
	}, 3*time.Second, 10*time.Millisecond)
}

// 断开后 6 秒无重连 → offline（spec §12.2）
func TestDisconnectGoesOfflineAfterGrace(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.NoError(t, s.Close())

	th.AdvanceUntilStatus(t, ta.MachineID, "offline", 6*time.Second)
	th.RequireEvent(t, ta.MachineID, "machine.disconnected")
}

// 断开后 3 秒内重连 → 状态始终 online，不产生 disconnected 事件
func TestReconnectWithinGraceStaysOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.NoError(t, s.Close())
	th.Clock.Advance(3 * time.Second) // 宽限期内，无论定时器挂没挂上都不该到期

	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")
	th.Clock.Advance(10 * time.Second) // 越过原定的到期点

	require.Equal(t, "online", th.MachineStatus(t, ta.MachineID))
	require.NotContains(t, th.EventKinds(t, ta.MachineID), "machine.disconnected")
}

// 同指纹二次连接 → 旧连接被踢，状态保持 online，不产生 disconnected 事件
func TestSecondConnectionKeepsOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	first := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	second := ta.Connect(t, th)
	require.NotNil(t, second)

	select {
	case <-first.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("旧连接未被踢掉")
	}

	th.Clock.Advance(time.Minute)
	require.Equal(t, "online", th.MachineStatus(t, ta.MachineID))
	require.NotContains(t, th.EventKinds(t, ta.MachineID), "machine.disconnected")
}

// 删除机器 → 活跃连接被踢
func TestDeleteMachineKicksConnection(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.NoError(t, th.App.Delete(m))

	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("删除机器后连接应被关闭")
	}

	_, err = th.App.FindRecordById("machines", ta.MachineID)
	require.Error(t, err, "记录应已删除")
}

// 三个 agent 并发接入同一 hub → 各自独立
func TestThreeAgentsConnectIndependently(t *testing.T) {
	th := testsupport.NewTestHub(t)

	agents := make([]*testsupport.TestAgent, 3)
	for i := range agents {
		agents[i] = testsupport.NewTestAgent(t, th, t.TempDir())
		agents[i].Connect(t, th)
	}
	for _, a := range agents {
		th.RequireStatus(t, a.MachineID, "online")
	}

	// 掐掉中间那台
	s := agents[1].Connect(t, th) // 取得一个可关闭的句柄（新连接顶替旧的）
	require.NoError(t, s.Close())

	th.AdvanceUntilStatus(t, agents[1].MachineID, "offline", 6*time.Second)
	require.Equal(t, "online", th.MachineStatus(t, agents[0].MachineID))
	require.Equal(t, "online", th.MachineStatus(t, agents[2].MachineID))
}

// hub 重启 → 幽灵 online 被清理
func TestHubRestartClearsGhosts(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	restarted := th.Restart(t)
	require.Equal(t, "offline", restarted.MachineStatus(t, ta.MachineID),
		"重启后不该留下幽灵 online")
}

// agent.Run 的完整装配：从 agent.yml + identity 起步，连上 hub 让面板转
// online，收到取消信号后干净退出。退避时长由计划 7 Task 2 的假时钟用例断言，
// 这里只验证「线接对了」。
func TestAgentRunConnectsAndStopsCleanly(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, agent.RunOptions{
			Dir:              ta.Dir,
			HandshakeTimeout: 2 * time.Second,
			ReadTimeout:      2 * time.Second,
		})
	}()

	th.RequireStatus(t, ta.MachineID, "online")

	// 运行状态已落盘，status 子命令据此报告
	require.Eventually(t, func() bool {
		s, err := agent.LoadStatus(ta.Dir)
		return err == nil && s.State == "connected"
	}, 3*time.Second, 10*time.Millisecond)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
