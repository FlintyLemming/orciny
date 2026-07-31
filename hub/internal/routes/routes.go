// Package routes 是自定义 HTTP 路由的唯一注册点。
//
// 分工（spec §5.2）：前端读机器列表与事件流走 PocketBase 的 JS SDK 与
// realtime；前端触发动作走这里的 /api/orciny/*，业务规则集中在 Go 侧。
package routes

import (
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
	"github.com/FlintyLemming/orciny/hub/internal/ws"
)

// Deps 是路由层需要的全部依赖。路由不持有状态，只做编解码与状态码映射。
type Deps struct {
	Enroll   *enroll.Service
	Identity *identity.Store
	WS       *ws.Handler
	Version  string

	// DownloadBase 是 install.sh 拉取 agent 归档的主下载源。
	// 空值走 DefaultDownloadBase(Version)，即本 hub 版本对应的 github-dl.flinty.moe Worker；
	// GitHub 直连兜底由 install-agent.sh 自行追加。
	DownloadBase string
}

// Register 在 OnServe 阶段注册全部自定义路由。
func Register(e *core.ServeEvent, d Deps) error {
	g := e.Router.Group("/api/orciny")

	g.POST("/enroll-tokens", d.issueToken).Bind(apis.RequireSuperuserAuth())
	g.GET("/hub-info", d.hubInfo).Bind(apis.RequireSuperuserAuth())

	// enroll 不能要求 superuser：agent 手上只有一枚一次性 token，
	// token 本身就是凭证（spec §9.1）。
	g.POST("/enroll", d.enroll)

	// agent 的长连接入口。认证在 wire 上做（Ed25519 双向挑战-应答），
	// 因此这里不挂任何 HTTP 层的鉴权中间件。
	//
	// WS 为 nil 时干脆不注册：只关心 HTTP 路由的测试不必去装配一个 ws.Handler，
	// 也好过留一条一被访问就 panic 的路由。
	if d.WS != nil {
		g.GET("/ws", func(e *core.RequestEvent) error { return d.WS.Upgrade(e) })
	}

	// 安装脚本不在 /api 下：它要能被 `curl -fsSL https://<hub>/install.sh` 直接取到。
	// 无需认证——脚本不含秘密，token 由用户拼在命令行上（spec §9.1）。
	e.Router.GET("/install.sh", d.installSh)

	return nil
}

// baseURL 推断 hub 的对外地址，用于拼装安装命令。
// hub 假定部署在反向代理之后，因此优先信任 X-Forwarded-*。
func baseURL(e *core.RequestEvent) string {
	scheme := "http"
	if e.IsTLS() {
		scheme = "https"
	}
	if v := e.Request.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = strings.Split(v, ",")[0]
	}
	host := e.Request.Host
	if v := e.Request.Header.Get("X-Forwarded-Host"); v != "" {
		host = strings.Split(v, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}
