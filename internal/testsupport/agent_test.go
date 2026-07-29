//go:build testing

package testsupport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
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
