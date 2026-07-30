package routes_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.NotContains(t, s, "__DOWNLOAD_BASE__", "下载地址占位符必须已注入")
	require.Contains(t, s, "0.1.0")
}

func TestInstallScriptServedAsShellText(t *testing.T) {
	f := newFixture(t)

	resp, err := http.Get(f.srv.URL + "/install.sh")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, resp.Header.Get("Content-Type"), "text/plain",
		"当成 HTML 送会被浏览器渲染，也可能被中间设备改写")
}
