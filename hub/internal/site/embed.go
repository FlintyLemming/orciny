//go:build !dev

// Package site 承载编译后的 Web UI。
// 生产用 //go:embed 内嵌 dist/；dist/index.html 是仓库里的占位文件，
// 前端构建会覆盖它（spec §5.4）。
package site

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distDir embed.FS

// DistFS 返回以 dist/ 为根的只读文件系统。
func DistFS() fs.FS {
	sub, err := fs.Sub(distDir, "dist")
	if err != nil {
		// 只可能在 dist/ 目录缺失时发生，属于构建期错误。
		panic("site: 内嵌 dist 目录缺失: " + err.Error())
	}
	return sub
}
