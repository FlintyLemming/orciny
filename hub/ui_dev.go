//go:build dev

package hub

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"
)

// registerUI 在 dev 构建下把前端请求反代给 Vite。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	proxy := site.DevProxy()
	e.Router.GET("/{path...}", func(re *core.RequestEvent) error {
		proxy.ServeHTTP(re.Response, re.Request)
		return nil
	})
	e.App.Logger().Info("dev 模式：前端请求反代至 " + site.DevTarget)
	return nil
}
