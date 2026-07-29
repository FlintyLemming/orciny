// Package probe 探测本机信息。
//
// M0 的边界（spec §7.4）：除了执行 `claude --version`（计划 7 加入），
// agent 不读取用户的任何文件，尤其不碰 ~/.claude。
package probe

import (
	"os"
	"runtime"
)

// Host 返回主机名与平台。主机名取不到时返回 "unknown"——
// 这不值得让 enroll 失败，用户随后可以在 UI 里改备注名。
func Host() (hostname, goos, goarch string) {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "unknown"
	}
	return name, runtime.GOOS, runtime.GOARCH
}
