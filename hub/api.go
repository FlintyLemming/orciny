package hub

import (
	"fmt"

	"github.com/FlintyLemming/orciny/hub/internal/importer"
	"github.com/FlintyLemming/orciny/protocol"
)

// AssignConfigSet 指派配置集并立即通知该机器。
func (h *Hub) AssignConfigSet(machineID, setID, mode string) error {
	if _, err := h.sets.Assign(machineID, setID, mode); err != nil {
		return err
	}
	return h.sync.NotifyMachine(machineID, protocol.ReasonAssigned)
}

// CreateCredential 新建凭据。
func (h *Hub) CreateCredential(name, value, note string) error {
	_, err := h.creds.Create(name, value, note)
	return err
}

// RotateCredential 轮换凭据。
//
// **已废弃**：凭据实体在 M1.6 里被废止，key 内联到 provider 上，
// 轮换走 UpdateProvider → NotifyProvider。本方法与它的路由在子计划 08 删除。
func (h *Hub) RotateCredential(name, value string) error {
	return h.creds.Rotate(name, value)
}

// DeleteCredential 删除凭据。被引用时拒绝。
func (h *Hub) DeleteCredential(name string) error {
	return h.creds.Delete(name)
}

// PublishConfigSet 发布草稿并通知全部指派机器。
func (h *Hub) PublishConfigSet(setID, note string) (string, error) {
	rev, err := h.revs.Publish(setID, note, "publish")
	if err != nil {
		return "", err
	}
	return rev.Id, h.sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonPublished)
}

// RollbackConfigSet 回滚到指定版本并通知。
func (h *Hub) RollbackConfigSet(setID, revisionID string) (string, error) {
	rev, err := h.revs.Rollback(setID, revisionID)
	if err != nil {
		return "", err
	}
	return rev.Id, h.sync.NotifyConfigSet(setID, rev.Id, protocol.ReasonPublished)
}

// CloneConfigSet 复制配置集的 manifest 与 draft 到新名字。
func (h *Hub) CloneConfigSet(setID, newName string) (string, error) {
	src, err := h.App.FindRecordById("config_sets", setID)
	if err != nil {
		return "", fmt.Errorf("配置集 %s 不存在: %w", setID, err)
	}
	rec, err := h.sets.Create(newName, src.GetString("note"))
	if err != nil {
		return "", err
	}
	m, err := h.sets.Manifest(setID)
	if err != nil {
		return "", err
	}
	if err := h.sets.SetManifest(rec.Id, m); err != nil {
		return "", err
	}
	draft, err := h.sets.Draft(setID)
	if err != nil {
		return "", err
	}
	if err := h.sets.SetDraft(rec.Id, draft); err != nil {
		return "", err
	}
	return rec.Id, nil
}

// DeleteConfigSet 删除配置集并回收其孤儿 blob。
// 已指派的机器会因 CascadeDelete 失去指派；磁盘上的文件不会被改动。
func (h *Hub) DeleteConfigSet(setID string) error {
	rec, err := h.App.FindRecordById("config_sets", setID)
	if err != nil {
		return fmt.Errorf("配置集 %s 不存在: %w", setID, err)
	}
	if err := h.App.Delete(rec); err != nil {
		return fmt.Errorf("删除配置集 %s: %w", setID, err)
	}
	if _, err := h.blobs.GCOrphans(setID); err != nil {
		h.App.Logger().Warn("回收配置集孤儿 blob 失败", "set", setID, "error", err)
	}
	return nil
}

// StartImport 触发一次导入采集，返回采集 token 与新建的草稿配置集 id。
func (h *Hub) StartImport(machineID string) (string, string, error) {
	return h.importer.Start(machineID)
}

// ImportFindings 对草稿跑一遍敏感项检测。
func (h *Hub) ImportFindings(setID string) ([]importer.Finding, error) {
	return h.importer.Findings(setID)
}

// ExtractCredential 把草稿里某处的值抽成凭据。
func (h *Hub) ExtractCredential(setID, path, location, name string) error {
	return h.importer.Extract(setID, path, location, name)
}

// AdoptDrift 收编选中的 open 漂移为新版本，并通知全部指派机器。
func (h *Hub) AdoptDrift(eventIDs []string) (string, error) {
	return h.AdoptDriftReviewed(eventIDs, nil)
}

// AdoptDriftReviewed 同 AdoptDrift，但允许调用方声明已人工确认的
// restore_partial 条目（spec §8.3）。
func (h *Hub) AdoptDriftReviewed(eventIDs, reviewed []string) (string, error) {
	rev, err := h.drift.AdoptReviewed(eventIDs, reviewed)
	if err != nil {
		return "", err
	}
	return rev.Id, nil
}

// RestoreDrift 向相关机器下发恢复指令。状态要等 agent 回执才变。
func (h *Hub) RestoreDrift(eventIDs []string) error {
	return h.drift.Restore(eventIDs)
}

// IgnoreDrift 写忽略规则并立即通知来源机器。
// global 为真时规则对全机队生效。
func (h *Hub) IgnoreDrift(eventIDs []string, global bool) error {
	return h.drift.Ignore(eventIDs, global)
}

// ClearDegraded 解除机器的 degraded 状态，打回 survey 让差异先进收件箱。
func (h *Hub) ClearDegraded(machineID string) error {
	return h.sync.ClearDegraded(machineID)
}
