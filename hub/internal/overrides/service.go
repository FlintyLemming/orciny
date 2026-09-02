package overrides

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/events"
)

// Service 管 machine_overrides 的读写与合并。
type Service struct {
	app    core.App
	blobs  *blobs.Store
	events *events.Writer
	log    *slog.Logger
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer, log *slog.Logger) *Service {
	if log == nil {
		if app != nil {
			log = app.Logger()
		} else {
			log = slog.Default()
		}
	}
	return &Service{app: app, blobs: b, events: ev, log: log}
}

// ForMachine 返回一台机器的全部覆盖层，按 path + selector 排序。
func (s *Service) ForMachine(machineID string) ([]*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("machine_overrides",
		"machine = {:m}", "path,selector", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("overrides: 查询 %s 的覆盖层: %w", machineID, err)
	}
	return recs, nil
}

// ForPath 返回一台机器某个路径上的全部覆盖层。
func (s *Service) ForPath(machineID, path string) ([]*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("machine_overrides",
		"machine = {:m} && path = {:p}", "selector", 0, 0,
		map[string]any{"m": machineID, "p": path})
	if err != nil {
		return nil, fmt.Errorf("overrides: 查询 %s 上 %s 的覆盖层: %w", machineID, path, err)
	}
	return recs, nil
}

// CreateJSON 为一批差异点各建一条 json_key 记录。
func (s *Service) CreateJSON(machineID, path, driftID string, points []Point) error {
	col, err := s.app.FindCollectionByNameOrId("machine_overrides")
	if err != nil {
		return fmt.Errorf("overrides: 找不到 collection: %w", err)
	}
	for _, pt := range points {
		r := core.NewRecord(col)
		r.Set("machine", machineID)
		r.Set("path", path)
		r.Set("kind", "json_key")
		r.Set("selector", pt.Selector)
		r.Set("base_value", pt.BaseValue)
		r.Set("mine_value", pt.MineValue)
		if driftID != "" {
			r.Set("origin_drift", driftID)
		}
		if err := s.app.Save(r); err != nil {
			return fmt.Errorf("overrides: 建 %s 上 %s 的覆盖层: %w", path, pt.Selector, err)
		}
	}
	return nil
}

// CreateText 建一条 text 记录：整份文件由 base / mine 两份全文表达。
// selector 恒为空串，因此一个路径上只能有一条 text 覆盖层（唯一索引保证）。
func (s *Service) CreateText(machineID, path, driftID string, base, mine []byte) error {
	col, err := s.app.FindCollectionByNameOrId("machine_overrides")
	if err != nil {
		return fmt.Errorf("overrides: 找不到 collection: %w", err)
	}
	baseID, err := s.putBlob(base)
	if err != nil {
		return err
	}
	mineID, err := s.putBlob(mine)
	if err != nil {
		return err
	}
	r := core.NewRecord(col)
	r.Set("machine", machineID)
	r.Set("path", path)
	r.Set("kind", "text")
	r.Set("selector", "")
	r.Set("base_blob", baseID)
	r.Set("mine_blob", mineID)
	if driftID != "" {
		r.Set("origin_drift", driftID)
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("overrides: 建 %s 的文本覆盖层: %w", path, err)
	}
	return nil
}

// DropPath 删掉一台机器某个路径上的全部覆盖层，返回删掉的条数。
//
// 两处用它：建 ignore 规则时（路径退管，覆盖层无处可盖，spec §2.3），
// 以及新旧 kind 不同时（用户把 JSON 文件改成了非 JSON，spec §8.5）。
func (s *Service) DropPath(machineID, path string) (int, error) {
	recs, err := s.ForPath(machineID, path)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		if err := s.app.Delete(r); err != nil {
			return 0, fmt.Errorf("overrides: 删 %s 的覆盖层: %w", path, err)
		}
	}
	return len(recs), nil
}

// DropOverlapping 删掉与 selector 构成前缀关系（任一方向）的既有记录。
//
// 两次分开的排除操作可能产出 env 与 env.ANTHROPIC_MODEL 这样的重叠，
// 而重叠会让合并结果依赖应用顺序——不可接受。规则是**新的替换旧的**：
// 用户最后点的那次就是他的意思（spec §8.5）。
func (s *Service) DropOverlapping(machineID, path, selector string) (int, error) {
	recs, err := s.ForPath(machineID, path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range recs {
		if !selectorsOverlap(r.GetString("selector"), selector) {
			continue
		}
		if err := s.app.Delete(r); err != nil {
			return 0, fmt.Errorf("overrides: 删被替换的覆盖层 %s: %w", r.Id, err)
		}
		n++
	}
	return n, nil
}

// selectorsOverlap 判断两个 selector 是否构成前缀关系。
// 按段比较而不是按字符：env.AB 与 env.AC 不重叠，env 与 env.A 重叠。
func selectorsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".")
}

// Delete 删一条覆盖层，返回它所属的机器 id（调用方据此发 ConfigNotify）。
func (s *Service) Delete(id string) (string, error) {
	r, err := s.app.FindRecordById("machine_overrides", id)
	if err != nil {
		return "", fmt.Errorf("overrides: 覆盖层 %s 不存在: %w", id, err)
	}
	machineID := r.GetString("machine")
	path := r.GetString("path")
	if err := s.app.Delete(r); err != nil {
		return "", fmt.Errorf("overrides: 删覆盖层 %s: %w", id, err)
	}
	if s.events != nil {
		if err := s.events.Write(events.KindOverrideDropped, machineID, map[string]any{
			"path": path, "selector": r.GetString("selector"),
		}); err != nil {
			s.log.Warn("写 override.dropped 事件失败", "error", err)
		}
	}
	return machineID, nil
}

// Keep 清掉 attention 与 shadowed_*：用户看过了，决定保持本机的说法。
func (s *Service) Keep(id string) (string, error) {
	r, err := s.app.FindRecordById("machine_overrides", id)
	if err != nil {
		return "", fmt.Errorf("overrides: 覆盖层 %s 不存在: %w", id, err)
	}
	r.Set("attention", "")
	r.Set("shadowed_value", "")
	r.Set("shadowed_blob", "")
	r.Set("shadowed_rev", "")
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("overrides: 清 %s 的提醒: %w", id, err)
	}
	machineID := r.GetString("machine")
	if s.events != nil {
		if err := s.events.Write(events.KindOverrideKept, machineID, map[string]any{
			"path": r.GetString("path"), "selector": r.GetString("selector"),
		}); err != nil {
			s.log.Warn("写 override.kept 事件失败", "error", err)
		}
	}
	return machineID, nil
}

// putBlob 写入内容并返回 blob 记录 id（RelationField 要 id 不要 hash）。
func (s *Service) putBlob(content []byte) (string, error) {
	hash, err := s.blobs.Put(content)
	if err != nil {
		return "", err
	}
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return "", fmt.Errorf("overrides: 写入后找不到 blob %s: %w", hash, err)
	}
	return r.Id, nil
}

// blobBy 按 blob 记录 id 取内容。id 为空时返回空切片。
func (s *Service) blobBy(id string) ([]byte, error) {
	if id == "" {
		return nil, nil
	}
	r, err := s.app.FindRecordById("blobs", id)
	if err != nil {
		return nil, fmt.Errorf("overrides: blob %s 不存在: %w", id, err)
	}
	return s.blobs.Get(r.GetString("hash"))
}
