# M0 计划 6 · 连接注册表与在线状态机 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 面板上的绿灯红灯变准：连接登记与踢除、5 秒离线宽限、hub 重启后的幽灵清理、删除机器时的连接联动、`MachineInfo` 落库。

**Architecture:** 注册表在内存，`fingerprint → Conn` 一对一。离线判定不是「连接一断就红」，而是「断开后启动一个 5 秒定时器，到期时若该指纹仍无连接才置 offline」——定时器走 `clock.Clock`，测试里瞬间推进。最容易写错的是「旧连接被新连接顶替」：旧连接的 `OnClose` 照常触发，此时注册表里已经是新连接，因此每一步都要先核对「注册表里这个指纹的当前连接是不是它」。

**Tech Stack:** Go 1.26 · PocketBase v0.39.9（record hook + realtime）· `internal/clock`

**上位文档：** [统括计划](00-overview.md) · [spec §6](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；`hub/internal/*` 的单测写在包内；`machines` **不得** import `ws`。
- 测试禁用 `time.Sleep`：5 秒宽限走 `clock.Fake`，用 `require.Eventually` 等可观测效果。
- record hook 只用于状态机联动，不放业务规则（spec §5.1 第 2 条）。
- `last_seen` 只在状态变化时持久化，不在每次 pong 时写库（spec §6.5）。
- 事件 kind 只允许统括计划里列的七个字符串。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `hub/internal/machines/manager.go` | `Manager`：注册表、宽限定时器、状态写库 |
| `hub/internal/machines/manager_test.go` | 包内单测（假连接 + 假时钟，穷举时序） |
| `hub/internal/machines/ghosts.go` | `ResetGhosts`：hub 重启后的幽灵清理 |
| `hub/internal/machines/hooks.go` | `OnRecordDeleted`：删除机器时踢连接 |
| `hub/hub.go` | 修改：构造 Manager、`OnServe` 里清幽灵并启动、绑 record hook |
| `internal/testsupport/hub.go` | 修改：暴露 `(*TestHub).MachineStatus` 等断言辅助 |
| `internal/testsupport/lifecycle_test.go` | 端到端生命周期用例 |

---

## Task 1: Manager 与 5 秒宽限

**Files:**
- Create: `hub/internal/machines/manager.go`, `hub/internal/machines/manager_test.go`

**Interfaces:**
- Consumes: 计划 5 的 `machines.Conn` / `machines.Registry` 接口、`events.Writer`、`clock.Clock`
- Produces:
  ```go
  type Manager struct{ /* 私有字段 */ }
  func NewManager(app core.App, ev *events.Writer, clk clock.Clock, grace time.Duration) *Manager
  func (m *Manager) Register(c Conn) error          // 实现 Registry
  func (m *Manager) Unregister(c Conn)              // 实现 Registry
  func (m *Manager) UpdateInfo(c Conn, info protocol.MachineInfo) error
  func (m *Manager) Get(fingerprint string) (Conn, bool)
  func (m *Manager) Count() int
  func (m *Manager) Stop()                          // 取消全部待定定时器
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/machines/manager_test.go`：

```go
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
	require.Equal(t, "2.1.3", r.GetString("tool_versions.claude-code"))
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/machines/... -v`
Expected: FAIL —— `undefined: machines.NewManager`

- [ ] **Step 3: 写实现**

创建 `hub/internal/machines/manager.go`：

```go
package machines

import (
	"fmt"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// Manager 是连接注册表与在线状态机。
//
// 离线判定不是「连接一断就红」：直接在 WS 关闭时置 offline 会让面板在网络
// 抖动时不停闪红（spec §6.3）。这里的做法是断开后挂一个宽限定时器，
// 到期时若该指纹仍无连接才真的置 offline。
type Manager struct {
	app   core.App
	ev    *events.Writer
	clk   clock.Clock
	grace time.Duration

	mu      sync.Mutex
	conns   map[string]Conn        // fingerprint → 当前连接
	pending map[string]clock.Timer // fingerprint → 待定的离线定时器
	stopped bool
}

func NewManager(app core.App, ev *events.Writer, clk clock.Clock, grace time.Duration) *Manager {
	return &Manager{
		app:     app,
		ev:      ev,
		clk:     clk,
		grace:   grace,
		conns:   make(map[string]Conn),
		pending: make(map[string]clock.Timer),
	}
}

// Register 登记一条握手成功的连接。
//
// 同一指纹已有连接时**新连接胜出**（agent 重启但 hub 还没检测到旧连接
// 断开时会发生，spec §6.4a）。旧连接以 close(1000, "replaced") 关闭，
// 它随后触发的 Unregister 会因为「注册表里已经不是它」而变成空操作。
func (m *Manager) Register(c Conn) error {
	fp := c.Fingerprint()

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return fmt.Errorf("machines: 管理器已停止")
	}
	old, hadOld := m.conns[fp]
	m.conns[fp] = c
	if t, ok := m.pending[fp]; ok {
		t.Stop()
		delete(m.pending, fp)
	}
	m.mu.Unlock()

	if hadOld && old != c {
		if err := old.Close(1000, "replaced"); err != nil {
			m.app.Logger().Debug("关闭被顶替的连接时出错", "fingerprint", fp, "error", err)
		}
	}

	// 已经是 online 就不重复写库，也不重复发事件（重连在宽限期内的情形）。
	changed, err := m.setStatus(c.MachineID(), "online")
	if err != nil {
		// 登记失败要把连接从表里撤掉，否则会留下一条指向不存在记录的连接。
		m.mu.Lock()
		if m.conns[fp] == c {
			delete(m.conns, fp)
		}
		m.mu.Unlock()
		return err
	}
	if changed {
		if err := m.ev.Write(events.KindMachineConnected, c.MachineID(), map[string]any{
			"fingerprint": fp,
			"remote":      c.RemoteAddr(),
		}); err != nil {
			m.app.Logger().Warn("写 machine.connected 事件失败", "error", err)
		}
	}
	return nil
}

// Unregister 在连接关闭时调用。
func (m *Manager) Unregister(c Conn) {
	fp := c.Fingerprint()

	m.mu.Lock()
	// 关键判断（spec §6.3）：只有当注册表里这个指纹的当前连接确实是它，
	// 才算「这台机器断了」。否则说明它已被新连接顶替，什么都不该做。
	if cur, ok := m.conns[fp]; !ok || cur != c {
		m.mu.Unlock()
		return
	}
	delete(m.conns, fp)
	if m.stopped {
		m.mu.Unlock()
		return
	}

	machineID := c.MachineID()
	timer := m.clk.NewTimer(m.grace)
	m.pending[fp] = timer
	m.mu.Unlock()

	go m.waitAndMarkOffline(fp, machineID, timer)
}

func (m *Manager) waitAndMarkOffline(fp, machineID string, timer clock.Timer) {
	<-timer.C()

	m.mu.Lock()
	// 定时器到期时若该指纹已有新连接，什么都不做。
	if cur, ok := m.pending[fp]; !ok || cur != timer {
		m.mu.Unlock()
		return
	}
	delete(m.pending, fp)
	if _, online := m.conns[fp]; online {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	changed, err := m.setStatus(machineID, "offline")
	if err != nil {
		m.app.Logger().Warn("置离线失败", "machine", machineID, "error", err)
		return
	}
	if changed {
		if err := m.ev.Write(events.KindMachineDisconnected, machineID, map[string]any{
			"fingerprint": fp,
		}); err != nil {
			m.app.Logger().Warn("写 machine.disconnected 事件失败", "error", err)
		}
	}
}

// UpdateInfo 落库握手后的首条业务消息。备注名是用户的，不动。
func (m *Manager) UpdateInfo(c Conn, info protocol.MachineInfo) error {
	r, err := m.app.FindRecordById("machines", c.MachineID())
	if err != nil {
		return fmt.Errorf("machines: 查询机器 %s: %w", c.MachineID(), err)
	}
	r.Set("hostname", info.Hostname)
	r.Set("os", info.OS)
	r.Set("arch", info.Arch)
	r.Set("agent_version", info.AgentVersion)
	if info.ToolVersions != nil {
		r.Set("tool_versions", info.ToolVersions)
	}
	if err := m.app.Save(r); err != nil {
		return fmt.Errorf("machines: 更新机器信息: %w", err)
	}
	return nil
}

// Get 返回某指纹当前的连接。
func (m *Manager) Get(fingerprint string) (Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[fingerprint]
	return c, ok
}

// Count 返回在线连接数。
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}

// Stop 取消全部待定定时器，用于关停与测试清理。
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	for fp, t := range m.pending {
		t.Stop()
		delete(m.pending, fp)
	}
}

// setStatus 写状态，返回状态是否真的变了。
// last_seen 只在这里写 —— 也就是只在状态变化时写（spec §6.5）。
func (m *Manager) setStatus(machineID, status string) (bool, error) {
	r, err := m.app.FindRecordById("machines", machineID)
	if err != nil {
		return false, fmt.Errorf("machines: 查询机器 %s: %w", machineID, err)
	}
	if r.GetString("status") == status {
		return false, nil
	}
	r.Set("status", status)
	r.Set("last_seen", m.clk.Now().UTC())
	if err := m.app.Save(r); err != nil {
		return false, fmt.Errorf("machines: 保存状态: %w", err)
	}
	return true, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./hub/internal/machines/... -race -v`
Expected: PASS（10 个用例）

若 `TestUnregisterWaitsForGraceBeforeOffline` 偶发失败：确认测试里是先 `require.Eventually(TimerCount == 1)` 再 `Advance`——`Unregister` 里的定时器是在锁外的 goroutine 中等待的，不等它挂上就推进时钟会丢掉这次触发。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/machines
git commit -m "feat: 连接注册表、5 秒离线宽限与状态写库"
```

---

## Task 2: 幽灵清理与删除联动

**Files:**
- Create: `hub/internal/machines/ghosts.go`, `hub/internal/machines/hooks.go`, `hub/internal/machines/hooks_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Manager`
- Produces:
  ```go
  func (m *Manager) ResetGhosts() error
  func (m *Manager) OnRecordDeleted(e *core.RecordEvent) error
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/machines/hooks_test.go`：

```go
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
```

并在 `manager_test.go` 里补一个辅助函数：

```go
// newManagerOn 在同一个数据库上造一个全新的 Manager，模拟 hub 重启。
func newManagerOn(t *testing.T, f *fixture) *machines.Manager {
	t.Helper()
	m := machines.NewManager(f.app, events.NewWriter(f.app), clock.NewFake(start), 5*time.Second)
	t.Cleanup(m.Stop)
	return m
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/machines/... -run 'Ghost|Deleted' -v`
Expected: FAIL —— `undefined: ResetGhosts`

- [ ] **Step 3: 写幽灵清理**

创建 `hub/internal/machines/ghosts.go`：

```go
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
```

- [ ] **Step 4: 写删除联动**

创建 `hub/internal/machines/hooks.go`：

```go
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
```

> **注意 `e.Next()`：** PocketBase 的 hook 必须调用 `e.Next()` 把事件链传下去。测试里我们直接构造 `core.RecordEvent` 调用它——空的 hook 链上 `Next()` 返回 nil，因此测试里的 `require.NoError` 成立。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./hub/internal/machines/... -race -v`
Expected: PASS（16 个用例）

- [ ] **Step 6: 提交**

```bash
git add hub/internal/machines
git commit -m "feat: 幽灵 online 清理与删除机器时的连接联动"
```

---

## Task 3: 接进 hub 并跑通端到端生命周期

**Files:**
- Modify: `hub/hub.go`（构造 Manager、清幽灵、绑 hook、把 Nop 换成真身）, `internal/testsupport/hub.go`（断言辅助）
- Create: `internal/testsupport/lifecycle_test.go`

**Interfaces:**
- Consumes: `machines.Manager`
- Produces:
  ```go
  // internal/testsupport
  func (h *TestHub) MachineStatus(t *testing.T, machineID string) string
  func (h *TestHub) EventKinds(t *testing.T, machineID string) []string
  func (h *TestHub) RequireStatus(t *testing.T, machineID, want string)
  ```

- [ ] **Step 1: 装配 Manager**

修改 `hub/hub.go`：

给 `Hub` 加字段 `machines *machines.Manager`；在 `Attach` 里构造（它只需要 app、events、clock）：

```go
	h.machines = machines.NewManager(app, h.events, h.cfg.Clock, h.cfg.OfflineGrace)
```

在 `Attach` 里绑 record hook（注意：hook 要在 `OnServe` 之外绑，否则重复注册）：

```go
	app.OnRecordAfterDeleteSuccess("machines").BindFunc(h.machines.OnRecordDeleted)
```

在 `OnServe` 内，`identity.Load()` 之后、构造 `ws.NewHandler` 之前：

```go
		if err := h.machines.ResetGhosts(); err != nil {
			return fmt.Errorf("清理幽灵 online: %w", err)
		}
```

把 `ws.Deps` 里的 `Registry: machines.NopRegistry{}` 换成 `Registry: h.machines`。

- [ ] **Step 2: 加测试辅助**

在 `internal/testsupport/hub.go` 追加：

```go
// MachineStatus 读机器当前状态。
func (h *TestHub) MachineStatus(t *testing.T, machineID string) string {
	t.Helper()
	r, err := h.App.FindRecordById("machines", machineID)
	require.NoError(t, err)
	return r.GetString("status")
}

// RequireStatus 等到机器状态变成 want 为止。
//
// 用 Eventually 而不是直接断言：状态变化发生在另一个 goroutine 里
// （宽限定时器、WS 读循环），需要给它一点真实时间落地——但等待的是
// 「效果出现」，不是「睡够 5 秒」。
func (h *TestHub) RequireStatus(t *testing.T, machineID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		r, err := h.App.FindRecordById("machines", machineID)
		return err == nil && r.GetString("status") == want
	}, 3*time.Second, 10*time.Millisecond, "机器 %s 未变成 %s", machineID, want)
}

// EventKinds 返回某机器的事件 kind 列表，按时间正序。
func (h *TestHub) EventKinds(t *testing.T, machineID string) []string {
	t.Helper()
	recs, err := h.App.FindRecordsByFilter("events", "machine = {:m}", "created", 0, 0,
		map[string]any{"m": machineID})
	require.NoError(t, err)
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("kind"))
	}
	return out
}
```

- [ ] **Step 3: 写端到端生命周期用例**

创建 `internal/testsupport/lifecycle_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestConnectMarksMachineOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	require.Equal(t, "offline", th.MachineStatus(t, ta.MachineID))

	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")
	require.Contains(t, th.EventKinds(t, ta.MachineID), "machine.connected")
}

func TestMachineInfoLandsInRecord(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.Eventually(t, func() bool {
		r, err := th.App.FindRecordById("machines", ta.MachineID)
		return err == nil && r.GetString("agent_version") != ""
	}, 3*time.Second, 10*time.Millisecond)
}

// 断开后 6 秒无重连 → offline（spec §12.2）
func TestDisconnectGoesOfflineAfterGrace(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.NoError(t, s.Close())

	// 等宽限定时器挂上，再把逻辑时间推过 5 秒
	require.Eventually(t, func() bool { return th.Clock.TimerCount() >= 1 },
		3*time.Second, 10*time.Millisecond)
	th.Clock.Advance(6 * time.Second)

	th.RequireStatus(t, ta.MachineID, "offline")
	require.Contains(t, th.EventKinds(t, ta.MachineID), "machine.disconnected")
}

// 断开后 3 秒内重连 → 状态始终 online，不产生 disconnected 事件
func TestReconnectWithinGraceStaysOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	require.NoError(t, s.Close())
	require.Eventually(t, func() bool { return th.Clock.TimerCount() >= 1 },
		3*time.Second, 10*time.Millisecond)
	th.Clock.Advance(3 * time.Second)

	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")
	th.Clock.Advance(10 * time.Second)

	require.Equal(t, "online", th.MachineStatus(t, ta.MachineID))
	require.NotContains(t, th.EventKinds(t, ta.MachineID), "machine.disconnected")
}

// 同指纹二次连接 → 旧连接被踢，状态保持 online，不产生 disconnected 事件
func TestSecondConnectionKeepsOnline(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	first := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	second := ta.Connect(t, th)
	require.NotNil(t, second)

	select {
	case <-first.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("旧连接未被踢掉")
	}

	th.Clock.Advance(time.Minute)
	require.Equal(t, "online", th.MachineStatus(t, ta.MachineID))
	require.NotContains(t, th.EventKinds(t, ta.MachineID), "machine.disconnected")
}

// 删除机器 → 活跃连接被踢
func TestDeleteMachineKicksConnection(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	s := ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.NoError(t, th.App.Delete(m))

	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("删除机器后连接应被关闭")
	}

	_, err = th.App.FindRecordById("machines", ta.MachineID)
	require.Error(t, err, "记录应已删除")
}

// 三个 agent 并发接入同一 hub → 各自独立
func TestThreeAgentsConnectIndependently(t *testing.T) {
	th := testsupport.NewTestHub(t)

	agents := make([]*testsupport.TestAgent, 3)
	for i := range agents {
		agents[i] = testsupport.NewTestAgent(t, th)
		agents[i].Connect(t, th)
	}
	for _, a := range agents {
		th.RequireStatus(t, a.MachineID, "online")
	}

	// 掐掉中间那台
	s := agents[1].Connect(t, th) // 取得一个可关闭的句柄（新连接顶替旧的）
	require.NoError(t, s.Close())
	require.Eventually(t, func() bool { return th.Clock.TimerCount() >= 1 },
		3*time.Second, 10*time.Millisecond)
	th.Clock.Advance(6 * time.Second)

	th.RequireStatus(t, agents[1].MachineID, "offline")
	require.Equal(t, "online", th.MachineStatus(t, agents[0].MachineID))
	require.Equal(t, "online", th.MachineStatus(t, agents[2].MachineID))
}

// hub 重启 → 幽灵 online 被清理
func TestHubRestartClearsGhosts(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)
	ta.Connect(t, th)
	th.RequireStatus(t, ta.MachineID, "online")

	restarted := th.Restart(t)
	require.Equal(t, "offline", restarted.MachineStatus(t, ta.MachineID),
		"重启后不该留下幽灵 online")
}
```

- [ ] **Step 4: 支持「重启」的脚手架**

`TestHubRestartClearsGhosts` 需要在**同一个数据目录**上重新装配一次 hub。在 `internal/testsupport/hub.go` 里把数据目录记下来并加 `Restart`：

给 `TestHub` 加字段 `dataDir string`（`NewTestHub` 里从 `app.DataDir()` 取），并新增：

先把 `NewTestHub` 的主体抽成 `newTestHubAt(t *testing.T, seedDir string, opts ...func(*hub.Config)) *TestHub`（把原来的 `tests.NewTestApp(t.TempDir())` 换成 `tests.NewTestApp(seedDir)`），`NewTestHub` 变成：

```go
func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub {
	t.Helper()
	return newTestHubAt(t, t.TempDir(), opts...)
}
```

并在 `TestHub` 结构体里保存 `cfgFns []func(*hub.Config)`（`newTestHubAt` 里赋值）。然后新增：

```go
// Restart 在同一份数据上重新装配一个 hub，用于验证 hub 重启后的行为。
// 旧的 TestHub 在此之后不应再使用。
//
// 顺序要紧：tests.NewTestApp 会克隆传入的目录，而 App.Cleanup() 会把
// 当前数据目录整个删掉。所以先快照，再让旧实例退场，最后用快照重建。
func (h *TestHub) Restart(t *testing.T) *TestHub {
	t.Helper()

	snapshot := t.TempDir()
	require.NoError(t, copyDir(h.App.DataDir(), snapshot), "快照数据目录")

	h.Server.Close()
	h.App.Cleanup() // t.Cleanup 里还会再调一次，重复调用是安全的

	return newTestHubAt(t, snapshot, h.cfgFns...)
}

// copyDir 把 src 下的内容平铺复制到 dst（只需处理一层子目录，
// PocketBase 的数据目录就是这个形状）。
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		sp := filepath.Join(src, e.Name())
		dp := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(dp, 0o700); err != nil {
				return err
			}
			if err := copyDir(sp, dp); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(sp)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dp, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}
```

（`hub.go` 的 import 需要补 `os` 与 `path/filepath`。）

> **为什么要快照而不是直接复用目录：** `tests.NewTestApp(dir)` 把 `dir` 克隆到别处再工作，原目录只作种子。若把旧 app 的数据目录直接传进去，旧 app 的 `Cleanup()`（`t.Cleanup` 里注册过）会在测试结束时把它删掉——而它此刻正是新 app 的种子来源，删除时机不受控。快照一份最省心。

- [ ] **Step 5: 运行测试**

Run: `go test -tags=testing ./internal/testsupport/... -race -v`
Expected: PASS

若 `TestSecondConnectionKeepsOnline` 不稳定：`ta.Connect` 每次都建一条新连接，第二次调用触发的正是「同指纹二次连接」路径；确认 `Manager.Register` 是在**持锁期间**换掉 `m.conns[fp]`、在锁外关闭旧连接，否则旧连接的 `OnClose` 可能抢在替换之前跑完。

- [ ] **Step 6: 全量测试**

Run: `make test && make lint`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add hub internal/testsupport
git commit -m "feat: 装配连接管理器并跑通连接生命周期"
```

---

## 完成检查

spec §12.2「连接生命周期」六条用例逐条对照：

- [ ] 断开后 3 秒内重连 → 状态始终 online，不产生 disconnected 事件 —— `TestReconnectWithinGraceKeepsOnline` + `TestReconnectWithinGraceStaysOnline`
- [ ] 断开后 6 秒无重连 → offline —— `TestUnregisterWaitsForGraceBeforeOffline` + `TestDisconnectGoesOfflineAfterGrace`
- [ ] **同 fingerprint 二次连接 → 旧连接被踢，状态保持 online，不产生 disconnected 事件** —— `TestSecondConnectionEvictsFirstWithoutGoingOffline` + `TestSecondConnectionKeepsOnline`
- [ ] hub 重启 → 幽灵 online 被清理 —— `TestResetGhostsFlipsOnlineToOffline` + `TestHubRestartClearsGhosts`
- [ ] 删除机器 → 活跃连接被踢且收到 `CodeMachineRemoved` —— `TestOnRecordDeletedKicksActiveConnection` + `TestDeleteMachineKicksConnection`
- [ ] 三个 agent 并发接入 → 各自独立 —— `TestThreeMachinesAreIndependent` + `TestThreeAgentsConnectIndependently`

另外：

- [ ] `last_seen` 只在状态变化时写 —— `TestLastSeenWrittenOnStatusChangeOnly`
- [ ] `paused` 不被幽灵清理波及 —— `TestResetGhostsLeavesPausedAlone`
- [ ] `go list -deps ./hub/internal/machines | grep 'internal/ws'` 无输出（方向不能反）
