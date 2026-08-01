package probe_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/probe"
)

func TestClaudeCodeVersionParsesOutput(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return []byte("2.1.3 (Claude Code)\n"), nil
	})
	defer restore()
	require.Equal(t, "2.1.3", probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionHandlesBareVersion(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return []byte("1.0.10\n"), nil
	})
	defer restore()
	require.Equal(t, "1.0.10", probe.ClaudeCodeVersion(context.Background()))
}

// 机器上没装 Claude Code 是合法状态（spec §7.4）——留空，不报错。
func TestClaudeCodeVersionEmptyWhenMissing(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return nil, errors.New("executable file not found in $PATH")
	})
	defer restore()
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnNonZeroExit(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return nil, errors.New("exit status 1")
	})
	defer restore()
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnUnparseableOutput(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return []byte("hello there\n"), nil
	})
	defer restore()
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

// 卡住的 claude 不能把 agent 一起拖住。
// 这一条仍走真 fork：要验证 CommandContext + WaitDelay 的超时路径。
func TestClaudeCodeVersionTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("本用例用 shell 脚本伪造可执行文件")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	// exec 顶替 shell 自身：否则被杀掉的是 shell，sleep 变成孤儿
	// 并继续攥着标准输出管道，Output() 会一直等到它自己退出为止。
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// 不替换 runner——走默认的 execClaudeVersion，真的找 PATH 里的 claude。

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	require.Empty(t, probe.ClaudeCodeVersion(ctx))
	require.Less(t, time.Since(start), 5*time.Second, "超时后应立刻返回，不能等 claude 自己退出")
}

func TestCollectFillsMachineInfo(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return []byte("2.1.3 (Claude Code)\n"), nil
	})
	defer restore()

	info := probe.Collect(context.Background(), "0.1.0")
	require.NotEmpty(t, info.Hostname)
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, runtime.GOARCH, info.Arch)
	require.Equal(t, "0.1.0", info.AgentVersion)
	require.Equal(t, "2.1.3", info.ToolVersions["claude-code"])
}

func TestCollectOmitsToolVersionsWhenNothingFound(t *testing.T) {
	restore := probe.SetClaudeRunnerForTest(func(context.Context) ([]byte, error) {
		return nil, errors.New("not found")
	})
	defer restore()

	info := probe.Collect(context.Background(), "0.1.0")
	require.Empty(t, info.ToolVersions, "没探到就别塞空 map，wire 上少一个字段")
}
