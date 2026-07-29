package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// StatusFileName 是运行状态的落盘位置。
//
// 注意它**不是** spec §5.2 预留的 state.json —— 那个是 M1 的配置落盘状态，
// M0 不创建。这里只放「进程当前连着没有」，供 status 子命令读取。
const StatusFileName = "status.json"

// Status 是 agent 当前的运行状态。里面不含任何密钥材料。
type Status struct {
	State       string    `json:"state"`
	Since       time.Time `json:"since"`
	HubURL      string    `json:"hub_url"`
	Fingerprint string    `json:"fingerprint"`
	Version     string    `json:"version"`
	LastError   string    `json:"last_error,omitempty"`
}

func LoadStatus(dir string) (*Status, error) {
	b, err := os.ReadFile(filepath.Join(dir, StatusFileName))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist：调用方据此报「未运行」
	}
	var s Status
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", StatusFileName, err)
	}
	return &s, nil
}

func SaveStatus(dir string, s *Status) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化运行状态: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, StatusFileName), append(b, '\n'), 0o600)
}
