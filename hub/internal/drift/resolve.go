package drift

import (
	"errors"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// Restore 按机器分组下发恢复指令。状态要等 agent 的 ApplyAck 才变
// ——指令发出不等于恢复成功（spec §8.4）。
func (s *Service) Restore(eventIDs []string) error {
	if len(eventIDs) == 0 {
		return fmt.Errorf("drift: 没有选中任何漂移")
	}
	byMachine, err := s.groupOpenPaths(eventIDs)
	if err != nil {
		return err
	}
	if s.d.Sync == nil {
		return fmt.Errorf("drift: 同步服务未装配，无法下发恢复指令")
	}

	s.mu.Lock()
	if s.pendingRestore == nil {
		s.pendingRestore = map[string]map[string]bool{}
	}
	for machineID, paths := range byMachine {
		if s.pendingRestore[machineID] == nil {
			s.pendingRestore[machineID] = map[string]bool{}
		}
		for _, p := range paths {
			s.pendingRestore[machineID][p] = true
		}
	}
	s.mu.Unlock()

	for machineID, paths := range byMachine {
		cmd := protocol.DriftCommand{Op: protocol.OpRestore, Paths: paths}
		if err := s.d.Sync.SendTo(machineID, protocol.KindDriftCommand, cmd); err != nil {
			s.log.Warn("下发恢复指令失败", "machine", machineID, "error", err)
			return fmt.Errorf("drift: 向 %s 下发恢复指令失败: %w", machineID, err)
		}
	}
	return nil
}

// MarkRestored 在 agent 成功回执后关闭对应的 open 漂移。
//
// 只认「刚下过 restore 指令」的路径（pendingRestore）：普通 apply 的回执
// 不会误把被覆盖的漂移标成 restored。调用后消费掉对应 pending 条目。
func (s *Service) MarkRestored(machineID string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	s.mu.Lock()
	pending := s.pendingRestore[machineID]
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		if pending != nil && pending[p] {
			want[p] = true
			delete(pending, p)
		}
	}
	if pending != nil && len(pending) == 0 {
		delete(s.pendingRestore, machineID)
	}
	s.mu.Unlock()

	if len(want) == 0 {
		return nil
	}

	now := types.NowDateTime()
	for path := range want {
		recs, err := s.d.App.FindRecordsByFilter("drift_events",
			"machine = {:m} && path = {:p} && state = 'open'", "", 1, 0,
			map[string]any{"m": machineID, "p": path})
		if err != nil {
			return fmt.Errorf("drift: 查询待恢复漂移: %w", err)
		}
		if len(recs) == 0 {
			continue
		}
		rec := recs[0]
		rec.Set("state", "restored")
		rec.Set("resolved_at", now)
		if err := s.d.App.Save(rec); err != nil {
			return fmt.Errorf("drift: 标记 %s 已恢复: %w", path, err)
		}
		if err := s.d.Events.Write(events.KindDriftRestored, machineID, map[string]any{
			"path": path,
		}); err != nil {
			s.log.Warn("写 drift.restored 事件失败", "error", err)
		}
	}
	return nil
}

// Ignore 写 ignore_rules、置 ignored，并立即给 agent 发 OpIgnore
// ——不必等下一份快照才生效（spec §8.4）。
func (s *Service) Ignore(eventIDs []string, global bool) error {
	if len(eventIDs) == 0 {
		return fmt.Errorf("drift: 没有选中任何漂移")
	}

	col, err := s.d.App.FindCollectionByNameOrId("ignore_rules")
	if err != nil {
		return fmt.Errorf("drift: 找不到 ignore_rules: %w", err)
	}

	byMachine := map[string][]string{}
	now := types.NowDateTime()

	for _, id := range eventIDs {
		rec, err := s.d.App.FindRecordById("drift_events", id)
		if err != nil {
			return fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
		}
		if rec.GetString("state") != "open" {
			return fmt.Errorf("drift: 漂移 %s 已被处理过", id)
		}

		path := rec.GetString("path")
		machineID := rec.GetString("machine")

		// 退管与覆盖层互斥：退管意味着中台根本不下发它，覆盖层无处可盖。
		// 用户的意图很明确（整个路径都不要了），拦住他没有意义（spec §2.3）。
		if s.d.Overrides != nil {
			n, err := s.d.Overrides.DropPath(machineID, path)
			if err != nil {
				return err
			}
			if n > 0 {
				if err := s.d.Events.Write(events.KindOverrideDropped, machineID,
					map[string]any{"path": path, "reason": "path_unmanaged", "count": n}); err != nil {
					s.log.Warn("写 override.dropped 事件失败", "error", err)
				}
			}
		}

		rule := core.NewRecord(col)
		if !global {
			rule.Set("machine", machineID)
		}
		rule.Set("path", path)
		if err := s.d.App.Save(rule); err != nil {
			return fmt.Errorf("drift: 写忽略规则 %s: %w", path, err)
		}

		rec.Set("state", "ignored")
		rec.Set("resolved_at", now)
		if err := s.d.App.Save(rec); err != nil {
			return fmt.Errorf("drift: 标记 %s 已忽略: %w", path, err)
		}
		if err := s.d.Events.Write(events.KindDriftIgnored, machineID, map[string]any{
			"path": path, "global": global,
		}); err != nil {
			s.log.Warn("写 drift.ignored 事件失败", "error", err)
		}

		// 全局规则也要通知来源机器立刻生效；其他机器等下次快照带上 IgnorePaths。
		byMachine[machineID] = append(byMachine[machineID], path)
	}

	if s.d.Sync == nil {
		return nil
	}
	for machineID, paths := range byMachine {
		// 稳定顺序，便于测试断言
		sort.Strings(paths)
		cmd := protocol.DriftCommand{Op: protocol.OpIgnore, Paths: paths}
		if err := s.d.Sync.SendTo(machineID, protocol.KindDriftCommand, cmd); err != nil {
			s.log.Warn("下发忽略指令失败", "machine", machineID, "error", err)
		}
	}
	return nil
}

// Supersede 把指定路径上仍 open 的漂移置为 superseded（spec §7.7）。
// apply 撞上未解决漂移时照常覆盖，但绝不静默丢数据——current_blob 留着，
// 用户还能在「已被覆盖」筛选里看 diff、重新收编。
func (s *Service) Supersede(machineID string, paths []string, revID string) error {
	if len(paths) == 0 {
		return nil
	}
	now := types.NowDateTime()
	for _, path := range paths {
		recs, err := s.d.App.FindRecordsByFilter("drift_events",
			"machine = {:m} && path = {:p} && state = 'open'", "", 1, 0,
			map[string]any{"m": machineID, "p": path})
		if err != nil {
			return fmt.Errorf("drift: 查询 open 漂移: %w", err)
		}
		if len(recs) == 0 {
			continue
		}
		rec := recs[0]
		rec.Set("state", "superseded")
		rec.Set("resolved_revision", revID)
		rec.Set("resolved_at", now)
		if err := s.d.App.Save(rec); err != nil {
			return fmt.Errorf("drift: 标记 %s 被覆盖: %w", path, err)
		}
		if err := s.d.Events.Write(events.KindDriftSuperseded, machineID, map[string]any{
			"path": path, "revision": revID,
		}); err != nil {
			s.log.Warn("写 drift.superseded 事件失败", "error", err)
		}
	}
	return nil
}

// groupOpenPaths 校验 eventIDs 均为 open，并按机器聚合路径。
func (s *Service) groupOpenPaths(eventIDs []string) (map[string][]string, error) {
	byMachine := map[string][]string{}
	seen := map[string]bool{} // machine\x00path 去重
	for _, id := range eventIDs {
		rec, err := s.d.App.FindRecordById("drift_events", id)
		if err != nil {
			return nil, fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
		}
		if rec.GetString("state") != "open" {
			return nil, fmt.Errorf("drift: 漂移 %s 已被处理过", id)
		}
		machineID := rec.GetString("machine")
		path := rec.GetString("path")
		key := machineID + "\x00" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		byMachine[machineID] = append(byMachine[machineID], path)
	}
	for m := range byMachine {
		sort.Strings(byMachine[m])
	}
	return byMachine, nil
}

// ErrGlobalRule：全局忽略规则不由单机的「恢复受管」删除，
// 那会悄悄改掉全机队的行为。
var ErrGlobalRule = errors.New(
	"drift: 这是一条全局规则，请到设置里解除，或改为只对这台机器建例外")

// Remanage 恢复受管（spec §3.1）：**原子地**置 survey 并删掉该机器
// 在这个路径上的 ignore 规则。
//
// 顺序不能反，survey 也不是可选项。若直接恢复受管，下一次 apply 会当场
// 用中台版本盖掉他本机的改动——那份改动此后只剩在 drift_events.current_blob
// 里，而他刚打开这个页面的**目的**就是把它捞回来。
func (s *Service) Remanage(machineID, path string) error {
	rules, err := s.d.App.FindRecordsByFilter("ignore_rules",
		"path = {:p} && (machine = '' || machine = {:m})", "", 0, 0,
		map[string]any{"p": path, "m": machineID})
	if err != nil {
		return fmt.Errorf("drift: 查询忽略规则: %w", err)
	}
	own := make([]*core.Record, 0, len(rules))
	for _, r := range rules {
		if r.GetString("machine") == "" {
			return fmt.Errorf("%w：%s", ErrGlobalRule, path)
		}
		own = append(own, r)
	}
	if len(own) == 0 {
		return nil // 本来就受管，幂等返回
	}

	err = s.d.App.RunInTransaction(func(tx core.App) error {
		assign, err := s.d.Sets.Assignment(machineID)
		if err != nil {
			return err
		}
		// 先置 survey：让差异先进收件箱，别让下一次 apply 抹掉本机改动。
		assign.Set("mode", configsets.ModeSurvey)
		assign.Set("state", configsets.StatePending)
		if err := tx.Save(assign); err != nil {
			return fmt.Errorf("drift: 打回 survey: %w", err)
		}
		// 再删规则。
		for _, r := range own {
			if err := tx.Delete(r); err != nil {
				return fmt.Errorf("drift: 删忽略规则 %s: %w", path, err)
			}
		}
		return s.d.Events.WriteTx(tx, events.KindAssignChanged, machineID, map[string]any{
			"reason": "remanage", "path": path, "mode": configsets.ModeSurvey,
		})
	})
	if err != nil {
		return err
	}

	if s.d.Sync == nil {
		return nil
	}
	return s.d.Sync.NotifyMachine(machineID, protocol.ReasonAssigned)
}
