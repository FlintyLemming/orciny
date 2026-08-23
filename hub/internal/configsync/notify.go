package configsync

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// NotifyConfigSet 通知指派了该配置集的全部机器。
//
// 离线的机器不需要队列：它上线后由 OnMachineOnline 无条件补发一次，
// 拉到的快照若与本地一致则 plan 全 skip、零写入。幂等是补发机制的替代品
// （spec §7.3）。
func (s *Service) NotifyConfigSet(setID, revisionID, reason string) error {
	set, err := s.d.App.FindRecordById("config_sets", setID)
	if err != nil {
		return fmt.Errorf("configsync: 配置集 %s 不存在: %w", setID, err)
	}
	if set.GetBool("paused") {
		s.log.Info("配置集已暂停下发，跳过通知", "config_set", setID)
		return nil
	}

	machineIDs, err := s.d.Sets.AssignedMachines(setID)
	if err != nil {
		return err
	}
	seq := uint32(0)
	if revisionID != "" {
		if rev, err := s.d.App.FindRecordById("revisions", revisionID); err == nil {
			seq = uint32(rev.GetInt("seq"))
		}
	}
	n := protocol.ConfigNotify{
		ConfigSetID: setID, RevisionID: revisionID, Seq: seq, Reason: reason,
	}
	for _, id := range machineIDs {
		s.send(id, n)
	}
	return nil
}

// NotifyMachine 通知单台机器（指派变更、状态自愈）。
func (s *Service) NotifyMachine(machineID, reason string) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		if errors.Is(err, configsets.ErrNoAssignment) {
			return nil
		}
		return err
	}
	setID := assign.GetString("config_set")
	set, err := s.d.App.FindRecordById("config_sets", setID)
	if err != nil {
		return fmt.Errorf("configsync: 配置集 %s 不存在: %w", setID, err)
	}
	if set.GetBool("paused") {
		return nil
	}
	head := set.GetString("head")
	seq := uint32(0)
	if head != "" {
		if rev, err := s.d.App.FindRecordById("revisions", head); err == nil {
			seq = uint32(rev.GetInt("seq"))
		}
	}
	s.send(machineID, protocol.ConfigNotify{
		ConfigSetID: setID, RevisionID: head, Seq: seq, Reason: reason,
	})
	return nil
}

// NotifyProvider 在改了 Provider 之后重注入全机队（M1.5 spec §5.2）。
//
// **不产生新 Revision**：走 ConfigNotify 且不带 RevisionID（= 仅 secrets 变更，
// M1 已有的语义）。agent 拉回来发现 revision 相同但 provider 值变了，
// 只重渲染受影响的文件；rendered hash 跟着更新，因此**不产生漂移**
// （spec §4.3——这条是 M1 设计正确性的红利，一行新代码都不需要）。
//
// 反查靠 config_sets.head_provider 这个冗余字段收敛成一次索引查询，
// 而不是 JSON 字段扫描 + 三次 join（spec §2.2）。
func (s *Service) NotifyProvider(providerID string) error {
	sets, err := s.d.App.FindRecordsByFilter("config_sets",
		"head_provider = {:p}", "", 0, 0, map[string]any{"p": providerID})
	if err != nil {
		return fmt.Errorf("configsync: 查询绑定了服务配置 %s 的配置集: %w", providerID, err)
	}
	for _, set := range sets {
		if set.GetBool("paused") {
			s.log.Info("配置集已暂停下发，跳过重注入", "config_set", set.Id)
			continue
		}
		machineIDs, err := s.d.Sets.AssignedMachines(set.Id)
		if err != nil {
			return err
		}
		for _, id := range machineIDs {
			s.send(id, protocol.ConfigNotify{
				ConfigSetID: set.Id, Reason: protocol.ReasonRotated,
			})
		}
	}
	return nil
}

// OnMachineOnline 是握手成功后的补发入口。
func (s *Service) OnMachineOnline(machineID string) {
	if err := s.NotifyMachine(machineID, protocol.ReasonAssigned); err != nil {
		s.log.Warn("上线补发通知失败", "machine", machineID, "error", err)
	}
}

func (s *Service) send(machineID string, n protocol.ConfigNotify) {
	if err := s.d.Sender.SendTo(machineID, protocol.KindConfigNotify, n); err != nil {
		// 离线不是错误：上线时会补发。
		s.log.Debug("通知未送达", "machine", machineID, "error", err)
	}
}

// ApplyAck 落库回执并更新指派状态（spec §7.3）。
func (s *Service) ApplyAck(machineID string, a protocol.ApplyAck) {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		s.log.Warn("收到回执但机器未指派配置集", "machine", machineID)
		return
	}

	detail := map[string]any{
		"revision":    a.RevisionID,
		"duration_ms": a.DurationMs,
		"results":     len(a.Results),
	}
	switch {
	case a.OK:
		assign.Set("state", configsets.StateAligned)
		assign.Set("applied_revision", a.RevisionID)
		assign.Set("applied_at", types.NowDateTime())
		assign.Set("last_error", "")
		s.writeEvent(events.KindApplyOK, machineID, detail)
		s.resolveDriftOnApply(machineID, a)
	case a.RolledBack:
		assign.Set("state", configsets.StateFailed)
		assign.Set("last_error", a.Error)
		detail["error"] = a.Error
		s.writeEvent(events.KindApplyFailed, machineID, detail)
	default:
		// 回滚都失败了：机器进 degraded，不再自动 apply（spec §7.4 第 2 条）。
		assign.Set("state", configsets.StateDegraded)
		assign.Set("last_error", a.Error)
		detail["error"] = a.Error
		s.writeEvent(events.KindApplyRollbackFailed, machineID, detail)
	}
	if err := s.d.App.Save(assign); err != nil {
		s.log.Warn("保存指派状态失败", "machine", machineID, "error", err)
	}
}

func (s *Service) writeEvent(kind, machineID string, detail map[string]any) {
	if err := s.d.Events.Write(kind, machineID, detail); err != nil {
		s.log.Warn("写事件失败", "kind", kind, "error", err)
	}
}

// resolveDriftOnApply 在成功 apply 后处置收件箱：
// 1. 先 MarkRestored（只认刚下过 restore 指令的路径）
// 2. 再 Supersede（剩下的 open 漂移是被覆盖的，spec §7.7）
//
// 顺序不能反：先 supersede 再 markRestored 会把被覆盖的也标成 restored。
func (s *Service) resolveDriftOnApply(machineID string, a protocol.ApplyAck) {
	if s.d.Drift == nil || len(a.Results) == 0 {
		return
	}
	var all, overwritten []string
	for _, res := range a.Results {
		all = append(all, res.Path)
		switch res.Action {
		case protocol.ActionOverwrite, protocol.ActionDelete, protocol.ActionMerge:
			overwritten = append(overwritten, res.Path)
		}
	}
	if err := s.d.Drift.MarkRestored(machineID, all); err != nil {
		s.log.Warn("标记已恢复漂移失败", "machine", machineID, "error", err)
	}
	if len(overwritten) > 0 {
		if err := s.d.Drift.Supersede(machineID, overwritten, a.RevisionID); err != nil {
			s.log.Warn("标记被覆盖的漂移失败", "machine", machineID, "error", err)
		}
	}
}
