package routes_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/routes"
)

func TestInstallScriptIsPubliclyServed(t *testing.T) {
	f := newFixture(t)

	// 脚本本身不含秘密，token 由用户拼在命令行上，因此不需要认证（spec §9.1）。
	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "#!/bin/sh")
	require.Contains(t, string(body), "orciny-agent")
}

func TestInstallScriptHasPlaceholdersFilled(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	s := string(body)
	require.NotContains(t, s, "__VERSION__", "版本占位符必须已注入")
	require.NotContains(t, s, "__DOWNLOAD_BASES__", "下载源占位符必须已注入")
	require.Contains(t, s, "0.1.0")
}

func TestInstallScriptPinsDownloadToOwnVersion(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	s := string(body)
	// hub 注入的是它**自己**的版本号，所以下载源也必须钉在同一个版本上。
	// 主源是 Cloudflare Worker（github-dl.flinty.moe），路径形如 /v0.1.0。
	require.Contains(t, s, "github-dl.flinty.moe/v0.1.0")
	require.NotContains(t, s, "releases/latest/download",
		"指向 latest 时，发了新版而 hub 未重新部署就会拿旧版本号去新 release 里找，必然 404")
}

func TestInstallScriptHasWorkerAndGitHubFallback(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	s := string(body)
	// Worker 主源在前，GitHub 直连兜底在后；任一源单点故障都不影响安装。
	require.Contains(t, s, "https://github-dl.flinty.moe/v0.1.0")
	require.Contains(t, s, "https://github.com/FlintyLemming/orciny/releases/download")
	// 兜底源也必须钉版本：脚本里 GitHub 直连那段不带版本号，靠和主源同一轮
	// 注入的 $VERSION 拼路径，所以这里只校验主源带版本即可（上一条用例已覆盖）。
}

// 手工 docker build 常直接传 git describe 的输出，形如 v0.3.0-34-g9ff2b3a：带 v
// 前缀（Makefile 会去掉），还挂着「tag 之后第几个提交」的后缀，没有哪个 release
// 叫这个名字。脚本必须回到它所基于的那个 tag 去下载，否则 Worker 主源与 GitHub
// 兜底一起 404（2026-09 线上踩过：地址被拼成了 /vv0.3.0-34-g9ff2b3a/）。
func TestInstallScriptDownloadsTagBehindGitDescribe(t *testing.T) {
	for _, ver := range []string{
		"v0.3.0-34-g9ff2b3a",
		"v0.3.0-34-g9ff2b3a-dirty",
		"v0.3.0-dirty",
		"v0.3.0",
		"0.3.0",
	} {
		t.Run(ver, func(t *testing.T) {
			s := routes.InstallScript(ver, "")
			require.Contains(t, s, `VERSION="0.3.0"`)
			require.Contains(t, s, `DOWNLOAD_BASES="https://github-dl.flinty.moe/v0.3.0"`)
		})
	}
}

// 预发布 tag（v0.4.0-rc1）本身就是一个 release，不能当成 describe 后缀剥掉——
// 剥成 0.4.0 反而指向一个还不存在的版本。
func TestInstallScriptKeepsPrereleaseTag(t *testing.T) {
	for ver, want := range map[string]string{
		"0.4.0-rc1":             "0.4.0-rc1",
		"v0.4.0-rc1-3-gabcdef0": "0.4.0-rc1",
	} {
		t.Run(ver, func(t *testing.T) {
			s := routes.InstallScript(ver, "")
			require.Contains(t, s, `VERSION="`+want+`"`)
			require.Contains(t, s, `DOWNLOAD_BASES="https://github-dl.flinty.moe/v`+want+`"`)
		})
	}
}

func TestInstallScriptServedAsShellText(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, resp.Header.Get("Content-Type"), "text/plain",
		"当成 HTML 送会被浏览器渲染，也可能被中间设备改写")
}
