// Package hub 是 hub 侧唯一的对外入口。
// 实现细节全部藏在 hub/internal/*，由 Go 的 internal 规则保证 agent 侧碰不到（spec §2.2）。
package hub

import (
	"errors"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"

	// 空 import 触发 init()，把初始迁移注册进 core.AppMigrations。
	// 这也让 internal/testsupport（只能看到公开包）间接获得迁移。
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

// Hub 嵌入 core.App，子系统作字段，业务全部挂在 OnServe 上（spec §5.1）。
type Hub struct {
	core.App

	cfg Config

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

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// 后续计划在此依次插入：hub 密钥加载、幽灵清理、自定义路由注册、
		// 连接管理器启动。顺序即依赖顺序，不要打乱。
		if err := h.registerUI(e); err != nil {
			return err
		}
		return e.Next()
	})

	return h, nil
}

// registerUI 挂内嵌前端。catch-all 模式在 net/http 的 ServeMux 里优先级最低，
// 因此不会盖住 /api/* 与 PocketBase 自带的 /_/*。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	e.Router.GET("/{path...}", apis.Static(site.DistFS(), true))
	return nil
}

// Start 运行生产 hub（cobra 根命令，含 serve 子命令）。
func (h *Hub) Start() error {
	if h.pb == nil {
		return errors.New("hub: Start 只能用于 New 创建的实例；Attach 的实例由调用方驱动 serve")
	}
	return h.pb.Start()
}
