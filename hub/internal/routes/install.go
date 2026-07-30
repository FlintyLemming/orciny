package routes

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// installScript 是内嵌的安装脚本源文本。
//
// 内嵌而不是运行时读文件：hub 是单二进制部署，多一个必须随行的文件
// 就是多一种「装完发现少了东西」的故障。
//
//go:embed install-agent.sh
var installScript string

// DefaultDownloadBase 是 release 产物的默认位置。
// 开发期用 --download-base 指向本地构建产物，避免每次都要发 release
// 才能测试安装路径（spec §11.4）。
const DefaultDownloadBase = "https://github.com/FlintyLemming/orciny/releases/latest/download"

// InstallScript 返回注入了版本与下载地址的脚本文本。
func InstallScript(version, downloadBase string) string {
	if downloadBase == "" {
		downloadBase = DefaultDownloadBase
	}
	r := strings.NewReplacer(
		"__VERSION__", version,
		"__DOWNLOAD_BASE__", downloadBase,
	)
	return r.Replace(installScript)
}

func (d Deps) installSh(e *core.RequestEvent) error {
	// text/plain 而不是 text/x-shellscript：前者到处都能正确透传，
	// 后者在某些代理上会触发下载对话框。
	return e.String(http.StatusOK, InstallScript(d.Version, d.DownloadBase))
}
