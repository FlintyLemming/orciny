package overrides

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
	"github.com/FlintyLemming/orciny/protocol"
)

// Apply 把一台机器的覆盖层盖到 head 的文件清单上（spec §4.3）。
//
// 输入的 files 处在**占位符空间**（{{provider.*}} / {{var.*}} 原样未渲染），
// 覆盖层里的内容也是——渲染发生在 agent 侧、合并之后，因此覆盖层永远
// 不会把 API key 明文带进 hub 库。
//
// 撞车一律取本机（spec §4.5）：unmergeable 与 path_gone 无从取，
// 等于放弃本轮合并、下发原基线。apply 永远不会因为撞车卡住。
func (s *Service) Apply(
	machineID, revID string, files []protocol.FileEntry,
) ([]protocol.FileEntry, error) {
	recs, err := s.ForMachine(machineID)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return files, nil
	}

	byPath := map[string][]*core.Record{}
	for _, r := range recs {
		p := r.GetString("path")
		byPath[p] = append(byPath[p], r)
	}

	out := make([]protocol.FileEntry, len(files))
	copy(out, files)
	seen := map[string]bool{}

	for i, entry := range out {
		group := byPath[entry.Path]
		if len(group) == 0 {
			continue
		}
		seen[entry.Path] = true

		base, err := s.blobs.Get(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("overrides: 取 %s 的基线内容: %w", entry.Path, err)
		}
		merged, err := s.mergeOne(base, group, revID)
		if err != nil {
			return nil, err
		}
		if string(merged) == string(base) {
			continue // 内容没变就不必重新 Put
		}
		h, err := s.blobs.Put(merged)
		if err != nil {
			return nil, fmt.Errorf("overrides: 写合并结果 %s: %w", entry.Path, err)
		}
		// Mode / Keys 不动：覆盖层只管内容。
		out[i].Hash = h
		out[i].Size = uint32(len(merged))
	}

	// 覆盖层指向的路径不在 files 里 → path_gone，其余照常。
	for path, group := range byPath {
		if seen[path] {
			continue
		}
		for _, r := range group {
			if err := s.flag(r, "path_gone", "", "", ""); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// mergeOne 合并一个路径上的一组覆盖层。同一路径上 kind 恒一致
// （kind 由文件内容决定，建覆盖层时已用「新的替换旧的」保证）。
func (s *Service) mergeOne(base []byte, group []*core.Record, revID string) ([]byte, error) {
	if group[0].GetString("kind") == "text" {
		return s.mergeText(base, group[0], revID)
	}
	return s.mergeJSON(base, group, revID)
}

// mergeJSON 逐点覆盖。撞车检测比的是**原始 base**，不是逐步改出来的中间态
// ——selector 互不重叠（spec §8.5），因此这样读是对的。
func (s *Service) mergeJSON(base []byte, group []*core.Record, revID string) ([]byte, error) {
	if !IsJSONObject(base) {
		for _, r := range group {
			if err := s.flag(r, "unmergeable", "", "", ""); err != nil {
				return nil, err
			}
		}
		return base, nil // 放弃本轮合并，下发原基线
	}

	out := base
	for _, r := range group {
		sel := r.GetString("selector")

		// —— 撞车检测：中台是不是也动了同一处？——
		cur := gjson.GetBytes(base, sel)
		curRaw := ""
		if cur.Exists() {
			curRaw = cur.Raw
		}
		if sameRawJSON(curRaw, r.GetString("base_value")) {
			if err := s.flag(r, "", "", "", ""); err != nil {
				return nil, err
			}
		} else if err := s.flag(r, "hub_changed", curRaw, "", revID); err != nil {
			return nil, err
		}

		// —— 取本机 ——
		mine := r.GetString("mine_value")
		var err error
		if mine == "" {
			out, err = sjson.DeleteBytes(out, sel) // 空串 = 本机没有这个键
		} else {
			out, err = sjson.SetRawBytes(out, sel, []byte(mine))
		}
		if err != nil {
			return nil, fmt.Errorf("overrides: 在 %s 上写 %s: %w",
				r.GetString("path"), sel, err)
		}
	}
	return out, nil
}

// mergeText 走行级三方合并。冲突取本机，标 merge_conflict，
// 并把中台的新全文存进 shadowed_blob 供收件箱做三方对比。
func (s *Service) mergeText(theirs []byte, r *core.Record, revID string) ([]byte, error) {
	base, err := s.blobBy(r.GetString("base_blob"))
	if err != nil {
		return nil, err
	}
	mine, err := s.blobBy(r.GetString("mine_blob"))
	if err != nil {
		return nil, err
	}

	lines, conflicts := merge3.Merge(
		merge3.SplitLines(base), merge3.SplitLines(mine), merge3.SplitLines(theirs))
	out := merge3.Join(lines)

	if len(conflicts) == 0 {
		if err := s.flag(r, "", "", "", ""); err != nil {
			return nil, err
		}
		return out, nil
	}
	shadow, err := s.putBlob(theirs)
	if err != nil {
		return nil, err
	}
	if err := s.flag(r, "merge_conflict", "", shadow, revID); err != nil {
		return nil, err
	}
	return out, nil
}

// flag 写 attention 与 shadowed_*，**只在与库里已存的值不同时才 Save**。
//
// Snapshot() 每次连接、每次通知都会调，无条件写会造成每次拉取一次 DB 写
// （spec §4.5）。
func (s *Service) flag(r *core.Record, attention, value, blobID, revID string) error {
	if attention == "" {
		value, blobID, revID = "", "", ""
	}
	if r.GetString("attention") == attention &&
		r.GetString("shadowed_value") == value &&
		r.GetString("shadowed_blob") == blobID &&
		r.GetString("shadowed_rev") == revID {
		return nil
	}
	r.Set("attention", attention)
	r.Set("shadowed_value", value)
	r.Set("shadowed_blob", blobID)
	r.Set("shadowed_rev", revID)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("overrides: 更新 %s 的提醒状态: %w", r.Id, err)
	}
	return nil
}

// sameRawJSON 用规范化形式比较，让重新缩进不误报撞车。
// 任一侧不是合法 JSON 时退回字节比较（空串对空串也走这条）。
func sameRawJSON(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	ca, errA := CanonJSON([]byte(a))
	cb, errB := CanonJSON([]byte(b))
	if errA != nil || errB != nil {
		return false
	}
	return ca == cb
}
