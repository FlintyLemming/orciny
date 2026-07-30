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

// DefaultDownloadBase 返回 version 那一版 release 产物的位置。
//
// 钉在具体版本上，而不是用 releases/latest/download：脚本里的版本号是 hub
// 注入的**自己**的版本，两者必须来自同一个 tag。指向 latest 的话，一旦发了
// 新版而 hub 没跟着重新部署，脚本就会拿旧版本号去新 release 的目录里找，
// 必然 404（2026-07 踩过）。按版本定位则陈旧的 hub 也能装到与自己匹配的
// agent —— 版本兼容性另有 orciny.MinAgentVersion 兜着。
//
// 开发期用 --download-base 指向本地构建产物，避免每次都要发 release
// 才能测试安装路径（spec §11.4）。
func DefaultDownloadBase(version string) string {
	// tag 带 v 前缀，注入的版本号不带（goreleaser 的 {{.Version}} 已剥掉）。
	return "https://github.com/FlintyLemming/orciny/releases/download/v" + version
}

// InstallScript 返回注入了版本与下载地址的脚本文本。
func InstallScript(version, downloadBase string) string {
	if downloadBase == "" {
		downloadBase = DefaultDownloadBase(version)
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
