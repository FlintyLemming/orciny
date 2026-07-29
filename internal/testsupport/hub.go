//go:build testing

package testsupport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// TestHub 是一个跑在内存里的完整 hub：真实路由、真实 HTTP 服务、临时数据库。
type TestHub struct {
	App     *tests.TestApp
	Hub     *hub.Hub
	Clock   *clock.Fake
	Server  *httptest.Server
	HTTPURL string // http://127.0.0.1:PORT
	WSURL   string // ws://127.0.0.1:PORT/api/orciny/ws
}

// FakeStart 是假时钟的起点，固定值让失败日志可读。
var FakeStart = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// NewTestHub 起一个测试 hub，并注册好 t.Cleanup。
//
// 默认配置里，走假时钟的时长保持生产值（宽限 5s、心跳 30s），
// 走真实网络 deadline 的时长被压到毫秒级——后者假时钟管不到。
func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub {
	t.Helper()

	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err, "创建 TestApp")
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(FakeStart)
	cfg := hub.Config{
		Clock:             fake,
		OfflineGrace:      5 * time.Second,
		HeartbeatInterval: 30 * time.Second,
		ReadTimeout:       2 * time.Second,
		HandshakeTimeout:  500 * time.Millisecond,
		EnrollTokenTTL:    15 * time.Minute,
	}
	for _, o := range opts {
		o(&cfg)
	}

	h, err := hub.Attach(app, cfg)
	require.NoError(t, err, "Attach hub")

	// 下面这段复刻 PocketBase 自身 apis.Serve 的装配顺序：
	// 建 router → 造 ServeEvent → 触发 OnServe（我们的注册就在里面）→ BuildMux。
	router, err := apis.NewRouter(app)
	require.NoError(t, err, "建 router")

	se := new(core.ServeEvent)
	se.App = app
	se.Router = router
	se.Server = &http.Server{}
	require.NoError(t, app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		e.Server.Handler = mux
		return nil
	}), "触发 OnServe")

	srv := httptest.NewServer(se.Server.Handler)
	t.Cleanup(srv.Close)

	return &TestHub{
		App:     app,
		Hub:     h,
		Clock:   fake,
		Server:  srv,
		HTTPURL: srv.URL,
		WSURL:   "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/orciny/ws",
	}
}
