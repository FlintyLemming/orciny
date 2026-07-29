package machines

import (
	"fmt"

	"github.com/pocketbase/dbx"
)

// ResetGhosts 把 DB 里残留的 online 全部翻成 offline。
//
// 连接注册表在内存里，hub 进程重启后为空，但库里的 status='online' 还在
// （spec §6.4b）。在 OnServe 阶段跑一次，随后 agent 陆续重连转回 online。
//
// 用一条 UPDATE 而不是逐条 Save：这是启动路径，机器可能有几十台，
// 且这里不需要触发 realtime——前端此刻还没连上来。
func (m *Manager) ResetGhosts() error {
	res, err := m.app.DB().NewQuery(`
		UPDATE {{machines}} SET status = 'offline' WHERE status = 'online'
	`).Bind(dbx.Params{}).Execute()
	if err != nil {
		return fmt.Errorf("machines: 清理幽灵 online: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		m.app.Logger().Info("已清理重启前残留的 online 状态", "count", n)
	}
	return nil
}
