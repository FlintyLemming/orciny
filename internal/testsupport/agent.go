//go:build testing

package testsupport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
)

// TestAgent 是一台已完成 enroll 的测试机器：密钥已生成、hub 公钥已钉扎、
// agent.yml 已写好。
type TestAgent struct {
	Dir         string
	ManagedHome string
	IdentityDir string
	Fingerprint string
	MachineID   string
}

// NewTestAgent 走真实的公开 enroll 入口接入给定的 TestHub。
//
// managedHome 必须显式给出（spec §2.3）：它是被管理的 HOME，
// 即 ~/.claude 所在之处。参数而不是可选项，是为了让「忘了隔离」这件事
// 在编译期就发生——一次疏忽会让集成测试去改开发者本人的 ~/.claude。
// 不关心配置管理的用例传 t.TempDir() 即可。
func NewTestAgent(t *testing.T, th *TestHub, managedHome string) *TestAgent {
	t.Helper()
	require.NotEmpty(t, managedHome, "managedHome 必须显式给出")

	dir := t.TempDir()
	token, _, err := th.Hub.IssueEnrollToken()
	require.NoError(t, err, "签发注册 token")

	res, err := agent.Enroll(context.Background(), agent.EnrollOptions{
		HubURL:     th.HTTPURL,
		Token:      token,
		Dir:        dir,
		RetryDelay: 0,
	})
	require.NoError(t, err, "agent enroll")

	// enroll 写过 agent.yml 了，这里补上 managed_home。
	cfg, err := agent.LoadConfig(dir)
	require.NoError(t, err, "读回 agent.yml")
	cfg.ManagedHome = managedHome
	require.NoError(t, agent.SaveConfig(dir, cfg), "写回 agent.yml")

	return &TestAgent{
		Dir:         dir,
		ManagedHome: managedHome,
		IdentityDir: filepath.Join(dir, "identity"),
		Fingerprint: res.Fingerprint,
		MachineID:   res.MachineID,
	}
}

// Connect 让这台测试机器真的连上 TestHub 并完成握手。
// 返回的 Session 由调用方负责关闭（或用 t.Cleanup）。
func (a *TestAgent) Connect(t *testing.T, th *TestHub) *agent.Session {
	t.Helper()
	s, err := agent.Connect(context.Background(), agent.ConnectOptions{
		Dir:              a.Dir,
		HubURL:           th.HTTPURL,
		HandshakeTimeout: 2 * time.Second,
		ReadTimeout:      2 * time.Second,
	})
	require.NoError(t, err, "agent 连接 hub")
	t.Cleanup(func() { _ = s.Close() })
	return s
}
