package routes

import (
	_ "embed"
	"net/http"
	"regexp"
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

// dlFlintyMoe 是 Cloudflare Worker 反代 + 边缘缓存的下载源。
// Worker 原样转发 /v<version>/<file> 到 GitHub release，把内容缓存到边缘，
// 客户端全程只连 github-dl.flinty.moe，不再直连 GitHub -- 中国大陆下载不再卡。
// 部署细节见 supplemental/cloudflare/dl-worker.js 与 docs/operations.md。
const dlFlintyMoe = "https://github-dl.flinty.moe"

// DefaultDownloadBase 返回 version 那一版 release 产物的**主**下载源。
//
// 钉在具体版本上，而不是用 releases/latest/download：脚本里的版本号是 hub
// 注入的**自己**的版本，两者必须来自同一个 tag。指向 latest 的话，一旦发了
// 新版而 hub 没跟着重新部署，脚本就会拿旧版本号去新 release 的目录里找，
// 必然 404（2026-07 踩过）。按版本定位则陈旧的 hub 也能装到与自己匹配的
// agent -- 版本兼容性另有 orciny.MinAgentVersion 兜着。
//
// 主源是 Worker（github-dl.flinty.moe）；GitHub 直连兜底由 install-agent.sh 用
// $VERSION 自行追加（见脚本注释），这样主源与兜底一定来自同一 tag，--download-base
// 覆盖主源时也不会失配。任一源单点故障都不影响安装。
//
// 开发期用 --download-base 指向本地构建产物，避免每次都要发 release
// 才能测试安装路径（spec §11.4）。
func DefaultDownloadBase(version string) string {
	// tag 带 v 前缀，注入的版本号不带（goreleaser 的 {{.Version}} 已剥掉）。
	return dlFlintyMoe + "/v" + version
}

// describeSuffix 是 git describe 挂在 tag 后面的部分：
// -<tag 之后的提交数>-g<短哈希>，以及 --dirty 追加的 -dirty。
var describeSuffix = regexp.MustCompile(`(-[0-9]+-g[0-9a-f]+)?(-dirty)?$`)

// releaseVersion 把构建版本号还原成它所基于的 release 版本号（不带 v）。
//
// 发版流水线注入的是去掉 v 前缀的 tag（0.3.0），原样可用；Makefile 注入的是
// git describe 的输出（v0.3.0-34-g9ff2b3a），没有哪个 release 叫这个名字。
// configsync 的版本门槛也认这种形态，但那边是丢掉整个 pre-release 再比大小；
// 这里不能照搬——v0.4.0-rc1 本身就是一个 release，剥成 0.4.0 反而指向还
// 不存在的版本。所以只剥 describe 自己加的后缀。
func releaseVersion(version string) string {
	return describeSuffix.ReplaceAllString(strings.TrimPrefix(version, "v"), "")
}

// InstallScript 返回注入了版本与下载源的脚本文本。
//
// version 是 hub 的构建版本号，注入前先经 releaseVersion 还原成 release 版本：
// 脚本拿它拼归档名与下载路径，只认真实存在的 release。
//
// downloadBase 为空时走 DefaultDownloadBase(version)，即 Worker 主源。
// downloadBase 用于开发期指向本地构建产物；GitHub 直连兜底始终由脚本侧
// 按 $VERSION 追加，不在此处注入。
func InstallScript(version, downloadBase string) string {
	version = releaseVersion(version)
	if downloadBase == "" {
		downloadBase = DefaultDownloadBase(version)
	}
	r := strings.NewReplacer(
		"__VERSION__", version,
		"__DOWNLOAD_BASES__", downloadBase,
	)
	return r.Replace(installScript)
}

func (d Deps) installSh(e *core.RequestEvent) error {
	// text/plain 而不是 text/x-shellscript：前者到处都能正确透传，
	// 后者在某些代理上会触发下载对话框。
	return e.String(http.StatusOK, InstallScript(d.Version, d.DownloadBase))
}
