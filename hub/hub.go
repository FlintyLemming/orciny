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
	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
	"github.com/FlintyLemming/orciny/hub/internal/importer"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
	"github.com/FlintyLemming/orciny/hub/internal/variables"
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
	vars     *variables.Store
	blobs    *blobs.Store
	sets     *configsets.Service
	revs     *revisions.Service
	provs    *providers.Store
	importer *importer.Service
	sync     *configsync.Service
	drift    *drift.Service

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

		// 主密钥。必须排在 ws 之前：一台解不开自己 key 的 hub 不该接客
		// ——它会把空值下发到全机队（M1.6 spec §2.6）。
		// 自检的对象从 credentials 表改成 providers 的三处密文字段。
		key, err := secretbox.LoadMasterKey(e.App.DataDir())
		if err != nil {
			return fmt.Errorf("加载主密钥: %w", err)
		}
		h.vars = variables.NewStore(e.App)

		h.blobs = blobs.New(e.App)
		h.provs = providers.NewStore(e.App, key, h.events)
		if err := h.provs.VerifyAll(); err != nil {
			return err
		}
		h.sets = configsets.NewService(e.App, h.blobs, h.events)
		h.revs = revisions.NewService(e.App, h.blobs, h.events)
		// 先建 importer（它要 Sender = h.machines），再建 configsync（它要 Importer）。
		// 两者互相需要，但依赖方向是单向的，不存在真正的循环。
		h.importer = importer.NewService(e.App, h.blobs, h.sets, h.provs, h.events, h.machines)
		h.sync = configsync.NewService(configsync.Deps{
			App: e.App, Blobs: h.blobs, Sets: h.sets, Revs: h.revs,
			Vars: h.vars, Providers: h.provs, Events: h.events, Sender: h.machines,
			Importer: h.importer, Logger: e.App.Logger(),
		})
		// drift 需要 configsync（发 DriftCommand），configsync 需要 drift（转交上报）：
		// 先建 configsync（Drift 留空），再建 drift，最后 SetDrift。
		h.drift = drift.NewService(drift.Deps{
			App: e.App, Blobs: h.blobs, Sets: h.sets, Revs: h.revs,
			Events: h.events, Sync: h.sync,
			Providers: h.provs, Logger: e.App.Logger(),
		})
		h.sync.SetDrift(h.drift)
		// 离线补发：agent 一上线就无条件通知一次，幂等保证它在无事可做时
		// 是零写入（spec §7.3）。
		h.machines.OnOnline(h.sync.OnMachineOnline)

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
			Agent:             h.sync,
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
			Admin:        h,
			Sets:         h.sets,
			Revs:         h.revs,
			Blobs:        h.blobs,
			Vars:         h.vars,
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
// 管理动作入口见 api.go；本方法历史最久，保留在这里。
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
