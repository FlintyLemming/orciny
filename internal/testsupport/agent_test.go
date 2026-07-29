//go:build testing

package testsupport_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestEnrollEndToEnd(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	require.Len(t, ta.Fingerprint, 32)

	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.Equal(t, ta.Fingerprint, m.GetString("fingerprint"))
	require.Equal(t, "offline", m.GetString("status"))
	require.NotEmpty(t, m.GetString("hostname"))
	require.NotEmpty(t, m.GetString("agent_version"))

	cfg, err := agent.LoadConfig(ta.Dir)
	require.NoError(t, err)
	require.Equal(t, th.HTTPURL, cfg.HubURL)
	require.Equal(t, ta.MachineID, cfg.MachineID)
}

func TestReEnrollSameMachine(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	token, _, err := th.Hub.IssueEnrollToken()
	require.NoError(t, err)
	res, err := agent.Enroll(context.Background(), agent.EnrollOptions{
		HubURL: th.HTTPURL, Token: token, Dir: ta.Dir, RetryDelay: 0,
	})
	require.NoError(t, err)
	require.Equal(t, ta.MachineID, res.MachineID, "同一目录重装应复用同一台机器")

	machines, err := th.App.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1)
}

func TestThreeAgentsEnrollIndependently(t *testing.T) {
	th := testsupport.NewTestHub(t)

	fps := map[string]bool{}
	for i := 0; i < 3; i++ {
		ta := testsupport.NewTestAgent(t, th)
		require.False(t, fps[ta.Fingerprint], "三台机器的指纹必须互不相同")
		fps[ta.Fingerprint] = true
	}

	machines, err := th.App.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 3)
}

func TestAgentHandshakeAgainstRealHub(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	s := ta.Connect(t, th)
	select {
	case <-s.Done():
		t.Fatalf("连接不该立刻结束: %v", s.Err())
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAgentWithTamperedHubKeyRefusesToConnect(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	// 篡改钉扎的 hub 公钥（对应 DoD #7）
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(ta.IdentityDir, "hub.pub"),
		[]byte(protocol.EncodePublicKey(other)+"\n"), 0o600))

	_, err = agent.Connect(context.Background(), agent.ConnectOptions{
		Dir: ta.Dir, HubURL: th.HTTPURL,
		HandshakeTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
	})
	require.Error(t, err, "hub 签名验证失败必须阻止连接建立")
}

func TestUnenrolledAgentIsRejectedWithCode(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	// 删掉 hub 侧的机器记录，模拟「指纹未登记」
	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.NoError(t, th.App.Delete(m))

	_, err = agent.Connect(context.Background(), agent.ConnectOptions{
		Dir: ta.Dir, HubURL: th.HTTPURL,
		HandshakeTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "code=1", "应带 CodeUnknownFingerprint")
}
