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

// NotifyCredential 在轮换后通知受影响的机器。
//
// 复用 ConfigNotify 且**不带 RevisionID**：agent 拉回来发现 revision 相同
// 但 secrets 变了，只重渲染受影响的文件。不产生新 Revision（产品 §4.5）。
func (s *Service) NotifyCredential(name string) error {
	setIDs, _, err := s.d.Creds.ReferencedBy(name)
	if err != nil {
		return err
	}
	for _, setID := range setIDs {
		set, err := s.d.App.FindRecordById("config_sets", setID)
		if err != nil || set.GetBool("paused") {
			continue
		}
		machineIDs, err := s.d.Sets.AssignedMachines(setID)
		if err != nil {
			return err
		}
		for _, id := range machineIDs {
			s.send(id, protocol.ConfigNotify{ConfigSetID: setID, Reason: protocol.ReasonRotated})
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
