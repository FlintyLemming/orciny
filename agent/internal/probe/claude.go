package probe

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/FlintyLemming/orciny/protocol"
)

// ClaudeProbeTimeout 是探测的硬上限。卡住的 claude 不能拖住 agent。
const ClaudeProbeTimeout = 3 * time.Second

// ToolClaudeCode 是 tool_versions 里的键名。
const ToolClaudeCode = "claude-code"

var versionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b`)

// runClaudeVersion 执行 `claude --version`。测试可替换它，避免在
// 全量并行套件把机器打满时 fork 壳脚本被调度延迟拖过 3s 超时。
var runClaudeVersion = execClaudeVersion

func execClaudeVersion(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude", "--version")
	// 超时杀掉 claude 之后最多再等 1 秒：它派生的子进程可能还攥着标准输出，
	// 没有这个兜底，Output() 会一直等到那个孙子进程自己退出。
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// ClaudeCodeVersion 执行 `claude --version` 并解析版本号。
//
// 任何失败都返回空串而不是错误：机器上没装 Claude Code 是合法状态，
// 产品文档 §4.2 允许「仅观测」的机器接入（spec §7.4）。
func ClaudeCodeVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, ClaudeProbeTimeout)
	defer cancel()

	out, err := runClaudeVersion(ctx)
	if err != nil {
		return ""
	}
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	return m[1]
}

// Collect 汇总一次上报所需的全部信息。
//
// 这是 M0 里 agent 读取本机信息的**全部**范围：主机名、平台、
// 以及一次 `claude --version`。除此之外不碰用户的任何文件，
// 尤其不碰 ~/.claude（spec §1.2 / §7.4）。
func Collect(ctx context.Context, agentVersion string) protocol.MachineInfo {
	hostname, goos, goarch := Host()

	info := protocol.MachineInfo{
		Hostname:     hostname,
		OS:           goos,
		Arch:         goarch,
		AgentVersion: agentVersion,
	}
	if v := ClaudeCodeVersion(ctx); v != "" {
		info.ToolVersions = map[string]string{ToolClaudeCode: v}
	}
	return info
}
