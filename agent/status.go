package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/state"
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

// printConfigStatus 把 state.json 里的配置闭环信息打到 out。
// state 不存在时说「尚未应用」，不报错——status 要在首次 enroll 后就能跑。
func printConfigStatus(out io.Writer, dir string) {
	st, err := state.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "配置状态   尚未应用任何配置")
			return
		}
		fmt.Fprintf(out, "配置状态   读取失败：%v\n", err)
		return
	}

	if st.Revision != "" {
		fmt.Fprintf(out, "配置集     %s\n", st.ConfigSet)
		fmt.Fprintf(out, "版本       %s", st.Revision)
		if st.Seq > 0 {
			fmt.Fprintf(out, "（v%d）", st.Seq)
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintln(out, "配置状态   尚未应用任何配置")
	}
	if st.Mode != "" {
		fmt.Fprintf(out, "模式       %s\n", st.Mode)
	}
	if st.Health != "" {
		fmt.Fprintf(out, "健康       %s\n", st.Health)
	}
	if st.Paused {
		fmt.Fprintln(out, "本机暂停   是（orciny-agent pause）")
	}

	// 漂移数：一次性本地扫描，不起监视、不联网。失败时静默跳过——
	// status 的主责是连接与版本，对账挂了不该让整条命令失败。
	if items, err := LocalDrift(dir); err == nil {
		fmt.Fprintf(out, "本机漂移   %d 项\n", len(items))
	}
}
