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
)

// Deps 是路由层需要的全部依赖。路由不持有状态，只做编解码与状态码映射。
type Deps struct {
	Enroll   *enroll.Service
	Identity *identity.Store
	Version  string
}

// Register 在 OnServe 阶段注册全部自定义路由。
func Register(e *core.ServeEvent, d Deps) error {
	g := e.Router.Group("/api/orciny")

	g.POST("/enroll-tokens", d.issueToken).Bind(apis.RequireSuperuserAuth())
	g.GET("/hub-info", d.hubInfo).Bind(apis.RequireSuperuserAuth())

	// enroll 不能要求 superuser：agent 手上只有一枚一次性 token，
	// token 本身就是凭证（spec §9.1）。
	g.POST("/enroll", d.enroll)

	// WS 端点与 /install.sh 分别由计划 5 与计划 9 在此追加。
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
