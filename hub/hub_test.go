package hub_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/clock"
)

func TestConfigWithDefaults(t *testing.T) {
	c := hub.Config{}.WithDefaults()
	require.NotNil(t, c.Clock)
	require.Equal(t, 5*time.Second, c.OfflineGrace)
	require.Equal(t, 30*time.Second, c.HeartbeatInterval)
	require.Equal(t, 70*time.Second, c.ReadTimeout)
	require.Equal(t, 10*time.Second, c.HandshakeTimeout)
	require.Equal(t, 15*time.Minute, c.EnrollTokenTTL)
	require.Equal(t, orciny.MinAgentVersion, c.MinAgentVersion)
}

func TestConfigWithDefaultsKeepsExplicitValues(t *testing.T) {
	f := clock.NewFake(time.Now())
	c := hub.Config{Clock: f, OfflineGrace: time.Second}.WithDefaults()
	require.Same(t, f, c.Clock)
	require.Equal(t, time.Second, c.OfflineGrace)
}

func TestAttachSucceedsOnTestApp(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)
	require.NotNil(t, h)
}

func TestStartOnAttachedHubIsRejected(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)

	err = h.Start()
	require.Error(t, err, "Attach 出来的 hub 没有 pocketbase 实例，Start 必须明确报错而不是 panic")
}
