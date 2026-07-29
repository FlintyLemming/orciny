package probe_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/probe"
)

// fakeClaude 在 PATH 前面塞一个假的 claude 可执行文件。
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("本用例用 shell 脚本伪造可执行文件")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClaudeCodeVersionParsesOutput(t *testing.T) {
	fakeClaude(t, `echo "2.1.3 (Claude Code)"`)
	require.Equal(t, "2.1.3", probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionHandlesBareVersion(t *testing.T) {
	fakeClaude(t, `echo "1.0.10"`)
	require.Equal(t, "1.0.10", probe.ClaudeCodeVersion(context.Background()))
}

// 机器上没装 Claude Code 是合法状态（spec §7.4）——留空，不报错。
func TestClaudeCodeVersionEmptyWhenMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnNonZeroExit(t *testing.T) {
	fakeClaude(t, `exit 1`)
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnUnparseableOutput(t *testing.T) {
	fakeClaude(t, `echo "hello there"`)
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

// 卡住的 claude 不能把 agent 一起拖住。
// 脚本里用 exec 顶替 shell 自身：否则被杀掉的是 shell，sleep 变成孤儿
// 并继续攥着标准输出管道，Output() 要一直等到它自己退出为止。
func TestClaudeCodeVersionTimesOut(t *testing.T) {
	fakeClaude(t, `exec sleep 30`)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	require.Empty(t, probe.ClaudeCodeVersion(ctx))
	require.Less(t, time.Since(start), 5*time.Second, "超时后应立刻返回，不能等 claude 自己退出")
}

func TestCollectFillsMachineInfo(t *testing.T) {
	fakeClaude(t, `echo "2.1.3 (Claude Code)"`)

	info := probe.Collect(context.Background(), "0.1.0")
	require.NotEmpty(t, info.Hostname)
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, runtime.GOARCH, info.Arch)
	require.Equal(t, "0.1.0", info.AgentVersion)
	require.Equal(t, "2.1.3", info.ToolVersions["claude-code"])
}

func TestCollectOmitsToolVersionsWhenNothingFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	info := probe.Collect(context.Background(), "0.1.0")
	require.Empty(t, info.ToolVersions, "没探到就别塞空 map，wire 上少一个字段")
}
