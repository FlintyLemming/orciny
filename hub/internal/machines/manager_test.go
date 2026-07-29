package machines_test

import (
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

var start = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// fakeConn 是注册表眼中的一条连接。它记录被关闭的次数与原因，
// 这样「旧连接被踢」这类断言可以直接读出来。
type fakeConn struct {
	fp string
	id string

	mu       sync.Mutex
	closed   bool
	closeMsg string
	results  []protocol.AuthResult
}

func (c *fakeConn) Fingerprint() string { return c.fp }
func (c *fakeConn) MachineID() string   { return c.id }
func (c *fakeConn) RemoteAddr() string  { return "127.0.0.1:1234" }

func (c *fakeConn) SendAuthResult(ok bool, reason string, code uint8) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, protocol.AuthResult{OK: ok, Reason: reason, Code: code})
	return nil
}

func (c *fakeConn) Close(_ uint16, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.closeMsg = reason
	return nil
}

func (c *fakeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeConn) lastResult() (protocol.AuthResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.results) == 0 {
		return protocol.AuthResult{}, false
	}
	return c.results[len(c.results)-1], true
}

type fixture struct {
	app *tests.TestApp
	mgr *machines.Manager
	clk *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	clk := clock.NewFake(start)
	mgr := machines.NewManager(app, events.NewWriter(app), clk, 5*time.Second)
	t.Cleanup(mgr.Stop)

	return &fixture{app: app, mgr: mgr, clk: clk}
}

// newManagerOn 在同一个数据库上造一个全新的 Manager，模拟 hub 重启。
func newManagerOn(t *testing.T, f *fixture) *machines.Manager {
	t.Helper()
	m := machines.NewManager(f.app, events.NewWriter(f.app), clock.NewFake(start), 5*time.Second)
	t.Cleanup(m.Stop)
	return m
}

// newMachine 建一条 offline 的机器记录并返回一条对应的假连接。
func (f *fixture) newMachine(t *testing.T, fp string) *fakeConn {
	t.Helper()
	c, err := f.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("fingerprint", fp)
	r.Set("pub_key", "k")
	r.Set("status", "offline")
	require.NoError(t, f.app.Save(r))
	return &fakeConn{fp: fp, id: r.Id}
}

func (f *fixture) status(t *testing.T, id string) string {
	t.Helper()
	r, err := f.app.FindRecordById("machines", id)
	require.NoError(t, err)
	return r.GetString("status")
}

func (f *fixture) eventCount(t *testing.T, kind string) int {
	t.Helper()
	recs, err := f.app.FindRecordsByFilter("events", "kind = {:k}", "", 0, 0,
		map[string]any{"k": kind})
	require.NoError(t, err)
	return len(recs)
}

// —— 用例 ——

func TestRegisterMarksOnline(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")

	require.NoError(t, f.mgr.Register(c))
	require.Equal(t, "online", f.status(t, c.id))
	require.Equal(t, 1, f.mgr.Count())
	require.Equal(t, 1, f.eventCount(t, events.KindMachineConnected))
}

func TestUnregisterWaitsForGraceBeforeOffline(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	f.mgr.Unregister(c)
	require.Equal(t, "online", f.status(t, c.id), "宽限期内不得转 offline")
	require.Equal(t, 0, f.mgr.Count(), "但注册表里已经没有它了")

	// 等定时器挂上再推进，避免竞态
	require.Eventually(t, func() bool { return f.clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond)

	f.clk.Advance(5 * time.Second)
	require.Eventually(t, func() bool { return f.status(t, c.id) == "offline" },
		2*time.Second, 5*time.Millisecond)
	require.Equal(t, 1, f.eventCount(t, events.KindMachineDisconnected))
}

// 断开后 3 秒内重连：状态始终 online，且不产生 disconnected 事件。
func TestReconnectWithinGraceKeepsOnline(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	f.mgr.Unregister(c)
	require.Eventually(t, func() bool { return f.clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond)

	f.clk.Advance(3 * time.Second)

	again := &fakeConn{fp: "fp-1", id: c.id}
	require.NoError(t, f.mgr.Register(again))

	f.clk.Advance(10 * time.Second) // 越过原定的到期点

	require.Equal(t, "online", f.status(t, c.id))
	require.Equal(t, 0, f.eventCount(t, events.KindMachineDisconnected),
		"抖动不该在事件流里留下痕迹")
}

// 最易写错处（spec §6.4a）：同一指纹二次连接，旧连接被踢，
// 旧连接的 OnClose 随后触发 Unregister —— 它不能把新连接的状态弄掉线。
func TestSecondConnectionEvictsFirstWithoutGoingOffline(t *testing.T) {
	f := newFixture(t)
	first := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(first))

	second := &fakeConn{fp: "fp-1", id: first.id}
	require.NoError(t, f.mgr.Register(second))

	require.True(t, first.isClosed(), "旧连接必须被关闭")
	require.Equal(t, "replaced", first.closeMsg)
	require.False(t, second.isClosed())
	require.Equal(t, 1, f.mgr.Count())

	// 旧连接的 OnClose 迟到了
	f.mgr.Unregister(first)

	require.Equal(t, "online", f.status(t, first.id))
	require.Equal(t, 0, f.clk.TimerCount(), "顶替不该挂上宽限定时器")

	f.clk.Advance(time.Minute)
	require.Equal(t, "online", f.status(t, first.id))
	require.Equal(t, 0, f.eventCount(t, events.KindMachineDisconnected))

	got, ok := f.mgr.Get("fp-1")
	require.True(t, ok)
	require.Same(t, second, got)
}

func TestUnregisterOfStaleConnIsNoop(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	stale := &fakeConn{fp: "fp-1", id: c.id}
	f.mgr.Unregister(stale) // 从未登记过的连接

	require.Equal(t, 1, f.mgr.Count())
	require.Equal(t, 0, f.clk.TimerCount())
}

func TestLastSeenWrittenOnStatusChangeOnly(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")

	require.NoError(t, f.mgr.Register(c))
	online, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	require.False(t, online.GetDateTime("last_seen").IsZero(), "上线时写一次")

	f.clk.Advance(time.Hour) // 期间没有状态变化
	same, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	require.Equal(t, online.GetDateTime("last_seen"), same.GetDateTime("last_seen"),
		"没有状态变化就不该写库（spec §6.5）")

	f.mgr.Unregister(c)
	require.Eventually(t, func() bool { return f.clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond)
	f.clk.Advance(5 * time.Second)

	require.Eventually(t, func() bool {
		r, err := f.app.FindRecordById("machines", c.id)
		return err == nil && r.GetString("status") == "offline"
	}, 2*time.Second, 5*time.Millisecond)

	off, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	require.True(t, off.GetDateTime("last_seen").Time().After(online.GetDateTime("last_seen").Time()),
		"离线时 last_seen 记的是断开那一刻")
}

func TestUpdateInfoWritesToRecord(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")
	require.NoError(t, f.mgr.Register(c))

	require.NoError(t, f.mgr.UpdateInfo(c, protocol.MachineInfo{
		Hostname: "build-01", OS: "darwin", Arch: "arm64", AgentVersion: "0.1.0",
		ToolVersions: map[string]string{"claude-code": "2.1.3"},
	}))

	r, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	require.Equal(t, "build-01", r.GetString("hostname"))
	require.Equal(t, "darwin", r.GetString("os"))
	require.Equal(t, "arm64", r.GetString("arch"))
	require.Equal(t, "0.1.0", r.GetString("agent_version"))

	// JSONField 读不了点号 key（见统括计划修正 D），只能整体反序列化。
	var tools map[string]string
	require.NoError(t, r.UnmarshalJSONField("tool_versions", &tools))
	require.Equal(t, "2.1.3", tools["claude-code"])

	require.Equal(t, "online", r.GetString("status"), "上报信息不该动状态")
}

func TestUpdateInfoDoesNotOverwriteUserName(t *testing.T) {
	f := newFixture(t)
	c := f.newMachine(t, "fp-1")

	r, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	r.Set("name", "我的构建机")
	require.NoError(t, f.app.Save(r))

	require.NoError(t, f.mgr.Register(c))
	require.NoError(t, f.mgr.UpdateInfo(c, protocol.MachineInfo{Hostname: "build-01", OS: "darwin", Arch: "arm64"}))

	after, err := f.app.FindRecordById("machines", c.id)
	require.NoError(t, err)
	require.Equal(t, "我的构建机", after.GetString("name"))
}

func TestRegisterUnknownMachineFails(t *testing.T) {
	f := newFixture(t)
	// 记录不存在的连接不该被登记 —— 握手已经查过库，走到这里说明记录刚被删。
	err := f.mgr.Register(&fakeConn{fp: "fp-x", id: "不存在的id"})
	require.Error(t, err)
	require.Equal(t, 0, f.mgr.Count())
}

func TestThreeMachinesAreIndependent(t *testing.T) {
	f := newFixture(t)
	a := f.newMachine(t, "fp-a")
	b := f.newMachine(t, "fp-b")
	c := f.newMachine(t, "fp-c")

	for _, conn := range []*fakeConn{a, b, c} {
		require.NoError(t, f.mgr.Register(conn))
	}
	require.Equal(t, 3, f.mgr.Count())

	f.mgr.Unregister(b)
	require.Eventually(t, func() bool { return f.clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond)
	f.clk.Advance(5 * time.Second)

	require.Eventually(t, func() bool { return f.status(t, b.id) == "offline" },
		2*time.Second, 5*time.Millisecond)
	require.Equal(t, "online", f.status(t, a.id))
	require.Equal(t, "online", f.status(t, c.id))
	require.Equal(t, 2, f.mgr.Count())
}
