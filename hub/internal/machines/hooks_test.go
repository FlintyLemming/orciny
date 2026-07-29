package machines_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// hub 重启后连接注册表是空的，但 DB 里的 status='online' 还在（spec §6.4b）。
func TestResetGhostsFlipsOnlineToOffline(t *testing.T) {
	f := newFixture(t)
	a := f.newMachine(t, "fp-a")
	b := f.newMachine(t, "fp-b")

	require.NoError(t, f.mgr.Register(a))
	require.NoError(t, f.mgr.Register(b))
	require.Equal(t, "online", f.status(t, a.id))

	// 模拟重启：新的 Manager，空注册表
	fresh := newManagerOn(t, f)
	require.NoError(t, fresh.ResetGhosts())

	require.Equal(t, "offline", f.status(t, a.id))
	require.Equal(t, "offline", f.status(t, b.id))
}

func TestResetGhostsLeavesPausedAlone(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-p")

	r, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	r.Set("status", "paused")
	require.NoError(t, f.app.Save(r))

	require.NoError(t, newManagerOn(t, f).ResetGhosts())

	require.Equal(t, "paused", f.status(t, c.id), "paused 不是幽灵，不要动它")
}

func TestResetGhostsOnEmptyDBIsNoop(t *testing.T) {
	f := newFixture(t)
	require.NoError(t, f.mgr.ResetGhosts())
}

// 机器在 UI 中被删除：先发 CodeMachineRemoved 让 agent 知道原因，再关连接。
func TestOnRecordDeletedKicksActiveConnection(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	rec, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)

	ev := new(core.RecordEvent)
	ev.App = f.app
	ev.Record = rec
	require.NoError(t, f.mgr.OnRecordDeleted(ev))

	res, ok := c.lastResult()
	require.True(t, ok, "必须先发一条 AuthResult 说明原因")
	require.False(t, res.OK)
	require.Equal(t, protocol.CodeMachineRemoved, res.Code)
	require.True(t, c.isClosed())
	require.Equal(t, 0, f.mgr.Count())
}

func TestOnRecordDeletedWithoutConnectionIsNoop(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1") // 没有 Register

	rec, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	ev := new(core.RecordEvent)
	ev.App = f.app
	ev.Record = rec

	require.NoError(t, f.mgr.OnRecordDeleted(ev))
	require.False(t, c.isClosed())
}

// 删除时踢掉连接后，宽限定时器不该再把它写成 offline —— 记录已经没了。
func TestOnRecordDeletedDoesNotScheduleOffline(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	rec, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	ev := new(core.RecordEvent)
	ev.App = f.app
	ev.Record = rec
	require.NoError(t, f.mgr.OnRecordDeleted(ev))

	// 连接层随后触发的 Unregister
	f.mgr.Unregister(c)
	f.clk.Advance(time.Minute)

	require.Equal(t, 0, f.eventCount(t, events.KindMachineDisconnected),
		"机器都删了，不该再产生断连事件")
}
