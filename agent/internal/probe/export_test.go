package probe

import "context"

// SetClaudeRunnerForTest 替换 `claude --version` 的执行器。返回恢复函数。
func SetClaudeRunnerForTest(fn func(context.Context) ([]byte, error)) (restore func()) {
	prev := runClaudeVersion
	runClaudeVersion = fn
	return func() { runClaudeVersion = prev }
}
