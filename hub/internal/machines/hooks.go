package machines

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// OnRecordDeleted 绑在 OnRecordAfterDeleteSuccess("machines") 上。
//
// record hook 只做状态机联动，不放业务规则（spec §5.1 第 2 条）：
// 这里唯一的动作是「把这台机器的活跃连接踢掉，并告诉它原因」。
func (m *Manager) OnRecordDeleted(e *core.RecordEvent) error {
	fingerprint := e.Record.GetString("fingerprint")

	m.mu.Lock()
	c, ok := m.conns[fingerprint]
	if ok {
		delete(m.conns, fingerprint)
	}
	// 连上来的机器被删后，任何待定的离线定时器都没有意义了 —— 记录已不存在，
	// 到期去写库只会报「找不到记录」。
	if t, pending := m.pending[fingerprint]; pending {
		t.Stop()
		delete(m.pending, fingerprint)
	}
	m.mu.Unlock()

	if err := m.ev.Write(events.KindMachineRemoved, "", map[string]any{
		"fingerprint": fingerprint,
		"name":        e.Record.GetString("name"),
	}); err != nil {
		m.app.Logger().Warn("写 machine.removed 事件失败", "error", err)
	}

	if !ok {
		return e.Next()
	}

	// 先说明原因再关闭：agent 收到 CodeMachineRemoved 后会停止重试（spec §7.2）。
	if err := c.SendAuthResult(false, "该机器已在面板中被删除，请重新 enroll", protocol.CodeMachineRemoved); err != nil {
		m.app.Logger().Debug("发送撤销通知失败", "fingerprint", fingerprint, "error", err)
	}
	if err := c.Close(1000, "machine removed"); err != nil {
		m.app.Logger().Debug("关闭被删除机器的连接失败", "fingerprint", fingerprint, "error", err)
	}
	return e.Next()
}
