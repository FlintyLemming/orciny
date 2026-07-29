//go:build testing

package testsupport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
)

// TestAgent 是一台已完成 enroll 的测试机器：密钥已生成、hub 公钥已钉扎、
// agent.yml 已写好。它不含连接逻辑——那由计划 5 补上。
type TestAgent struct {
	Dir         string
	IdentityDir string
	Fingerprint string
	MachineID   string
}

// NewTestAgent 走真实的公开 enroll 入口接入给定的 TestHub。
func NewTestAgent(t *testing.T, th *TestHub) *TestAgent {
	t.Helper()

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

	return &TestAgent{
		Dir:         dir,
		IdentityDir: filepath.Join(dir, "identity"),
		Fingerprint: res.Fingerprint,
		MachineID:   res.MachineID,
	}
}
