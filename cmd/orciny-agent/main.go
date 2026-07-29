// Command orciny-agent 是 Orciny agent 的可执行入口。
package main

import (
	"os"

	"github.com/FlintyLemming/orciny/agent"
)

func main() {
	if err := agent.Execute(); err != nil {
		os.Exit(1) // cobra 已把错误打到 stderr
	}
}
