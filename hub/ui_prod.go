//go:build !dev

package hub

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"
)

// registerUI 挂内嵌前端。catch-all 在 net/http 的 ServeMux 里优先级最低，
// 不会盖住 /api/* 与 PocketBase 自带的 /_/*。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	e.Router.GET("/{path...}", apis.Static(site.DistFS(), true))
	return nil
}
