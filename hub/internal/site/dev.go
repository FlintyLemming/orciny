//go:build dev

package site

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// DevTarget 是 Vite dev server 的地址。
const DevTarget = "http://127.0.0.1:5173"

// DistFS 在 dev 构建下不可用——请用 DevProxy。
// 保留同名函数是为了让 hub 侧的调用点不必分叉。
func DistFS() fs.FS { return nil }

// DevProxy 把非 /api/* 请求反代给 Vite，从而支持 HMR。
//
// 前端 embed 在生产是对的，在开发是灾难——改一行 CSS 要重编译 Go
// （spec §5.4）。用 build tag 分离两种形态，生产构建不含这段代码。
func DevProxy() http.Handler {
	target, err := url.Parse(DevTarget)
	if err != nil {
		panic("site: dev 目标地址不合法: " + err.Error())
	}
	return httputil.NewSingleHostReverseProxy(target)
}
