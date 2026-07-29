//go:build testing

package testsupport

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// 留给 Restart：重建时要用同一组配置改写函数。
	cfgFns []func(*hub.Config)
}

// FakeStart 是假时钟的起点，固定值让失败日志可读。
var FakeStart = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// NewTestHub 起一个测试 hub，并注册好 t.Cleanup。
//
// 默认配置里，走假时钟的时长保持生产值（宽限 5s、心跳 30s），
// 走真实网络 deadline 的时长被压到毫秒级——后者假时钟管不到。
func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub {
	t.Helper()
	return newTestHubAt(t, t.TempDir(), opts...)
}

// newTestHubAt 用 seedDir 作数据种子起一个 hub。tests.NewTestApp 会把种子目录
// 克隆到别处再工作，因此 seedDir 本身不会被写。
func newTestHubAt(t *testing.T, seedDir string, opts ...func(*hub.Config)) *TestHub {
	t.Helper()

	app, err := tests.NewTestApp(seedDir)
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
	// t.Cleanup 是后进先出：这两条都排在 app.Cleanup 之后注册，因此都先于它跑。
	//
	// 必须先排空 hub 的 WS 读循环再拆库。httptest.Server.Close 不会等被劫持的
	// 连接（Go 在 StateHijacked 上直接 wg.Done），所以指望它同步是没用的：
	// 一条在途的 MachineInfo 会在库拆掉之后落到 Manager 的写库路径上。
	t.Cleanup(h.Shutdown)
	t.Cleanup(srv.Close)

	return &TestHub{
		App:     app,
		Hub:     h,
		Clock:   fake,
		Server:  srv,
		HTTPURL: srv.URL,
		WSURL:   "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/orciny/ws",
		cfgFns:  opts,
	}
}

// Restart 在同一份数据上重新装配一个 hub，用于验证 hub 重启后的行为。
// 旧的 TestHub 在此之后不应再使用。
//
// 顺序要紧：tests.NewTestApp 会克隆传入的目录，而 App.Cleanup() 会把
// 当前数据目录整个删掉。所以先快照，再让旧实例退场，最后用快照重建。
func (h *TestHub) Restart(t *testing.T) *TestHub {
	t.Helper()

	snapshot := t.TempDir()
	require.NoError(t, copyDir(h.App.DataDir(), snapshot), "快照数据目录")

	h.Server.Close()
	h.Hub.Shutdown()
	h.App.Cleanup() // t.Cleanup 里还会再调一次，重复调用是安全的

	return newTestHubAt(t, snapshot, h.cfgFns...)
}

// MachineStatus 读机器当前状态。
func (h *TestHub) MachineStatus(t *testing.T, machineID string) string {
	t.Helper()
	r, err := h.App.FindRecordById("machines", machineID)
	require.NoError(t, err)
	return r.GetString("status")
}

// RequireStatus 等到机器状态变成 want 为止。
//
// 用 Eventually 而不是直接断言：状态变化发生在另一个 goroutine 里
// （宽限定时器、WS 读循环），需要给它一点真实时间落地——但等待的是
// 「效果出现」，不是「睡够 5 秒」。
func (h *TestHub) RequireStatus(t *testing.T, machineID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		r, err := h.App.FindRecordById("machines", machineID)
		return err == nil && r.GetString("status") == want
	}, 3*time.Second, 10*time.Millisecond, "机器 %s 未变成 %s", machineID, want)
}

// AdvanceUntilStatus 每轮推进 step 逻辑时间，直到机器状态变成 want。
//
// 为什么不能「先等 Clock.TimerCount 到某个数，再一次性 Advance」：WS 的心跳
// ticker 跟宽限定时器共用这只假时钟，连接一上线 TimerCount 就已经不为零，
// 数个数分辨不出宽限定时器有没有挂上。而宽限定时器是在 WS 读循环那个
// goroutine 上挂的，测试无法准确知道时刻。改成在轮询里反复推进：定时器
// 一挂上，下一轮推进就会触发它。
//
// 条件函数跑在 Eventually 自己的 goroutine 上，因此里面不能用 require。
func (h *TestHub) AdvanceUntilStatus(t *testing.T, machineID, want string, step time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		h.Clock.Advance(step)
		r, err := h.App.FindRecordById("machines", machineID)
		return err == nil && r.GetString("status") == want
	}, 5*time.Second, 20*time.Millisecond, "机器 %s 未变成 %s", machineID, want)
}

// RequireEvent 等到某机器出现指定 kind 的事件为止。
//
// 不能在 RequireStatus 之后直接断言事件：状态与事件是两次独立的写库，
// Manager 先写状态再写事件，所以「状态已经翻了」的那一刻事件可能还没落地。
func (h *TestHub) RequireEvent(t *testing.T, machineID, kind string) {
	t.Helper()
	require.Eventually(t, func() bool {
		recs, err := h.App.FindRecordsByFilter("events", "machine = {:m} && kind = {:k}", "", 1, 0,
			map[string]any{"m": machineID, "k": kind})
		return err == nil && len(recs) > 0
	}, 3*time.Second, 10*time.Millisecond, "机器 %s 未产生 %s 事件", machineID, kind)
}

// EventKinds 返回某机器的事件 kind 列表，按时间正序。
func (h *TestHub) EventKinds(t *testing.T, machineID string) []string {
	t.Helper()
	recs, err := h.App.FindRecordsByFilter("events", "machine = {:m}", "created", 0, 0,
		map[string]any{"m": machineID})
	require.NoError(t, err)
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("kind"))
	}
	return out
}

// copyDir 把 src 下的内容平铺复制到 dst（只需处理一层子目录，
// PocketBase 的数据目录就是这个形状）。
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(dp, 0o700); err != nil {
				return err
			}
			if err := copyDir(sp, dp); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(sp)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dp, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}
