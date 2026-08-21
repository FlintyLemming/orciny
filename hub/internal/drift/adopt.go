package drift

import (
	"errors"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

var (
	// ErrConflict：两台机器对同一文件的改动，静默取其一是最容易让人丢
	// 工作成果的操作。产品 §4.4 只要求「显式警告」，这里做成硬阻止
	// （取舍记录 #9）。
	ErrConflict = errors.New("drift: 多台机器改了同一路径，必须先做三方对比")

	// ErrMixedConfigSets：一个 Revision 只属于一个配置集。
	ErrMixedConfigSets = errors.New("drift: 选中的漂移不属于同一个配置集")

	// ErrNeedsReview：含未完全还原的变量，diff 里会显示 main vs
	// {{var.workspace}}，用户看得懂，但必须先看一眼（spec §6.4）。
	ErrNeedsReview = errors.New("drift: 含未完全还原的变量，需先人工确认")

	// ErrBindingDrift：绑定漂移不能收编（M1.5 spec §6.2）。
	//
	// 默认收编语义是「把机器现状写进配置集」，作用在绑定漂移上会把占位符
	// 拍平成硬编码字面值——绑定当场死掉，而且是静悄悄地死：下一次改
	// Provider 时这个配置集不再跟着走，没有任何提示。
	ErrBindingDrift = errors.New(
		"drift: 绑定漂移不能收编——收编会把占位符拍平成硬编码字面值，绑定会当场失效")
)

// Adopt 把选中的漂移合进一个新 Revision（spec §8.3）。
// 等价于 AdoptReviewed(ids, nil)。
func (s *Service) Adopt(eventIDs []string) (*core.Record, error) {
	return s.AdoptReviewed(eventIDs, nil)
}

// AdoptReviewed 把选中的漂移合进一个新 Revision（spec §8.3）。
//
// reviewed 是用户已逐条确认过的 event id；含 restore_partial 的条目必须
// 出现在里面。
func (s *Service) AdoptReviewed(eventIDs, reviewed []string) (*core.Record, error) {
	if len(eventIDs) == 0 {
		return nil, fmt.Errorf("drift: 没有选中任何漂移")
	}
	reviewedSet := map[string]bool{}
	for _, id := range reviewed {
		reviewedSet[id] = true
	}

	recs := make([]*core.Record, 0, len(eventIDs))
	setID := ""
	seenPath := map[string]string{} // path → machineID
	machines := map[string]bool{}

	for _, id := range eventIDs {
		rec, err := s.d.App.FindRecordById("drift_events", id)
		if err != nil {
			return nil, fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
		}
		if rec.GetString("state") != "open" {
			return nil, fmt.Errorf("drift: 漂移 %s 已被处理过", id)
		}
		if rec.GetBool("truncated") {
			return nil, fmt.Errorf("drift: %s 未能安全脱敏，无法收编；"+
				"请在 Web 上手工处理该文件", rec.GetString("path"))
		}
		// 必须在任何写入之前，与冲突检查同一批——要么整批成，要么什么都不动。
		if rec.GetBool("binding_drift") {
			return nil, fmt.Errorf("%w：%s。请改用卡片上的「改绑定」，"+
				"或者「恢复」把它拉回基线", ErrBindingDrift, rec.GetString("path"))
		}
		if rec.GetBool("restore_partial") && !reviewedSet[id] {
			return nil, fmt.Errorf("%w: %s", ErrNeedsReview, rec.GetString("path"))
		}

		cur := rec.GetString("config_set")
		if setID == "" {
			setID = cur
		} else if setID != cur {
			return nil, ErrMixedConfigSets
		}

		path := rec.GetString("path")
		machine := rec.GetString("machine")
		// 冲突检查在**任何写入之前**完成：要么整批成，要么什么都不动。
		if other, dup := seenPath[path]; dup && other != machine {
			return nil, fmt.Errorf("%w: %s", ErrConflict, path)
		}
		seenPath[path] = machine
		machines[machine] = true

		recs = append(recs, rec)
	}

	// 以 head Revision 为基，逐条覆盖。
	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return nil, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return nil, err
	}
	byPath := map[string]protocol.FileEntry{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	for _, rec := range recs {
		path := rec.GetString("path")
		switch rec.GetString("kind") {
		case "deleted":
			delete(byPath, path)
		default:
			blobRec, err := s.d.App.FindRecordById("blobs", rec.GetString("current_blob"))
			if err != nil {
				return nil, fmt.Errorf("drift: %s 的内容不在库里: %w", path, err)
			}
			hash := blobRec.GetString("hash")
			content, err := s.d.Blobs.Get(hash)
			if err != nil {
				return nil, err
			}
			mode := uint32(rec.GetInt("mode"))
			if mode == 0 {
				mode = 0o644
			}
			byPath[path] = protocol.FileEntry{
				Path: path, Hash: hash, Size: uint32(len(content)),
				Mode: mode, Keys: byPath[path].Keys,
			}
		}
	}

	merged := make([]protocol.FileEntry, 0, len(byPath))
	for _, f := range byPath {
		merged = append(merged, f)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Path < merged[j].Path })

	// 收编改的是文件，不是绑定：沿用 head 的绑定（M1.5 spec §2.2）。
	// 不沿用的话，收编一次就等于顺手解绑，且没有任何提示。
	var binding *providers.Binding
	if head, err := s.d.Revs.Head(setID); err == nil {
		binding, err = s.d.Revs.BindingOf(head.Id)
		if err != nil {
			return nil, err
		}
	}

	note := adoptNote(s.d.App, machines, len(recs))
	rev, err := s.d.Revs.PublishFiles(setID, merged, binding, note, "adopt")
	if err != nil {
		return nil, err
	}

	now := types.NowDateTime()
	for _, rec := range recs {
		rec.Set("state", "adopted")
		rec.Set("resolved_revision", rev.Id)
		rec.Set("resolved_at", now)
		if err := s.d.App.Save(rec); err != nil {
			s.log.Warn("标记漂移为已收编失败", "id", rec.Id, "error", err)
		}
		if err := s.d.Events.Write(events.KindDriftAdopted, rec.GetString("machine"),
			map[string]any{"path": rec.GetString("path"), "revision": rev.Id}); err != nil {
			s.log.Warn("写 drift.adopted 事件失败", "error", err)
		}
	}

	// 通知**所有**指派机器，包括来源机器：它 apply 后 rendered hash 与磁盘
	// 一致，plan 全是 skip，天然幂等。不开特例就少一条会腐烂的分支
	// （取舍记录 #10）。
	if s.d.Sync != nil {
		if err := s.d.Sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonAdopted); err != nil {
			s.log.Warn("收编后通知失败", "config_set", setID, "error", err)
		}
	}
	return rev, nil
}

func adoptNote(app core.App, machines map[string]bool, n int) string {
	names := make([]string, 0, len(machines))
	for id := range machines {
		name := id
		if r, err := app.FindRecordById("machines", id); err == nil {
			if v := r.GetString("name"); v != "" {
				name = v
			} else if v := r.GetString("hostname"); v != "" {
				name = v
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 1 {
		return fmt.Sprintf("收编自 %s 的 %d 项改动", names[0], n)
	}
	return fmt.Sprintf("收编自 %d 台机器的 %d 项改动", len(names), n)
}
