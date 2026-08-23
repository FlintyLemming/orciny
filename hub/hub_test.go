package hub_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
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

func TestPublicKeyBeforeServeIsNil(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)

	// 密钥路径依赖 DataDir，只有 OnServe 之后才有值——此前访问必须是 nil 而不是 panic。
	require.Nil(t, h.PublicKey())
}

func TestShutdownBeforeServeIsSafe(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)

	// ws.Handler 要到 OnServe 才构造。关停必须能在此之前调用——
	// app.Cleanup() 触发的 OnTerminate 就会走到这里。
	require.NotPanics(t, h.Shutdown)
	require.NotPanics(t, h.Shutdown, "重复关停必须是安全的")
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

// 库里有凭据但主密钥解不开时必须拒绝启动（spec §6.6）。
func TestServeFailsWhenMasterKeyDoesNotMatch(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	// 先用当前主密钥存一条凭据
	key, err := secretbox.LoadMasterKey(app.DataDir())
	require.NoError(t, err)
	store := credentials.NewStore(app, key, events.NewWriter(app))
	_, err = store.Create("k", "sk-value-1234", "")
	require.NoError(t, err)

	// 再把主密钥换掉，模拟「恢复备份时忘了带密钥文件」
	t.Setenv(secretbox.EnvKeyName,
		base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)
	t.Cleanup(h.Shutdown)

	se := new(core.ServeEvent)
	se.App = app
	router, err := apis.NewRouter(app)
	require.NoError(t, err)
	se.Router = router
	se.Server = &http.Server{}

	err = app.OnServe().Trigger(se, func(*core.ServeEvent) error { return nil })
	require.Error(t, err, "主密钥不匹配必须让 serve 失败")
	require.ErrorContains(t, err, "主密钥")
}
