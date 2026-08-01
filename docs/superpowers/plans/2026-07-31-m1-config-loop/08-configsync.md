# 子计划 08 · 下发全链路

**前置**：01–07 全部
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`machines` 的按机器发消息、`ws` 的双向路由、`hub/internal/configsync`、`agent/internal/syncer`、hub 与 agent 两侧的装配、以及一条端到端集成测试。

**链路（spec §7.3）**

```
hub: 发布 / 收编 / 轮换 / 指派变更
  → configsync 算出该收到通知的机器集合
  → ConfigNotify（在线的立即推；离线的等它上线时在握手后补发）
agent:
  → ConfigPull → ConfigSnapshot → 比对 blobcache → BlobRequest / BlobData
  → 生成 plan → 快照 → 落盘 → 更新 state.json → ApplyAck
hub: 回执落库，assignment.state 更新，写 events
```

**离线补发不需要队列**：agent 上线后 hub 无条件发一次 `ConfigNotify`，拉到的快照若与 `state.json` 一致则 plan 全是 skip、零写入。**幂等是补发机制的替代品**。

本子计划任务较多，按「hub 发送能力 → ws 路由 → configsync → agent syncer → 装配 → 集成测试」六步走。

---

### Task 1: machines 按机器发消息

**Files:**
- Modify: `hub/internal/machines/registry.go`（`Conn` 接口加一个方法）
- Modify: `hub/internal/machines/manager.go`
- Test: `hub/internal/machines/manager_test.go`

**Interfaces:**
- Produces:
  ```go
  // machines.Conn 追加
  Send(kind protocol.Kind, payload any) error

  // *Manager 实现 configsync.Sender
  func (m *Manager) SendTo(machineID string, kind protocol.Kind, payload any) error
  func (m *Manager) Online(machineID string) bool
  func (m *Manager) OnOnline(fn func(machineID string))
  var ErrOffline = errors.New("machines: 机器不在线")
  ```

`*ws.Conn` 已经有 `Send`，结构性地就满足新接口，不必改 ws。注册表按 fingerprint 索引，因此 `SendTo` 要遍历——机队规模是「个人的几台机器」，线性扫描完全够用，加一张反向索引只会多一处要同步的状态。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/machines/manager_test.go`（沿用该文件已有的假连接类型，若假连接尚未实现 `Send`，给它加上并记录发出的消息）：

```go
func TestSendToRoutesByMachineID(t *testing.T) {
	app, m := newManager(t)
	id := seedMachine(t, app, "fp-send")

	c := &fakeConn{fingerprint: "fp-send", machineID: id}
	require.NoError(t, m.Register(c))

	require.True(t, m.Online(id))
	require.NoError(t, m.SendTo(id, protocol.KindConfigNotify,
		protocol.ConfigNotify{ConfigSetID: "set1", Reason: protocol.ReasonPublished}))

	require.Len(t, c.sent, 1)
	require.Equal(t, protocol.KindConfigNotify, c.sent[0].kind)
}

func TestSendToOfflineMachine(t *testing.T) {
	app, m := newManager(t)
	id := seedMachine(t, app, "fp-offline")
	require.False(t, m.Online(id))
	require.ErrorIs(t, m.SendTo(id, protocol.KindConfigNotify, protocol.ConfigNotify{}),
		machines.ErrOffline)
}

// 上线回调是离线补发的入口（spec §7.3）：agent 一上线就无条件发一次
// ConfigNotify，幂等保证它在无事可做时是零写入。
func TestOnOnlineFiresAfterRegister(t *testing.T) {
	app, m := newManager(t)
	id := seedMachine(t, app, "fp-cb")

	got := make(chan string, 1)
	m.OnOnline(func(machineID string) { got <- machineID })

	require.NoError(t, m.Register(&fakeConn{fingerprint: "fp-cb", machineID: id}))
	select {
	case v := <-got:
		require.Equal(t, id, v)
	case <-time.After(2 * time.Second):
		t.Fatal("上线回调未触发")
	}
}
```

> `newManager` / `seedMachine` / `fakeConn` 用该测试文件里已有的辅助；`fakeConn` 需要新增字段 `sent []struct{ kind protocol.Kind; payload any }` 与方法 `Send`，并在方法里加锁——回调跑在 `Register` 的调用者 goroutine 上，但断言在测试 goroutine 上。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/machines/ -run 'SendTo|OnOnline' -v`
Expected: FAIL，`m.SendTo undefined`

- [ ] **Step 3: 实现**

`registry.go` 的 `Conn` 接口追加：

```go
	// Send 发一条任意消息。M1 的下发链路走它（ConfigNotify / ConfigSnapshot /
	// BlobData / DriftCommand / CollectRequest）。
	Send(kind protocol.Kind, payload any) error
```

`NopRegistry` 不受影响（它实现的是 `Registry` 不是 `Conn`）；但 `machines` 包内的测试假连接要补上 `Send`。

`manager.go` 追加：

```go
// ErrOffline 表示目标机器当前没有活跃连接。
//
// 这不是错误路径的终点：下发是「hub 发信号、agent 主动拉」，
// 离线的机器会在上线后由 OnOnline 回调补发（spec §7.3）。
var ErrOffline = errors.New("machines: 机器不在线")

// SendTo 按机器 id 找到当前连接并发一条消息。
//
// 注册表按 fingerprint 索引，这里线性扫描：机队规模是「个人的几台机器」，
// 加一张反向索引只会多一处要同步的状态。
func (m *Manager) SendTo(machineID string, kind protocol.Kind, payload any) error {
	m.mu.Lock()
	var target Conn
	for _, c := range m.conns {
		if c.MachineID() == machineID {
			target = c
			break
		}
	}
	m.mu.Unlock()

	if target == nil {
		return fmt.Errorf("%w: %s", ErrOffline, machineID)
	}
	return target.Send(kind, payload)
}

func (m *Manager) Online(machineID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		if c.MachineID() == machineID {
			return true
		}
	}
	return false
}

// OnOnline 登记「机器上线」回调。configsync 用它补发 ConfigNotify。
// 只在装配期调用，因此不做并发保护之外的处理。
func (m *Manager) OnOnline(fn func(machineID string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onOnline = append(m.onOnline, fn)
}
```

`Manager` 结构体加字段 `onOnline []func(string)`。在 `Register` 成功之后（`setStatus` 与事件写完之后）触发：

```go
	// 回调放在最后：它会去查库读指派，必须等状态写完。
	// 起 goroutine 是因为回调里要发 WS 消息，而 Register 跑在读循环上，
	// 阻塞它会卡住这条连接的后续收信。
	m.mu.Lock()
	callbacks := make([]func(string), len(m.onOnline))
	copy(callbacks, m.onOnline)
	m.mu.Unlock()
	for _, fn := range callbacks {
		go fn(c.MachineID())
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/machines/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/machines/
git commit -m "feat: 按机器 id 发消息与上线回调"
```

---

### Task 2: ws 把 agent 来的消息路由到业务层

**Files:**
- Modify: `hub/internal/ws/handler.go`
- Test: `hub/internal/ws/ws_test.go`

**Interfaces:**
- Produces:
  ```go
  type AgentMessages interface {
      Pull(machineID string, p protocol.ConfigPull)
      BlobRequest(machineID string, r protocol.BlobRequest)
      ApplyAck(machineID string, a protocol.ApplyAck)
      DriftReport(machineID string, d protocol.DriftReport)
      CollectResult(machineID string, c protocol.CollectResult)
  }
  // Deps 追加字段：Agent AgentMessages
  ```

接口定义在 `ws` 侧、由 `configsync` 反向满足，是为了避免 `ws` 与 `configsync` 互相 import：`configsync` 需要 `Sender`（由 `machines.Manager` 提供），`ws` 需要 `AgentMessages`。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/ws/ws_test.go`：

```go
type recordingAgent struct {
	mu   sync.Mutex
	pull []string
	acks []protocol.ApplyAck
}

func (r *recordingAgent) Pull(machineID string, _ protocol.ConfigPull) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pull = append(r.pull, machineID)
}
func (r *recordingAgent) BlobRequest(string, protocol.BlobRequest) {}
func (r *recordingAgent) ApplyAck(_ string, a protocol.ApplyAck) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.acks = append(r.acks, a)
}
func (r *recordingAgent) DriftReport(string, protocol.DriftReport)     {}
func (r *recordingAgent) CollectResult(string, protocol.CollectResult) {}

func (r *recordingAgent) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pull), len(r.acks)
}

// 已认证连接上收到 M1 的消息必须转给业务层，而不是落进
// 「已认证连接收到意外消息」的 default 分支。
func TestAuthenticatedMessagesAreRouted(t *testing.T) {
	rec := &recordingAgent{}
	// 用该测试文件已有的方式起一个握手完成的连接，Deps 里带上 Agent: rec
	// （见文件内既有的 newTestHandler / dialAndHandshake 辅助）。
	h, client := newAuthedPair(t, func(d *ws.Deps) { d.Agent = rec })
	defer client.Close()

	require.NoError(t, client.send(protocol.KindConfigPull, protocol.ConfigPull{Have: "rev1"}))
	require.NoError(t, client.send(protocol.KindApplyAck, protocol.ApplyAck{RevisionID: "rev1", OK: true}))

	require.Eventually(t, func() bool {
		p, a := rec.counts()
		return p == 1 && a == 1
	}, 2*time.Second, 5*time.Millisecond)
	_ = h
}

// Agent 为 nil 时不能 panic：只关心握手的测试不必装配业务层。
func TestRoutingIsNoopWhenAgentIsNil(t *testing.T) {
	h, client := newAuthedPair(t, nil)
	defer client.Close()
	require.NoError(t, client.send(protocol.KindConfigPull, protocol.ConfigPull{}))
	require.Eventually(t, func() bool { return h.LiveCount() == 1 },
		2*time.Second, 5*time.Millisecond, "连接不该因此断开")
}
```

> `newAuthedPair` 与 `client.send`、`h.LiveCount()` 若尚不存在，按该测试文件里既有的握手辅助补出来（`LiveCount` 是 `Handler` 上一个只读 `len(h.live)` 的小方法，加锁）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/ws/ -run Routed -v`
Expected: FAIL，`d.Agent undefined`

- [ ] **Step 3: 实现**

`Deps` 追加字段与接口定义：

```go
// AgentMessages 是 ws 对业务层的全部需求。由 *configsync.Service 实现。
//
// 接口定义在这一侧、由业务层反向满足，是为了避免 ws 与 configsync 互相
// import：configsync 要 Sender（machines.Manager 提供），ws 要 AgentMessages。
//
// 全部方法都不返回错误：ws 是搬运工，处理失败该由业务层自己记日志与写事件，
// 让它冒泡到读循环只会诱使人在这里写 WriteClose。
type AgentMessages interface {
	Pull(machineID string, p protocol.ConfigPull)
	BlobRequest(machineID string, r protocol.BlobRequest)
	ApplyAck(machineID string, a protocol.ApplyAck)
	DriftReport(machineID string, d protocol.DriftReport)
	CollectResult(machineID string, c protocol.CollectResult)
}
```

`handleAuthenticated` 的 switch 追加分支：

```go
	case protocol.KindConfigPull:
		dispatch(h, c, env, func(p protocol.ConfigPull) { h.d.Agent.Pull(c.MachineID(), p) })
	case protocol.KindBlobRequest:
		dispatch(h, c, env, func(r protocol.BlobRequest) { h.d.Agent.BlobRequest(c.MachineID(), r) })
	case protocol.KindApplyAck:
		dispatch(h, c, env, func(a protocol.ApplyAck) { h.d.Agent.ApplyAck(c.MachineID(), a) })
	case protocol.KindDriftReport:
		dispatch(h, c, env, func(d protocol.DriftReport) { h.d.Agent.DriftReport(c.MachineID(), d) })
	case protocol.KindCollectResult:
		dispatch(h, c, env, func(r protocol.CollectResult) { h.d.Agent.CollectResult(c.MachineID(), r) })
```

并在文件末尾加分派辅助：

```go
// dispatch 解 payload 并交给业务层。Agent 为 nil 时静默丢弃——
// 只关心握手的测试不必装配业务层，留一条会 nil deref 的路径不如不走。
func dispatch[T any](h *Handler, c *Conn, env protocol.Envelope, fn func(T)) {
	if h.d.Agent == nil {
		h.log.Debug("未装配业务层，忽略消息", "kind", env.Kind.String())
		return
	}
	v, err := protocol.DecodePayload[T](env)
	if err != nil {
		h.log.Warn("解析消息失败", "kind", env.Kind.String(),
			"fingerprint", c.Fingerprint(), "error", err)
		return
	}
	fn(v)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/ws/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/ws/
git commit -m "feat: ws 把 agent 的 M1 消息路由到业务层"
```

---

### Task 3: configsync —— 快照组装与通知

**Files:**
- Create: `hub/internal/configsync/service.go`
- Create: `hub/internal/configsync/notify.go`
- Test: `hub/internal/configsync/service_test.go`

**Interfaces:**
- Consumes: `blobs` / `configsets` / `revisions` / `credentials` / `events`、`machines.Manager`（作 `Sender`）
- Produces: `Sender` / `Deps` / `Service` / `NewService` / `NotifyConfigSet` / `NotifyMachine` / `NotifyCredential` / `Snapshot` / `Pull` / `BlobRequest` / `ApplyAck` / `OnMachineOnline`（逐字符见 00-overview；`DriftReport` 与 `CollectResult` 在本子计划先做成空实现，13 与 09 分别填上）

**关键规则**

- `ConfigSnapshot.Credentials` **只含该 Revision 引用到的凭据**（spec §5.3）：最小权限，也是一条实际防线——一台被攻陷的机器不该因为连着 hub 就拿到所有订阅的 key。
- `paused` 的配置集不发通知；`survey` 模式的快照 `Mode = protocol.ModeSurvey`。
- `BlobData` 一条一个 blob；hub 侧找不到就发 `Missing: true`，agent 据此中止 apply。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/configsync/service_test.go`：

```go
package configsync_test

import (
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

type sentMsg struct {
	machine string
	kind    protocol.Kind
	payload any
}

type fakeSender struct {
	mu     sync.Mutex
	sent   []sentMsg
	online map[string]bool
}

func (f *fakeSender) SendTo(machineID string, kind protocol.Kind, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{machineID, kind, payload})
	return nil
}
func (f *fakeSender) Online(machineID string) bool { return f.online[machineID] }

func (f *fakeSender) of(kind protocol.Kind) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMsg
	for _, m := range f.sent {
		if m.kind == kind {
			out = append(out, m)
		}
	}
	return out
}

type rig struct {
	app    *tests.TestApp
	sets   *configsets.Service
	revs   *revisions.Service
	creds  *credentials.Store
	sender *fakeSender
	svc    *configsync.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	ev := events.NewWriter(app)
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	r := &rig{
		app:    app,
		sets:   configsets.NewService(app, b, ev),
		revs:   revisions.NewService(app, b, ev),
		creds:  credentials.NewStore(app, key, ev),
		sender: &fakeSender{online: map[string]bool{}},
	}
	r.svc = configsync.NewService(configsync.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Creds: r.creds,
		Events: ev, Sender: r.sender,
	})
	return r
}

func (r *rig) machine(t *testing.T, fp string) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	rec := core.NewRecord(c)
	rec.Set("fingerprint", fp)
	rec.Set("pub_key", "pk-"+fp)
	rec.Set("status", "online")
	require.NoError(t, r.app.Save(rec))
	r.sender.online[rec.Id] = true
	return rec.Id
}

// 只发该版本引用到的凭据（spec §5.3）。
func TestSnapshotCarriesOnlyReferencedCredentials(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp1")

	_, err := r.creds.Create("used_key", "sk-used-1234", "")
	require.NoError(t, err)
	_, err = r.creds.Create("other_key", "sk-other-9999", "")
	require.NoError(t, err)

	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"K":"{{cred.used_key}}","W":"{{var.ws}}"}`), 0o600, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	require.NoError(t, r.creds.SetVariable(m, "ws", "main"))
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"used_key": "sk-used-1234"}, snap.Credentials,
		"不该把整个凭据库发给一台机器")
	require.Equal(t, map[string]string{"ws": "main"}, snap.Variables)
	require.Equal(t, protocol.ModeApply, snap.Mode)
	require.NotEmpty(t, snap.Manifest)
	require.Len(t, snap.Files, 1)
	require.Equal(t, protocol.Checksum(snap.Files), snap.Checksum)
}

func TestSnapshotSurveyMode(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp2")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "survey")
	require.NoError(t, err)

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	require.Equal(t, protocol.ModeSurvey, snap.Mode)
}

func TestNotifyConfigSetReachesAllAssignedMachines(t *testing.T) {
	r := newRig(t)
	m1 := r.machine(t, "fp3")
	m2 := r.machine(t, "fp4")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m1, set.Id, "apply")
	require.NoError(t, err)
	_, err = r.sets.Assign(m2, set.Id, "survey")
	require.NoError(t, err)

	require.NoError(t, r.svc.NotifyConfigSet(set.Id, rev.Id, protocol.ReasonPublished))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, sent, 2)
	for _, s := range sent {
		n := s.payload.(protocol.ConfigNotify)
		require.Equal(t, set.Id, n.ConfigSetID)
		require.Equal(t, rev.Id, n.RevisionID)
		require.Equal(t, protocol.ReasonPublished, n.Reason)
	}
}

// 暂停下发的配置集不发通知（产品 §4.2）。
func TestNotifySkipsPausedConfigSet(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp5")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	set.Set("paused", true)
	require.NoError(t, r.app.Save(set))

	require.NoError(t, r.svc.NotifyConfigSet(set.Id, rev.Id, protocol.ReasonPublished))
	require.Empty(t, r.sender.of(protocol.KindConfigNotify))
}

// 轮换复用同一条消息，且不带 RevisionID（spec §5.1）。
func TestNotifyCredentialSendsRotatedNotify(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp6")
	_, err := r.creds.Create("k", "sk-value-1234", "")
	require.NoError(t, err)

	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte(`{{cred.k}}`), 0o600, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	require.NoError(t, r.svc.NotifyCredential("k"))
	sent := r.sender.of(protocol.KindConfigNotify)
	require.Len(t, sent, 1)
	n := sent[0].payload.(protocol.ConfigNotify)
	require.Empty(t, n.RevisionID, "轮换不产生新版本")
	require.Equal(t, protocol.ReasonRotated, n.Reason)
}

// 引用不到这枚凭据的配置集，其机器不该被打扰。
func TestNotifyCredentialSkipsUnrelatedMachines(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp7")
	_, err := r.creds.Create("k", "sk-value-1234", "")
	require.NoError(t, err)

	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("没有占位符"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	require.NoError(t, r.svc.NotifyCredential("k"))
	require.Empty(t, r.sender.of(protocol.KindConfigNotify))
}

func TestPullSendsSnapshot(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp8")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.Pull(m, protocol.ConfigPull{})
	sent := r.sender.of(protocol.KindConfigSnapshot)
	require.Len(t, sent, 1)
}

func TestBlobRequestSendsOneMessagePerBlob(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp9")
	b := blobs.New(r.app)
	h1, err := b.Put([]byte("一"))
	require.NoError(t, err)
	h2, err := b.Put([]byte("二"))
	require.NoError(t, err)

	r.svc.BlobRequest(m, protocol.BlobRequest{Hashes: []string{h1, h2, "不存在的hash"}})

	sent := r.sender.of(protocol.KindBlobData)
	require.Len(t, sent, 3, "一条一个 blob")
	var missing int
	for _, s := range sent {
		if s.payload.(protocol.BlobData).Missing {
			missing++
		}
	}
	require.Equal(t, 1, missing, "找不到的要明确告知，让 agent 中止 apply")
}

func TestApplyAckUpdatesAssignment(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp10")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.ApplyAck(m, protocol.ApplyAck{RevisionID: rev.Id, OK: true, DurationMs: 12})

	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "aligned", a.GetString("state"))
	require.Equal(t, rev.Id, a.GetString("applied_revision"))
	require.Empty(t, a.GetString("last_error"))
}

func TestApplyAckFailureAndDegraded(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp11")
	set, err := r.sets.Create("s", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(m, set.Id, "apply")
	require.NoError(t, err)

	r.svc.ApplyAck(m, protocol.ApplyAck{
		RevisionID: rev.Id, OK: false, RolledBack: true, Error: "permission denied",
	})
	a, err := r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "failed", a.GetString("state"))
	require.Contains(t, a.GetString("last_error"), "permission denied")

	// 回滚失败 → degraded
	r.svc.ApplyAck(m, protocol.ApplyAck{
		RevisionID: rev.Id, OK: false, RolledBack: false, Error: "回滚也失败了",
	})
	a, err = r.sets.Assignment(m)
	require.NoError(t, err)
	require.Equal(t, "degraded", a.GetString("state"))

	kinds := map[string]bool{}
	recs, err := r.app.FindAllRecords("events")
	require.NoError(t, err)
	for _, rec := range recs {
		kinds[rec.GetString("kind")] = true
	}
	require.True(t, kinds[events.KindApplyFailed])
	require.True(t, kinds[events.KindApplyRollbackFailed])
}

// 未指派的机器拉取不该报错崩掉，只是没有快照可给。
func TestPullOfUnassignedMachineIsQuiet(t *testing.T) {
	r := newRig(t)
	m := r.machine(t, "fp12")
	r.svc.Pull(m, protocol.ConfigPull{})
	require.Empty(t, r.sender.of(protocol.KindConfigSnapshot))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsync/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现快照组装**

Create `hub/internal/configsync/service.go`：

```go
// Package configsync 是下发编排：谁该收到通知、快照怎么组装、回执怎么落库。
//
// 形状是 M0 就定下的（产品 §3.3）：hub 只发信号，agent 主动拉。M1 保持它，
// 多一个理由——凭据轮换可以复用同一条 ConfigNotify，不产生新 Revision
// （spec §5.1）。
package configsync

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

// Sender 是「往某台机器发一条消息」的能力，由 *machines.Manager 实现。
type Sender interface {
	SendTo(machineID string, kind protocol.Kind, payload any) error
	Online(machineID string) bool
}

type Deps struct {
	App    core.App
	Blobs  *blobs.Store
	Sets   *configsets.Service
	Revs   *revisions.Service
	Creds  *credentials.Store
	Events *events.Writer
	Sender Sender
	Logger *slog.Logger
}

type Service struct {
	d   Deps
	log *slog.Logger
}

func NewService(d Deps) *Service {
	log := d.Logger
	if log == nil {
		if d.App != nil {
			log = d.App.Logger()
		} else {
			log = slog.Default()
		}
	}
	return &Service{d: d, log: log}
}

// Snapshot 组装一台机器当前该有的完整快照。
func (s *Service) Snapshot(machineID string) (protocol.ConfigSnapshot, error) {
	var snap protocol.ConfigSnapshot

	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return snap, err
	}
	setID := assign.GetString("config_set")

	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return snap, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return snap, err
	}

	var refs configsets.Refs
	if err := head.UnmarshalJSONField("refs", &refs); err != nil {
		return snap, fmt.Errorf("configsync: 解析 refs: %w", err)
	}
	// 只发这个 Revision 实际引用到的凭据（spec §5.3）：最小权限，
	// 也是一条实际的防线——一台被攻陷的机器不该因为连着 hub
	// 就拿到所有订阅的 key。
	creds, err := s.d.Creds.Values(refs.Creds)
	if err != nil {
		return snap, err
	}
	allVars, err := s.d.Creds.MachineVariables(machineID)
	if err != nil {
		return snap, err
	}
	vars := make(map[string]string, len(refs.Vars))
	for _, name := range refs.Vars {
		if v, ok := allVars[name]; ok {
			vars[name] = v
		}
	}

	ignore, err := s.ignorePaths(machineID)
	if err != nil {
		return snap, err
	}

	mode := protocol.ModeApply
	if assign.GetString("mode") == configsets.ModeSurvey {
		mode = protocol.ModeSurvey
	}

	return protocol.ConfigSnapshot{
		ConfigSetID: setID,
		RevisionID:  head.Id,
		Seq:         uint32(head.GetInt("seq")),
		Manifest:    []byte(head.GetString("manifest")),
		Files:       files,
		Checksum:    head.GetString("checksum"),
		Credentials: creds,
		Variables:   vars,
		IgnorePaths: ignore,
		Mode:        mode,
	}, nil
}

// ignorePaths 汇总全局规则与该机器的规则（spec §8.4）。
func (s *Service) ignorePaths(machineID string) ([]string, error) {
	recs, err := s.d.App.FindRecordsByFilter("ignore_rules",
		"machine = '' || machine = {:m}", "path", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("configsync: 读取忽略规则: %w", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("path"))
	}
	return out, nil
}

// —— ws.AgentMessages 实现 ——

func (s *Service) Pull(machineID string, p protocol.ConfigPull) {
	snap, err := s.Snapshot(machineID)
	if err != nil {
		if errors.Is(err, configsets.ErrNoAssignment) {
			s.log.Debug("机器未指派配置集，忽略拉取", "machine", machineID)
			return
		}
		s.log.Warn("组装快照失败", "machine", machineID, "error", err)
		return
	}
	s.log.Info("下发快照", "machine", machineID,
		"revision", snap.RevisionID, "have", p.Have, "files", len(snap.Files))
	if err := s.d.Sender.SendTo(machineID, protocol.KindConfigSnapshot, snap); err != nil {
		s.log.Warn("下发快照失败", "machine", machineID, "error", err)
	}
}

// BlobRequest 一条消息回一个 blob（spec §5.4）：512 KiB 的单文件上限
// 保证它不会越过 1 MiB 的 WS 帧上限。
func (s *Service) BlobRequest(machineID string, r protocol.BlobRequest) {
	for _, h := range r.Hashes {
		content, err := s.d.Blobs.Get(h)
		if err != nil {
			s.log.Warn("agent 索取的 blob 不存在", "machine", machineID, "hash", h)
			_ = s.d.Sender.SendTo(machineID, protocol.KindBlobData,
				protocol.BlobData{Hash: h, Missing: true})
			continue
		}
		if err := s.d.Sender.SendTo(machineID, protocol.KindBlobData,
			protocol.BlobData{Hash: h, Content: content}); err != nil {
			s.log.Warn("发送 blob 失败", "machine", machineID, "hash", h, "error", err)
			return
		}
	}
}

// DriftReport 在子计划 13 接上 drift.Service。
func (s *Service) DriftReport(machineID string, _ protocol.DriftReport) {
	s.log.Debug("收到漂移上报（尚未接入处理）", "machine", machineID)
}

// CollectResult 在子计划 09 接上 importer.Service。
func (s *Service) CollectResult(machineID string, _ protocol.CollectResult) {
	s.log.Debug("收到采集结果（尚未接入处理）", "machine", machineID)
}
```

- [ ] **Step 4: 实现通知与回执**

Create `hub/internal/configsync/notify.go`：

```go
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
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsync/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add hub/internal/configsync/
git commit -m "feat: 下发编排——快照组装、通知与回执"
```

---

### Task 4: agent 侧的编排者 syncer

**Files:**
- Create: `agent/internal/syncer/syncer.go`
- Test: `agent/internal/syncer/syncer_test.go`

**Interfaces:**
- Consumes: `state` / `secrets` / `blobcache` / `applier` / `internal/manifest` / `protocol`
- Produces: `Deps` / `Syncer` / `New` / `Handle` / `SyncNow` / `State`（逐字符见 00-overview；`Report` 在子计划 13 用到，这里先建好）

**为什么单独成包（偏离记录 #5）**：`ConfigNotify → Pull → 补 blob → apply → Ack` 是一台状态机，且要跨消息保存「还在等哪些 hash」。挂在 `conn` 里会让连接层长出业务，挂在 `applier` 里会让它依赖网络。

**状态机**

```
收到 ConfigNotify           → 发 ConfigPull
收到 ConfigSnapshot         → 存为 pending；算缺失 hash
                              缺失为空 → 立即 apply
                              否则 → 发 BlobRequest，等
收到 BlobData               → 落 blobcache；全齐了 → apply
apply 完                    → 写 state.json + secrets.json → 发 ApplyAck
```

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/syncer/syncer_test.go`：

```go
package syncer_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/syncer"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type outbox struct {
	mu   sync.Mutex
	msgs []struct {
		kind    protocol.Kind
		payload any
	}
}

func (o *outbox) send(kind protocol.Kind, payload any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.msgs = append(o.msgs, struct {
		kind    protocol.Kind
		payload any
	}{kind, payload})
	return nil
}

func (o *outbox) of(kind protocol.Kind) []any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []any
	for _, m := range o.msgs {
		if m.kind == kind {
			out = append(out, m.payload)
		}
	}
	return out
}

type rig struct {
	dir  string
	home string
	out  *outbox
	s    *syncer.Syncer
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{dir: t.TempDir(), home: t.TempDir(), out: &outbox{}}
	s, err := syncer.New(syncer.Deps{
		Dir:         r.dir,
		ManagedHome: r.home,
		Clock:       clock.NewFake(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)),
		Send:        r.out.send,
		MachineName: "主力",
	})
	require.NoError(t, err)
	r.s = s
	return r
}

func envelope(t *testing.T, kind protocol.Kind, payload any) protocol.Envelope {
	t.Helper()
	b, err := protocol.Encode(kind, nil, payload)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}

func snapshotOf(t *testing.T, files map[string]string) (protocol.ConfigSnapshot, map[string][]byte) {
	t.Helper()
	mj, err := manifest.Default().JSON()
	require.NoError(t, err)
	snap := protocol.ConfigSnapshot{
		ConfigSetID: "set1", RevisionID: "rev1", Seq: 1, Manifest: mj,
	}
	blobs := map[string][]byte{}
	for rel, content := range files {
		h := blobcache.Hash([]byte(content))
		snap.Files = append(snap.Files, protocol.FileEntry{
			Path: rel, Hash: h, Size: uint32(len(content)), Mode: 0o644,
		})
		blobs[h] = []byte(content)
	}
	snap.Checksum = protocol.Checksum(snap.Files)
	return snap, blobs
}

func TestNotifyTriggersPull(t *testing.T) {
	r := newRig(t)
	r.s.Handle(envelope(t, protocol.KindConfigNotify,
		protocol.ConfigNotify{ConfigSetID: "set1", RevisionID: "rev1"}))

	pulls := r.out.of(protocol.KindConfigPull)
	require.Len(t, pulls, 1)
}

// 本地什么都没有 → 先要 blob，不能直接 apply。
func TestSnapshotRequestsMissingBlobs(t *testing.T) {
	r := newRig(t)
	snap, _ := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))

	reqs := r.out.of(protocol.KindBlobRequest)
	require.Len(t, reqs, 1)
	require.Equal(t, []string{snap.Files[0].Hash}, reqs[0].(protocol.BlobRequest).Hashes)
	require.Empty(t, r.out.of(protocol.KindApplyAck), "内容没齐不能 apply")
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
}

func TestBlobDataCompletesApply(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))

	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 1)
	require.True(t, acks[0].(protocol.ApplyAck).OK)

	got, err := os.ReadFile(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, "内容", string(got))

	st, err := state.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, "rev1", st.Revision)
	require.Equal(t, snap.Checksum, st.Checksum)
}

// 缓存命中就不再向 hub 要内容。
func TestSnapshotUsesCacheWhenAvailable(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	cache := blobcache.New(r.dir)
	for _, content := range blobs {
		_, err := cache.Put(content)
		require.NoError(t, err)
	}

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	require.Empty(t, r.out.of(protocol.KindBlobRequest), "本地已有就不该再要")
	require.Len(t, r.out.of(protocol.KindApplyAck), 1)
}

// 凭据与变量随快照落进 secrets.json，供离线自愈与还原使用（spec §6.2）。
func TestSecretsArePersisted(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/settings.json": `{"K":"{{cred.k}}"}`})
	snap.Credentials = map[string]string{"k": "sk-real-1234"}
	snap.Variables = map[string]string{"ws": "main"}

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}

	sec, err := secrets.Load(r.dir)
	require.NoError(t, err)
	require.Equal(t, "sk-real-1234", sec.Creds["k"])
	require.Equal(t, "main", sec.Vars["ws"])
	require.Equal(t, "主力", sec.Machine["name"])
	require.NotEmpty(t, sec.Machine["hostname"])
	require.NotEmpty(t, sec.Machine["os"])
	require.NotEmpty(t, sec.Machine["arch"])

	info, err := os.Stat(secrets.Path(r.dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// hub 说找不到 blob → 中止本次 apply，不写任何文件（spec §5.2）。
func TestMissingBlobAbortsApply(t *testing.T) {
	r := newRig(t)
	snap, _ := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	r.s.Handle(envelope(t, protocol.KindBlobData,
		protocol.BlobData{Hash: snap.Files[0].Hash, Missing: true}))

	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 1)
	ack := acks[0].(protocol.ApplyAck)
	require.False(t, ack.OK)
	require.Contains(t, ack.Error, "内容")
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
}

// survey 模式不写盘（spec §7.6）。落盘的部分在子计划 15 补全对账上报。
func TestSurveyModeDoesNotWrite(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	snap.Mode = protocol.ModeSurvey
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	require.NoFileExists(t, filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.Empty(t, r.out.of(protocol.KindApplyAck), "survey 不产生 apply 回执")
}

// 重复下发同一版本：第二次零写入（spec §7.3 的幂等）。
func TestRepeatedSnapshotIsIdempotent(t *testing.T) {
	r := newRig(t)
	snap, blobs := snapshotOf(t, map[string]string{".claude/CLAUDE.md": "内容"})
	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	for h, content := range blobs {
		r.s.Handle(envelope(t, protocol.KindBlobData, protocol.BlobData{Hash: h, Content: content}))
	}
	first, err := os.Stat(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)

	r.s.Handle(envelope(t, protocol.KindConfigSnapshot, snap))
	acks := r.out.of(protocol.KindApplyAck)
	require.Len(t, acks, 2)
	require.True(t, acks[1].(protocol.ApplyAck).OK)

	after, err := os.Stat(filepath.Join(r.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, first.ModTime(), after.ModTime(), "第二次不该碰文件")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/syncer/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/syncer/syncer.go`：

```go
// Package syncer 是 agent 侧配置闭环的编排者。
//
// 单独成包的理由：ConfigNotify → Pull → 补 blob → apply → Ack 是一台状态机，
// 且要跨消息保存「还在等哪些 hash」。挂在 conn 里会让连接层长出业务；
// 挂在 applier 里会让它依赖网络。
package syncer

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type Deps struct {
	Dir         string // agent 目录（~/.orciny）
	ManagedHome string
	Clock       clock.Clock
	Logger      *slog.Logger
	FS          applier.FS
	Send        func(kind protocol.Kind, payload any) error
	MachineName string // 面板上的备注名，供 {{machine.name}}
}

type Syncer struct {
	d     Deps
	log   *slog.Logger
	cache *blobcache.Cache
	app   *applier.Applier

	mu      sync.Mutex
	st      *state.State
	pending *protocol.ConfigSnapshot // 正在等 blob 的快照
	waiting map[string]bool          // 还没到的 hash
	content map[string][]byte        // 本次已凑齐的内容
}

func New(d Deps) (*Syncer, error) {
	if d.Clock == nil {
		d.Clock = clock.System()
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.FS == nil {
		d.FS = applier.OSFS()
	}
	if d.ManagedHome == "" {
		return nil, fmt.Errorf("syncer: ManagedHome 不能为空")
	}

	s := &Syncer{
		d:     d,
		log:   d.Logger,
		cache: blobcache.New(d.Dir),
		app: applier.New(applier.Options{
			Dir: d.Dir, ManagedHome: d.ManagedHome,
			Clock: d.Clock, Logger: d.Logger, FS: d.FS,
		}),
	}
	// state.json 缺失或损坏都不是错误：spec §7.6 的自愈路径靠它触发。
	if st, err := state.Load(d.Dir); err == nil {
		s.st = st
	}
	return s, nil
}

// State 返回当前本地状态的副本视图（CLI 与 watcher 用）。
func (s *Syncer) State() *state.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Handle 分派一条来自 hub 的消息。
func (s *Syncer) Handle(env protocol.Envelope) {
	switch env.Kind {
	case protocol.KindConfigNotify:
		n, err := protocol.DecodePayload[protocol.ConfigNotify](env)
		if err != nil {
			s.log.Warn("解析 ConfigNotify 失败", "error", err)
			return
		}
		s.log.Info("收到配置变更通知",
			"config_set", n.ConfigSetID, "revision", n.RevisionID, "reason", n.Reason)
		s.pull()

	case protocol.KindConfigSnapshot:
		snap, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
		if err != nil {
			s.log.Warn("解析 ConfigSnapshot 失败", "error", err)
			return
		}
		s.onSnapshot(snap)

	case protocol.KindBlobData:
		bd, err := protocol.DecodePayload[protocol.BlobData](env)
		if err != nil {
			s.log.Warn("解析 BlobData 失败", "error", err)
			return
		}
		s.onBlob(bd)
	}
}

// SyncNow 主动拉一次（CLI 的 orciny-agent sync）。
func (s *Syncer) SyncNow() error { return s.pull() }

func (s *Syncer) pull() error {
	have := ""
	s.mu.Lock()
	if s.st != nil {
		have = s.st.Revision
	}
	s.mu.Unlock()
	return s.d.Send(protocol.KindConfigPull, protocol.ConfigPull{Have: have})
}

func (s *Syncer) onSnapshot(snap protocol.ConfigSnapshot) {
	// 凭据与变量先落盘再说：还原、离线自愈、对账都依赖它，
	// 而且它与本次 apply 是否成功无关（spec §6.2）。
	if err := s.saveSecrets(snap); err != nil {
		s.log.Warn("保存凭据缓存失败", "error", err)
	}

	var need []string
	for _, f := range snap.Files {
		if !s.cache.Has(f.Hash) {
			need = append(need, f.Hash)
		}
	}
	need = s.cache.Missing(need) // 顺带去重

	s.mu.Lock()
	s.pending = &snap
	s.content = map[string][]byte{}
	s.waiting = map[string]bool{}
	for _, h := range need {
		s.waiting[h] = true
	}
	ready := len(s.waiting) == 0
	s.mu.Unlock()

	if ready {
		s.applyPending()
		return
	}
	if err := s.d.Send(protocol.KindBlobRequest, protocol.BlobRequest{Hashes: need}); err != nil {
		s.log.Warn("索取内容失败", "error", err)
	}
}

func (s *Syncer) onBlob(bd protocol.BlobData) {
	s.mu.Lock()
	if s.pending == nil || !s.waiting[bd.Hash] {
		s.mu.Unlock()
		return // 迟到的或不相干的，忽略
	}
	if bd.Missing {
		rev := s.pending.RevisionID
		s.pending, s.waiting, s.content = nil, nil, nil
		s.mu.Unlock()
		// hub 都找不到内容，本次彻底做不成——明确报回去，
		// 绝不带着半份清单去写盘（spec §7.4「宁可不动」）。
		s.log.Error("hub 找不到所需内容，已中止本次 apply", "hash", bd.Hash)
		_ = s.d.Send(protocol.KindApplyAck, protocol.ApplyAck{
			RevisionID: rev,
			OK:         false,
			Error:      fmt.Sprintf("hub 缺少内容 %s，未做任何改动", bd.Hash),
		})
		return
	}
	s.mu.Unlock()

	// 落盘前自己再算一遍 hash：这是 agent 少数能自我保护的地方。
	if err := s.cache.PutHash(bd.Hash, bd.Content); err != nil {
		s.log.Error("收到的内容与 hash 不符，已丢弃", "hash", bd.Hash, "error", err)
		return
	}

	s.mu.Lock()
	delete(s.waiting, bd.Hash)
	s.content[bd.Hash] = bd.Content
	ready := len(s.waiting) == 0
	s.mu.Unlock()

	if ready {
		s.applyPending()
	}
}

func (s *Syncer) applyPending() {
	s.mu.Lock()
	if s.pending == nil {
		s.mu.Unlock()
		return
	}
	snap := *s.pending
	st := s.st
	s.pending, s.waiting = nil, nil
	s.mu.Unlock()

	// survey 模式不写盘（spec §7.6）：只做一次全量对账，
	// 由子计划 15 接上上报路径。
	if snap.Mode == protocol.ModeSurvey {
		s.log.Info("survey 模式，不写盘", "revision", snap.RevisionID)
		return
	}

	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	content, err := s.contentFor(snap)
	if err != nil {
		s.log.Error("凑齐内容失败", "error", err)
		_ = s.d.Send(protocol.KindApplyAck, protocol.ApplyAck{
			RevisionID: snap.RevisionID, OK: false, Error: err.Error(),
		})
		return
	}

	plan, err := applier.BuildPlan(snap, st, content, sec.Lookup)
	if err != nil {
		s.log.Error("生成 apply 计划失败", "error", err)
		_ = s.d.Send(protocol.KindApplyAck, protocol.ApplyAck{
			RevisionID: snap.RevisionID, OK: false, Error: err.Error(),
		})
		return
	}

	ack, next := s.app.Apply(snap, plan, st)
	for _, sk := range plan.Skip {
		ack.Results = append(ack.Results, protocol.ApplyResult{
			Path: sk.Rel, Action: protocol.ActionSkip, Error: sk.Reason,
		})
	}
	if err := state.Save(s.d.Dir, next); err != nil {
		s.log.Error("写 state.json 失败", "error", err)
	}
	s.mu.Lock()
	s.st = next
	s.mu.Unlock()

	s.log.Info("apply 完成", "revision", snap.RevisionID,
		"ok", ack.OK, "writes", plan.Writes(), "rolled_back", ack.RolledBack)
	if err := s.d.Send(protocol.KindApplyAck, ack); err != nil {
		s.log.Warn("上报回执失败", "error", err)
	}
}

// contentFor 从本次收到的内容与本地缓存里凑齐清单需要的全部 blob。
func (s *Syncer) contentFor(snap protocol.ConfigSnapshot) (map[string][]byte, error) {
	s.mu.Lock()
	got := s.content
	s.mu.Unlock()

	out := make(map[string][]byte, len(snap.Files))
	for _, f := range snap.Files {
		if b, ok := got[f.Hash]; ok {
			out[f.Hash] = b
			continue
		}
		b, err := s.cache.Get(f.Hash)
		if err != nil {
			return nil, fmt.Errorf("syncer: 缺少 %s 的内容（hash %s）", f.Path, f.Hash)
		}
		out[f.Hash] = b
	}
	return out, nil
}

// saveSecrets 把快照带来的凭据、变量与内置的 machine.* 一起落盘。
func (s *Syncer) saveSecrets(snap protocol.ConfigSnapshot) error {
	hostname, _ := os.Hostname()
	f := &secrets.File{
		Creds: snap.Credentials,
		Vars:  snap.Variables,
		Machine: map[string]string{
			"name":     s.d.MachineName,
			"hostname": hostname,
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	if f.Creds == nil {
		f.Creds = map[string]string{}
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	return secrets.Save(s.d.Dir, f)
}
```

> `{{machine.name}}` 取的是面板上的备注名，agent 自己不知道——由 `Deps.MachineName` 传入，装配处（Task 5）从 hub 下发的快照或本地配置取。M1 先用 hostname 兜底：`New` 里若 `MachineName` 为空，`saveSecrets` 用 hostname 填它。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/syncer/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/syncer/
git commit -m "feat: agent 侧下发编排"
```

---

### Task 5: 两侧装配

**Files:**
- Modify: `hub/hub.go`
- Modify: `agent/internal/conn/dial.go`（`Config` 加 `OnMessage`）
- Modify: `agent/connect.go`、`agent/run.go`
- Test: `hub/hub_test.go`、`agent/internal/conn/dial_test.go`

**Interfaces:**
- Produces:
  ```go
  // conn.Config 追加
  OnMessage func(protocol.Envelope)

  // Hub 新增未导出字段
  blobs *blobs.Store
  sets  *configsets.Service
  revs  *revisions.Service
  sync  *configsync.Service

  // agent.ConnectOptions 追加
  OnMessage func(protocol.Envelope)
  ```

M0 的 `handler.onConnected` 只认 `AuthResult`，其余一律「忽略连接期收到的意外消息」。M1 要把配置类消息交给 syncer。

- [ ] **Step 1: 写失败的测试（agent 侧）**

追加到 `agent/internal/conn/dial_test.go`：

```go
// 连接期收到的业务消息要交给上层，而不是落进「忽略意外消息」的分支。
// M0 的 AC 修正（inbox 无人消费）就是这个位置出的问题。
func TestConnectedMessagesReachOnMessage(t *testing.T) {
	got := make(chan protocol.Envelope, 4)
	// 用该文件已有的 fakeHandler 起一个握手完成的连接，
	// Config 里带上 OnMessage。
	srv, cfg := newFakeHub(t)
	cfg.OnMessage = func(env protocol.Envelope) { got <- env }

	sess, err := conn.Dial(context.Background(), cfg)
	require.NoError(t, err)
	defer sess.Close()

	srv.push(t, protocol.KindConfigNotify, protocol.ConfigNotify{ConfigSetID: "set1"})

	select {
	case env := <-got:
		require.Equal(t, protocol.KindConfigNotify, env.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("OnMessage 未收到 ConfigNotify")
	}
}

// AuthResult 仍走原来的撤销通知路径，不该被 OnMessage 抢走。
func TestAuthResultStillEndsSession(t *testing.T) {
	srv, cfg := newFakeHub(t)
	cfg.OnMessage = func(protocol.Envelope) { t.Error("AuthResult 不该走 OnMessage") }

	sess, err := conn.Dial(context.Background(), cfg)
	require.NoError(t, err)
	srv.push(t, protocol.KindAuthResult,
		protocol.AuthResult{OK: false, Code: protocol.CodeMachineRemoved, Reason: "已删除"})

	select {
	case <-sess.Done():
		var rej *conn.RejectedError
		require.ErrorAs(t, sess.Err(), &rej)
		require.Equal(t, protocol.CodeMachineRemoved, rej.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("连接未因撤销通知结束")
	}
}
```

> `newFakeHub` 与 `srv.push` 按该文件里既有的假 hub 辅助补出来：`push` 就是往这条连接上写一帧编码好的信封。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/conn/ -run OnMessage -v`
Expected: FAIL，`cfg.OnMessage undefined`

- [ ] **Step 3: 实现 agent 侧**

`conn.Config` 追加：

```go
	// OnMessage 接收握手完成之后收到的业务消息（M1 的配置下发）。
	// 为 nil 时这些消息被记 debug 日志后丢弃。
	//
	// 它跑在读循环那个 goroutine 上：实现方必须自己保证不长时间阻塞，
	// 否则会卡住这条连接的后续收信（含心跳）。
	OnMessage func(protocol.Envelope)
```

`handler` 加字段 `onMessage func(protocol.Envelope)`，`Dial` 里赋值。`onConnected` 改成：

```go
func (h *handler) onConnected(socket *gws.Conn, env protocol.Envelope) {
	if env.Kind != protocol.KindAuthResult {
		// M1：配置类消息交给上层的 syncer。
		if h.onMessage != nil {
			h.onMessage(env)
			return
		}
		h.log.Debug("未装配消息处理器，忽略", "kind", env.Kind.String())
		return
	}
	err := rejectionFrom(env)
	if err == nil {
		return
	}
	h.log.Warn("hub 撤销了本连接的授权", "error", err)
	if h.session != nil {
		h.session.finish(err)
	}
	_ = socket.WriteClose(1000, nil)
}
```

`agent/connect.go` 的 `ConnectOptions` 追加 `OnMessage func(protocol.Envelope)` 并透传给 `conn.Config`。

`agent/run.go` 里在 `Dial` 闭包中构造 syncer 并接线：

```go
	managedHome, err := cfg.ManagedHomeDir()
	if err != nil {
		return err
	}
	o.Logger.Info("受管 HOME", "path", managedHome)

	client := conn.NewClient(conn.ClientConfig{
		Dial: func(ctx context.Context) (*Session, error) {
			// syncer 每条连接一个：Send 绑定在具体会话上，
			// 连接断了旧的 syncer 也就该退场。state.json 是跨连接的持久层。
			var sess *Session
			sync, err := syncer.New(syncer.Deps{
				Dir:         o.Dir,
				ManagedHome: managedHome,
				Clock:       clock.System(),
				Logger:      o.Logger,
				Send: func(kind protocol.Kind, payload any) error {
					if sess == nil {
						return errors.New("agent: 连接尚未建立")
					}
					return sess.Send(kind, payload)
				},
			})
			if err != nil {
				return nil, err
			}
			sess, err = Connect(ctx, ConnectOptions{
				Dir:              o.Dir,
				HubURL:           cfg.HubURL,
				HandshakeTimeout: o.HandshakeTimeout,
				ReadTimeout:      o.ReadTimeout,
				Logger:           o.Logger,
				OnMessage:        sync.Handle,
			})
			if err != nil {
				return nil, err
			}
			return sess, nil
		},
		// 其余字段不变
	})
```

> `sess` 先声明后赋值、闭包里判 nil，是因为 `Send` 要指向即将建立的那条连接，而 `OnMessage` 又必须在 `Connect` 之前就绪。判 nil 那条分支只在「握手期收到业务消息」时命中，而 hub 不会那么发。

- [ ] **Step 4: 实现 hub 侧装配**

`hub/hub.go` 的 `Hub` 加字段：

```go
	blobs *blobs.Store
	sets  *configsets.Service
	revs  *revisions.Service
	sync  *configsync.Service
```

在 `OnServe` 里、`h.creds` 就绪之后、`ws.NewHandler` 之前：

```go
		h.blobs = blobs.New(e.App)
		h.sets = configsets.NewService(e.App, h.blobs, h.events)
		h.revs = revisions.NewService(e.App, h.blobs, h.events)
		h.sync = configsync.NewService(configsync.Deps{
			App: e.App, Blobs: h.blobs, Sets: h.sets, Revs: h.revs,
			Creds: h.creds, Events: h.events, Sender: h.machines,
			Logger: e.App.Logger(),
		})
		// 离线补发：agent 一上线就无条件通知一次，幂等保证它在无事可做时
		// 是零写入（spec §7.3）。
		h.machines.OnOnline(h.sync.OnMachineOnline)
```

`ws.NewHandler` 的 `ws.Deps` 追加 `Agent: h.sync`。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add hub/ agent/
git commit -m "feat: hub 与 agent 两侧接上下发链路"
```

---

### Task 6: 端到端集成测试

**Files:**
- Modify: `internal/testsupport/agent.go`（加 `RunSync` / `ReadManaged` / `WriteManaged` / `State`）
- Create: `internal/testsupport/configsync_test.go`

**Interfaces:**
- Produces:
  ```go
  func (a *TestAgent) RunSync(t *testing.T, th *TestHub) *agent.Session
  func (a *TestAgent) ReadManaged(t *testing.T, rel string) []byte
  func (a *TestAgent) WriteManaged(t *testing.T, rel string, content []byte, perm os.FileMode)
  func (a *TestAgent) State(t *testing.T) *state.State
  ```

- [ ] **Step 1: 写脚手架**

追加到 `internal/testsupport/agent.go`：

```go
// RunSync 连上 hub 并接好 syncer，返回会话（已注册 t.Cleanup 关闭）。
//
// 与 Connect 的区别只有一个：它把配置类消息接到真正的 syncer 上，
// 因此这条连接会真的写受管 HOME。
func (a *TestAgent) RunSync(t *testing.T, th *TestHub) *agent.Session {
	t.Helper()

	var sess *agent.Session
	sy, err := syncer.New(syncer.Deps{
		Dir:         a.Dir,
		ManagedHome: a.ManagedHome,
		Clock:       clock.System(),
		Send: func(kind protocol.Kind, payload any) error {
			return sess.Send(kind, payload)
		},
	})
	require.NoError(t, err, "构造 syncer")

	sess, err = agent.Connect(context.Background(), agent.ConnectOptions{
		Dir:              a.Dir,
		HubURL:           th.HTTPURL,
		HandshakeTimeout: 2 * time.Second,
		ReadTimeout:      2 * time.Second,
		OnMessage:        sy.Handle,
	})
	require.NoError(t, err, "agent 连接 hub")
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func (a *TestAgent) ReadManaged(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.ManagedHome, filepath.FromSlash(rel)))
	require.NoError(t, err, "读受管文件 %s", rel)
	return b
}

func (a *TestAgent) WriteManaged(t *testing.T, rel string, content []byte, perm os.FileMode) {
	t.Helper()
	p := filepath.Join(a.ManagedHome, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, content, perm))
}

func (a *TestAgent) State(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(a.Dir)
	require.NoError(t, err, "读 state.json")
	return st
}
```

- [ ] **Step 2: 写集成测试**

Create `internal/testsupport/configsync_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

// 指派 → 下发 → 落盘 → 回执，一条链路走通。DoD 第 2 条的骨架。
func TestAssignThenApply(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, revID := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md":     "# 团队规矩\n",
		".claude/settings.json": `{"model":"opus"}`,
	})

	ta.RunSync(t, th)

	// 指派并通知
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)

	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)
	require.Equal(t, "# 团队规矩\n", string(ta.ReadManaged(t, ".claude/CLAUDE.md")))
	require.Equal(t, `{"model":"opus"}`, string(ta.ReadManaged(t, ".claude/settings.json")))

	st := ta.State(t)
	require.Equal(t, revID, st.Revision)
	require.Equal(t, "ok", st.Health)

	th.RequireEvent(t, ta.MachineID, "apply.ok")
}

// 凭据渲染：blob 里是占位符，磁盘上是真值，库里查不到明文（DoD 第 1、9 条）。
func TestCredentialIsRenderedButNeverStored(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	th.SeedCredential(t, "anthropic_key", "sk-ant-secret-9999")
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"{{cred.anthropic_key}}"}}`,
	})

	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	require.Contains(t, string(ta.ReadManaged(t, ".claude/settings.json")), "sk-ant-secret-9999")
	th.RequireNoPlaintextInBlobs(t, "sk-ant-secret-9999")
}

// 轮换不产生新版本，但受影响的机器要重新落盘（DoD 第 9 条）。
func TestRotationRewritesWithoutNewRevision(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())

	th.SeedCredential(t, "k", "sk-old-value-1111")
	setID, revID := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/settings.json": `{"K":"{{cred.k}}"}`,
	})
	ta.RunSync(t, th)
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply)
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)

	th.RotateCredential(t, "k", "sk-new-value-2222")

	require.Eventually(t, func() bool {
		b, err := os.ReadFile(filepath.Join(ta.ManagedHome, ".claude/settings.json"))
		return err == nil && strings.Contains(string(b), "sk-new-value-2222")
	}, 3*time.Second, 20*time.Millisecond, "轮换后未重新落盘")

	revs, err := th.App.FindRecordsByFilter("revisions", "config_set = {:s}", "", 0, 0,
		map[string]any{"s": setID})
	require.NoError(t, err)
	require.Len(t, revs, 1, "轮换不产生新 Revision")
	require.Equal(t, revID, ta.State(t).Revision)
}

// 离线补发：机器不在线时发布，上线后自动对齐（spec §7.3）。
func TestOfflineMachineCatchesUpOnReconnect(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th, t.TempDir())
	setID, _ := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md": "第一版\n",
	})
	th.Assign(t, ta.MachineID, setID, configsets.ModeApply) // 此刻 agent 还没连

	ta.RunSync(t, th) // 上线 → OnOnline 补发 → 拉取 → 落盘
	th.RequireAssignmentState(t, ta.MachineID, configsets.StateAligned)
	require.Equal(t, "第一版\n", string(ta.ReadManaged(t, ".claude/CLAUDE.md")))
}
```

`TestHub` 需要三个新辅助（放进 `internal/testsupport/configsets.go`）：

```go
// Assign 指派并立刻通知，等价于 UI 上点「应用配置集」。
func (h *TestHub) Assign(t *testing.T, machineID, setID, mode string) {
	t.Helper()
	require.NoError(t, h.Hub.AssignConfigSet(machineID, setID, mode))
}

func (h *TestHub) SeedCredential(t *testing.T, name, value string) {
	t.Helper()
	require.NoError(t, h.Hub.CreateCredential(name, value, ""))
}

func (h *TestHub) RotateCredential(t *testing.T, name, value string) {
	t.Helper()
	require.NoError(t, h.Hub.RotateCredential(name, value))
}

// RequireNoPlaintextInBlobs 逐条读回 blob 内容，断言里面没有明文密钥。
// DoD 第 1 条与第 6 条都要求「直接查库确认」。
func (h *TestHub) RequireNoPlaintextInBlobs(t *testing.T, secret string) {
	t.Helper()
	store := blobs.New(h.App)
	recs, err := h.App.FindAllRecords("blobs")
	require.NoError(t, err)
	for _, r := range recs {
		content, err := store.Get(r.GetString("hash"))
		require.NoError(t, err)
		require.NotContains(t, string(content), secret,
			"blob %s 里出现了明文密钥", r.GetString("hash"))
	}
}
```

对应地，`hub` 包要开三个公开入口（放在 `hub/api.go`，与 `IssueEnrollToken` 同性质——「hub 对外暴露的管理动作」）：

```go
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
```

> 这五个入口同时是子计划 10 的 HTTP 路由要调的东西：路由层只做编解码与状态码映射，业务规则留在这里（M0 spec §5.2 的分工）。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test -tags=testing ./internal/testsupport/ -run 'AssignThenApply|Credential|Offline' -v`
Expected: FAIL，`h.Hub.AssignConfigSet undefined`

- [ ] **Step 4: 实现并跑通**

按上面补齐 `hub/api.go` 与 testsupport 辅助。

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/ internal/testsupport/
git commit -m "test: 下发链路端到端集成测试"
```

---

### Task 7: 阶段一真机验收（DoD 2、8、9、10）

**Files:**
- Create: `docs/superpowers/plans/2026-07-31-m1-config-loop/acceptance.md`

前端就绪之前先把纯后端能验的几条跑掉，用 `orciny-agent sync`（子计划 17）之前可以用两台真机 + `curl` 打 hub 的 API。

- [ ] **Step 1: 记录验收结果**

在 `acceptance.md` 里建表，列 DoD 15 条与「待验 / 通过 / 未通过」三态，本次先填 2、8、9、10 四条的实测结论（含命令与输出摘要）。未通过的写清现象与定位。

- [ ] **Step 2: 提交**

```bash
git add docs/superpowers/plans/2026-07-31-m1-config-loop/acceptance.md
git commit -m "docs: M1 阶段一验收记录（下发链路）"
```
