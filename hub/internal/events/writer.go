// Package events 写入产品文档 §6 意义上的「轻量事件流」——不是合规审计。
// 事件产生速率很低，M0 不做保留期清理（spec §8）。
package events

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// 事件 kind 的唯一来源。新增 kind 必须先在这里定义，不允许在调用处写字面量。
const (
	KindMachineEnrolled     = "machine.enrolled"
	KindMachineReEnrolled   = "machine.re-enrolled"
	KindMachineConnected    = "machine.connected"
	KindMachineDisconnected = "machine.disconnected"
	KindMachineRemoved      = "machine.removed"
	KindTokenIssued         = "token.issued"
	KindAuthFailed          = "auth.failed"
)

// M1 新增（spec §8.5）。events.kind 是自由文本，加取值不需要迁移。
const (
	KindConfigSetPublished  = "configset.published"
	KindConfigSetRolledBack = "configset.rolled_back"
	KindAssignChanged       = "assign.changed"

	KindApplyOK             = "apply.ok"
	KindApplyFailed         = "apply.failed"
	KindApplyRollbackFailed = "apply.rollback_failed"

	KindDriftReported   = "drift.reported"
	KindDriftAdopted    = "drift.adopted"
	KindDriftRestored   = "drift.restored"
	KindDriftIgnored    = "drift.ignored"
	KindDriftSuperseded = "drift.superseded"

	// credential.created / .rotated / .deleted 三个 kind 随凭据实体一起废止
	// （M1.6 spec §3.5）。Go 侧没有写入方了，但**历史 events 行留着**——
	// 前端的 EventKind 联合类型仍保留这三个字符串，否则老事件渲染不出来。

	KindImportCompleted = "import.completed"

	// M1.5 服务绑定（spec §2）。改 Provider 内部不产生新 Revision，
	// 因此这几条事件是唯一的审计痕迹。
	KindProviderCreated = "provider.created"
	KindProviderUpdated = "provider.updated"
	KindProviderDeleted = "provider.deleted"
	KindBindingChanged  = "binding.changed"

	// M1.8 本机覆盖层（spec §2、§8.5）。覆盖层的增删不产生新 Revision，
	// 因此这几条事件是唯一的审计痕迹。
	KindOverrideCreated  = "override.created"
	KindOverrideDropped  = "override.dropped"
	KindOverrideReplaced = "override.replaced"
	KindOverrideKept     = "override.kept"
)

// Writer 往 events collection 写记录。
type Writer struct {
	app core.App
}

func NewWriter(app core.App) *Writer { return &Writer{app: app} }

// Write 用构造时的 app 写事件。
func (w *Writer) Write(kind, machineID string, detail map[string]any) error {
	return w.WriteTx(w.app, kind, machineID, detail)
}

// WriteTx 用指定的 app 写事件——在事务里调用时必须传 txApp，
// 否则事务回滚了事件还留着。
func (w *Writer) WriteTx(txApp core.App, kind, machineID string, detail map[string]any) error {
	c, err := txApp.FindCollectionByNameOrId("events")
	if err != nil {
		return fmt.Errorf("events: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("kind", kind)
	if machineID != "" {
		r.Set("machine", machineID)
	}
	if detail != nil {
		r.Set("detail", detail)
	}
	if err := txApp.Save(r); err != nil {
		return fmt.Errorf("events: 写入 %s: %w", kind, err)
	}
	return nil
}
