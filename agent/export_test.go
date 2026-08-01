package agent

import (
	"bytes"
)

// RunCLIForTest 供 agent_test 包调用的 CLI 入口（合并 stdout/stderr）。
//
// 放在 export_test.go：只在测试构建里存在，不进生产二进制。
func RunCLIForTest(dir string, args ...string) (string, error) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	full := append([]string{}, args...)
	full = append(full, "--dir", dir)
	root.SetArgs(full)
	err := root.Execute()
	return out.String(), err
}
