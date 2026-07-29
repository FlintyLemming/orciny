// Package agent 是 agent 侧唯一的对外入口。
// 实现细节藏在 agent/internal/*，hub 侧碰不到（spec §2.2）。
package agent

// Execute 运行 agent 的命令行。cmd/orciny-agent 只调这一个函数。
func Execute() error {
	return newRootCmd().Execute()
}
