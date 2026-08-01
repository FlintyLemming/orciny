package hub

import (
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
