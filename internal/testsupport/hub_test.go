//go:build testing

package testsupport_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestNewTestHubServesPocketBaseAPI(t *testing.T) {
	th := testsupport.NewTestHub(t)

	resp, err := http.Get(th.HTTPURL + "/api/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNewTestHubServesEmbeddedUI(t *testing.T) {
	th := testsupport.NewTestHub(t)

	resp, err := http.Get(th.HTTPURL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "Orciny")
}

func TestNewTestHubRunsMigrations(t *testing.T) {
	th := testsupport.NewTestHub(t)
	for _, name := range []string{"machines", "enroll_tokens", "events"} {
		_, err := th.App.FindCollectionByNameOrId(name)
		require.NoError(t, err, "%s 应已由迁移创建", name)
	}
}

func TestNewTestHubURLsAndClock(t *testing.T) {
	th := testsupport.NewTestHub(t)
	require.Contains(t, th.WSURL, "ws://")
	require.Contains(t, th.WSURL, "/api/orciny/ws")
	require.NotNil(t, th.Clock)

	before := th.Clock.Now()
	th.Clock.Advance(time.Minute)
	require.Equal(t, before.Add(time.Minute), th.Clock.Now())
}

func TestNewTestHubAcceptsConfigOverride(t *testing.T) {
	th := testsupport.NewTestHub(t, func(c *hub.Config) {
		c.OfflineGrace = 123 * time.Millisecond
	})
	require.NotNil(t, th.Hub)
}
