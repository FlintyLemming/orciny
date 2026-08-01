// Package state 读写 ~/.orciny/state.json（spec §7.8）。
//
// 单独成包而不是塞进 applier：applier 写它，watcher 读它当漂移基线，
// CLI 的 status / drift / pause 也要读。塞进 applier 会让 watcher
// 依赖 applier，而两者本无关系。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

const FileName = "state.json"

// health 的取值（spec §7.4）。degraded 表示回滚都失败过一次，
// 此后不再自动 apply 任何后续版本，直到人工解除。
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
)

// FileState 为每个受管路径同时记两个 hash（spec §6.3）。
type FileState struct {
	// Blob 是渲染**前**的内容 hash，等于 Revision 清单里的 hash。
	// 用途：判断本地 blob cache 是否命中、要不要向 hub 索取。
	Blob string `json:"blob"`
	// Rendered 是渲染**后**落盘内容的 hash。
	// 用途：漂移比对的唯一依据。轮换后重写文件并更新它，因此轮换不产生漂移。
	Rendered string   `json:"rendered"`
	Mode     uint32   `json:"mode"`
	Size     uint32   `json:"size,omitempty"`
	Keys     []string `json:"keys,omitempty"`
}

type State struct {
	ConfigSet string               `json:"config_set"`
	Revision  string               `json:"revision"`
	Seq       uint32               `json:"seq"`
	Checksum  string               `json:"checksum"`
	AppliedAt time.Time            `json:"applied_at"`
	Mode      string               `json:"mode"`
	Health    string               `json:"health"`
	Files     map[string]FileState `json:"files"`
	Ignored   []string             `json:"ignored"`
	Paused    bool                 `json:"paused"`
	// Manifest 随快照冻结（spec §4.1）。watcher 展开受管范围、恢复时定位
	// 基线都要用它——存 revision id 而不存 manifest 本身，agent 离线时
	// 就没法对账了。
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load 读回本机状态。
//
// **损坏与丢失同等对待**，都返回包装了 os.ErrNotExist 的错误：两种情形下
// agent 都不知道基线是什么，而 spec §7.6 的规矩是——不知道基线就绝不覆盖
// 用户的文件，一律上报状态丢失、由 hub 打回 survey 做全量对账。
func Load(dir string) (*State, error) {
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state: %s 损坏，按状态丢失处理: %w", FileName, os.ErrNotExist)
	}
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	return &s, nil
}

func Save(dir string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: 序列化: %w", err)
	}
	return atomicfile.Write(Path(dir), b, 0o600)
}
