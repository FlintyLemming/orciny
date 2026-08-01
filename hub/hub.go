// Package hub 是 hub 侧唯一的对外入口。
// 实现细节全部藏在 hub/internal/*，由 Go 的 internal 规则保证 agent 侧碰不到（spec §2.2）。
package hub

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
	"github.com/FlintyLemming/orciny/hub/internal/ws"

	// 空 import 触发 init()，把初始迁移注册进 core.AppMigrations。
	// 这也让 internal/testsupport（只能看到公开包）间接获得迁移。
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

// Hub 嵌入 core.App，子系统作字段，业务全部挂在 OnServe 上（spec §5.1）。
type Hub struct {
	core.App

	cfg Config

	identity *identity.Store
	events   *events.Writer
	enroll   *enroll.Service
	machines *machines.Manager
	ws       *ws.Handler
	creds    *credentials.Store

	// pb 仅在 New 创建时非 nil。Attach 出来的实例由调用方驱动 serve。
	pb *pocketbase.PocketBase
}

// New 创建生产用的 hub：内部是一个完整的 PocketBase 应用。
func New(cfg Config) (*Hub, error) {
	pb := pocketbase.New()
	h, err := Attach(pb, cfg)
	if err != nil {
		return nil, err
	}
	h.pb = pb
	return h, nil
}

// Attach 把 hub 的子系统绑定到任意 core.App 上。
// 生产由 New 调用；测试脚手架用它绑到 tests.TestApp（spec §12.1）。
//
// 约定：这里只做「注册」，不做任何需要数据库或数据目录的事——
// 那些一律推迟到 OnServe，因为此刻 app 还没 Bootstrap。
func Attach(app core.App, cfg Config) (*Hub, error) {
	h := &Hub{App: app, cfg: cfg.WithDefaults()}

	// 这两个子系统只需要 core.App，不需要数据目录，因此可以在这里就构造。
	h.events = events.NewWriter(app)
	h.enroll = enroll.NewService(app, h.events, h.cfg.Clock, h.cfg.EnrollTokenTTL, rand.Reader)
	h.machines = machines.NewManager(app, h.events, h.cfg.Clock, h.cfg.OfflineGrace)

	// record hook 绑在 Attach 而不是 OnServe 里：OnServe 每次 serve 都会触发，
	// 绑在里面会重复注册同一个 handler。
	app.OnRecordAfterDeleteSuccess("machines").BindFunc(h.machines.OnRecordDeleted)

	// OnTerminate 跑在数据库真正关闭之前，正好是排空读循环的时机。
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		h.Shutdown()
		return e.Next()
	})

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// 后续计划在此依次插入：自定义路由注册、连接管理器启动。
		// 顺序即依赖顺序，不要打乱。

		// DataDir 只有在 Bootstrap 之后才有值，所以密钥路径必须在这里算，
		// 不能在 Attach 时算。
		h.identity = identity.NewStore(filepath.Join(e.App.DataDir(), identity.KeyFileName))
		if err := h.identity.Load(); err != nil {
			return fmt.Errorf("加载 hub 密钥: %w", err)
		}
		e.App.Logger().Info("hub 身份就绪", "fingerprint", h.identity.Fingerprint())

		// 凭据主密钥。必须排在 ws 之前：一台解不开凭据的 hub 不该接客
		// ——它会把空值下发到全机队（spec §6.6）。
		key, err := credentials.LoadMasterKey(e.App.DataDir())
		if err != nil {
			return fmt.Errorf("加载凭据主密钥: %w", err)
		}
		h.creds = credentials.NewStore(e.App, key, h.events)
		if err := h.creds.VerifyAll(); err != nil {
			return err
		}

		// 内存里的注册表此刻是空的，库里可能还留着上次进程退出时的 online。
		// 必须赶在 WS 端点接客之前翻掉，否则会和真实重连交叉写状态。
		if err := h.machines.ResetGhosts(); err != nil {
			return fmt.Errorf("清理幽灵 online: %w", err)
		}

		// ws 依赖已加载的密钥（要用它签挑战），因此必须排在 identity.Load 之后。
		h.ws = ws.NewHandler(ws.Deps{
			App:               e.App,
			Handshake:         handshake.NewServer(handshake.NewAppStore(e.App), h.identity, h.cfg.MinAgentVersion, rand.Reader),
			Registry:          h.machines,
			Events:            h.events,
			Clock:             h.cfg.Clock,
			HandshakeTimeout:  h.cfg.HandshakeTimeout,
			ReadTimeout:       h.cfg.ReadTimeout,
			HeartbeatInterval: h.cfg.HeartbeatInterval,
		})

		if err := routes.Register(e, routes.Deps{
			Enroll:       h.enroll,
			Identity:     h.identity,
			WS:           h.ws,
			Version:      orciny.Version,
			DownloadBase: h.cfg.DownloadBase,
		}); err != nil {
			return fmt.Errorf("注册路由: %w", err)
		}

		// PocketBase 自带 cron 调度器，不必自己起 goroutine。
		// 清理失败只记日志——它不该拖垮 serve。
		e.App.Cron().MustAdd("purge_enroll_tokens", "0 * * * *", func() {
			n, err := h.enroll.PurgeExpiredTokens(24 * time.Hour)
			if err != nil {
				e.App.Logger().Warn("清理过期注册 token 失败", "error", err)
				return
			}
			if n > 0 {
				e.App.Logger().Info("已清理过期注册 token", "count", n)
			}
		})

		if err := h.registerUI(e); err != nil {
			return err
		}
		return e.Next()
	})

	return h, nil
}

// registerUI 由 ui_prod.go / ui_dev.go 按 build tag 分别实现：
// 生产挂内嵌 dist，dev 反代给 Vite。

// PublicKey 返回 hub 的公钥。OnServe 之前返回 nil。
func (h *Hub) PublicKey() ed25519.PublicKey {
	if h.identity == nil {
		return nil
	}
	return h.identity.PublicKey()
}

// IssueEnrollToken 签发一枚一次性注册 token。
// 这是 hub 包对外暴露的唯一「管理动作」，供测试脚手架绕开 HTTP 认证使用。
func (h *Hub) IssueEnrollToken() (string, time.Time, error) {
	return h.enroll.IssueToken()
}

// Shutdown 有序停掉 hub 的运行时部件：先踢连接，再等读循环退出，最后停
// 宽限定时器。多次调用是安全的。
//
// 顺序不能反。读循环会经由 machines.Manager 写库，若它跑在数据库关闭之后，
// PocketBase 内部会对着 nil 的 DB 解引用而 panic —— 生产上是关停时崩一下，
// 测试里则是 t.Cleanup 拆掉 TestApp 后必现的段错误。
func (h *Hub) Shutdown() {
	if h.ws != nil {
		h.ws.CloseAll()
		h.ws.Wait()
	}
	if h.machines != nil {
		h.machines.Stop()
	}
}

// Start 运行生产 hub（cobra 根命令，含 serve 子命令）。
func (h *Hub) Start() error {
	if h.pb == nil {
		return errors.New("hub: Start 只能用于 New 创建的实例；Attach 的实例由调用方驱动 serve")
	}
	return h.pb.Start()
}
