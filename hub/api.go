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

// RotateCredential 轮换凭据并通知受影响的机器。不产生新 Revision。
func (h *Hub) RotateCredential(name, value string) error {
	if err := h.creds.Rotate(name, value); err != nil {
		return err
	}
	return h.sync.NotifyCredential(name)
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
