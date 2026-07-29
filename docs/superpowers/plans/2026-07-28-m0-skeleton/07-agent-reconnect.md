# M0 计划 7 · agent 重连状态机 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agent 成为一个能长期无人值守运行的进程：带抖动的指数退避、按 `AuthResult.Code` 分流、hub 签名不符时进入 `Compromised` 终态、`run` / `status` 子命令、JSON 日志与脱敏。

**Architecture:** 重连循环把「怎么连」抽成注入的 `Dial` 函数，因此全部时序分支都能在无网络、无真实等待的条件下测。退避与固定间隔重试都走 `clock.Clock`。状态变化写一份 `~/.orciny/status.json`，`status` 子命令读它——M0 没有 IPC，这是让运维在机器上一眼看清状况的最小代价。

**Tech Stack:** Go 1.26 · cobra v1.10.2 · 标准库 `log/slog` `os/exec`

**上位文档：** [统括计划](00-overview.md) · [spec §7.2 §7.3 §7.4 §7.5](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；共享只走 `protocol/`。
- 测试禁用 `time.Sleep`：退避与固定间隔走 `clock.Fake`，等可观测效果用 `require.Eventually`。
- **注册 token 只记前 8 位；私钥、hub 公钥内容一律不入日志。**
- 所有落盘走 `internal/atomicfile.Write`。
- agent 除 `claude --version` 外不读取用户的任何文件，尤其不碰 `~/.claude`。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `agent/internal/conn/backoff.go` | 退避与抖动 |
| `agent/internal/conn/client.go` | 重连状态机 |
| `agent/internal/probe/claude.go` | `claude --version` 探测（3 秒超时，失败留空） |
| `agent/internal/logging/logging.go` | JSON slog 装配、可选文件日志与轮转、脱敏辅助 |
| `agent/status.go` | `status.json` 的读写与 `agent.Status` 类型 |
| `agent/run.go` | 公开入口 `agent.Run` |
| `agent/cli.go` | 修改：`run` / `status` 子命令 |

---

## Task 1: 退避与抖动

**Files:**
- Create: `agent/internal/conn/backoff.go`, `agent/internal/conn/backoff_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  ```go
  type Backoff struct{ /* 私有字段 */ }
  func NewBackoff(base, max time.Duration, jitter float64, rnd func() float64) *Backoff
  func (b *Backoff) Next() time.Duration
  func (b *Backoff) Reset()
  ```

- [ ] **Step 1: 写失败的测试**

创建 `agent/internal/conn/backoff_test.go`：

```go
package conn_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
)

// noJitter 让随机数固定落在区间中点，抖动因子恰好为 1，
// 于是可以逐项断言退避序列本身。
func noJitter() float64 { return 0.5 }

func TestBackoffDoublesUpToMax(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter)

	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	}
	for i, w := range want {
		require.Equal(t, w, b.Next(), "第 %d 次退避", i+1)
	}
}

func TestBackoffResetGoesBackToBase(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter)
	b.Next()
	b.Next()
	b.Next()

	b.Reset()
	require.Equal(t, time.Second, b.Next(), "连接成功后退避必须重置")
}

// 抖动不是可选项：机队规模上去后，hub 重启会让所有 agent 在同一秒重连，
// 抖动把这个尖峰摊平（spec §7.2）。
func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	b := conn.NewBackoff(10*time.Second, time.Minute, 0.2, rnd.Float64)

	sawBelow, sawAbove := false, false
	for i := 0; i < 200; i++ {
		b.Reset()
		d := b.Next()
		require.GreaterOrEqual(t, d, 8*time.Second, "不得低于 base 的 80%%")
		require.LessOrEqual(t, d, 12*time.Second, "不得高于 base 的 120%%")
		if d < 10*time.Second {
			sawBelow = true
		}
		if d > 10*time.Second {
			sawAbove = true
		}
	}
	require.True(t, sawBelow && sawAbove, "抖动应当双向散开，而不是恒定偏移")
}

func TestJitterAppliesAtMaxToo(t *testing.T) {
	b := conn.NewBackoff(time.Second, 10*time.Second, 0.2, func() float64 { return 0 })
	for i := 0; i < 10; i++ {
		b.Next()
	}
	d := b.Next()
	require.Equal(t, 8*time.Second, d, "封顶后仍然抖动，否则封顶点会再次形成尖峰")
}

func TestZeroJitterIsDeterministic(t *testing.T) {
	b := conn.NewBackoff(time.Second, time.Minute, 0, func() float64 { return 0 })
	require.Equal(t, time.Second, b.Next())
	require.Equal(t, 2*time.Second, b.Next())
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./agent/internal/conn/... -run Backoff -v`
Expected: FAIL —— `undefined: conn.NewBackoff`

- [ ] **Step 3: 写实现**

创建 `agent/internal/conn/backoff.go`：

```go
package conn

import "time"

// Backoff 是带抖动的指数退避（spec §7.2）。非并发安全——由重连循环独占。
type Backoff struct {
	base   time.Duration
	max    time.Duration
	jitter float64
	rnd    func() float64 // 返回 [0,1)，注入以便测试

	cur time.Duration
}

// NewBackoff 构造退避器。jitter 是比例，0.2 表示 ±20%。
// rnd 传 nil 时不抖动（仅测试用；生产必须传真随机源）。
func NewBackoff(base, max time.Duration, jitter float64, rnd func() float64) *Backoff {
	if rnd == nil {
		rnd = func() float64 { return 0.5 }
	}
	return &Backoff{base: base, max: max, jitter: jitter, rnd: rnd}
}

// Next 返回下一次等待时长并推进内部状态。
func (b *Backoff) Next() time.Duration {
	if b.cur == 0 {
		b.cur = b.base
	} else {
		b.cur *= 2
		if b.cur > b.max {
			b.cur = b.max
		}
	}
	return b.applyJitter(b.cur)
}

// Reset 把退避拉回起点。连接成功后必须调用。
func (b *Backoff) Reset() { b.cur = 0 }

// applyJitter 把 d 乘以 [1-jitter, 1+jitter] 之间的一个因子。
// 封顶后同样抖动 —— 否则所有 agent 会在 60 秒这个点上重新对齐。
func (b *Backoff) applyJitter(d time.Duration) time.Duration {
	if b.jitter <= 0 {
		return d
	}
	factor := 1 + (b.rnd()*2-1)*b.jitter
	return time.Duration(float64(d) * factor)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./agent/internal/conn/... -run 'Backoff|Jitter' -v`
Expected: PASS（5 个用例）

- [ ] **Step 5: 提交**

```bash
git add agent/internal/conn/backoff.go agent/internal/conn/backoff_test.go
git commit -m "feat: 带抖动的指数退避"
```

---

## Task 2: 重连状态机

**Files:**
- Create: `agent/internal/conn/client.go`, `agent/internal/conn/client_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Backoff`、计划 5 的 `Session` / `ErrHubSignature` / `RejectedError`、`clock.Clock`
- Produces:
  ```go
  type State int
  const (
      StateDisconnected State = iota
      StateHandshaking
      StateConnected
      StateCompromised
  )
  func (s State) String() string

  type ClientConfig struct {
      Dial                func(ctx context.Context) (*Session, error)
      Clock               clock.Clock
      Backoff             *Backoff
      RejectRetryInterval time.Duration // 0 → 5min
      Logger              *slog.Logger
      OnState             func(State, error)
  }
  type Client struct{ /* 私有字段 */ }
  func NewClient(cfg ClientConfig) *Client
  func (c *Client) Run(ctx context.Context) error
  func (c *Client) State() State
  var ErrMachineRemoved = errors.New("conn: 机器已在面板中被删除")
  ```

- [ ] **Step 1: 写失败的测试**

创建 `agent/internal/conn/client_test.go`：

```go
package conn_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

var epoch = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// dialRecorder 记录每次拨号的逻辑时刻，并按脚本返回结果。
type dialRecorder struct {
	clk *clock.Fake

	mu      sync.Mutex
	times   []time.Time
	results []func() (*conn.Session, error)
	calls   int
}

func (d *dialRecorder) dial(context.Context) (*conn.Session, error) {
	d.mu.Lock()
	d.times = append(d.times, d.clk.Now())
	i := d.calls
	d.calls++
	var fn func() (*conn.Session, error)
	if i < len(d.results) {
		fn = d.results[i]
	} else {
		fn = d.results[len(d.results)-1]
	}
	d.mu.Unlock()
	return fn()
}

func (d *dialRecorder) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *dialRecorder) gaps() []time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]time.Duration, 0, len(d.times))
	for i := 1; i < len(d.times); i++ {
		out = append(out, d.times[i].Sub(d.times[i-1]))
	}
	return out
}

func failWith(err error) func() (*conn.Session, error) {
	return func() (*conn.Session, error) { return nil, err }
}

// runClient 起 Run 并返回一个取结果的函数。
func runClient(t *testing.T, c *conn.Client) (context.CancelFunc, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, func() error {
		select {
		case err := <-errCh:
			return err
		case <-time.After(3 * time.Second):
			t.Fatal("Run 未在预期时间内返回")
			return nil
		}
	}
}

// 网络错误 → 指数退避序列（注入假时钟与固定随机源）
func TestNetworkErrorsBackOffExponentially(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
		failWith(errors.New("connection refused")),
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial:    d.dial,
		Clock:   clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter),
	})
	cancel, wait := runClient(t, c)

	// 逐次放行：每次都等退避定时器挂上再推进逻辑时间。
	for i := 0; i < 5; i++ {
		require.Eventually(t, func() bool { return clk.TimerCount() == 1 },
			2*time.Second, 5*time.Millisecond, "第 %d 次退避未挂上定时器", i+1)
		clk.Advance(time.Minute) // 一次推过，具体等了多久看 gaps
	}
	require.Eventually(t, func() bool { return d.count() >= 6 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)

	gaps := d.gaps()
	require.GreaterOrEqual(t, len(gaps), 5)
	require.Equal(t, time.Second, gaps[0])
	require.Equal(t, 2*time.Second, gaps[1])
	require.Equal(t, 4*time.Second, gaps[2])
	require.Equal(t, 8*time.Second, gaps[3])
	require.Equal(t, 16*time.Second, gaps[4])
}

// hub 明确拒绝（非致命码）→ 固定 5 分钟间隔，而不是指数退避
func TestRejectedCodesUseFixedInterval(t *testing.T) {
	for _, code := range []uint8{
		protocol.CodeUnknownFingerprint,
		protocol.CodeBadSignature,
		protocol.CodeVersionTooOld,
	} {
		t.Run(protocolCodeName(code), func(t *testing.T) {
			clk := clock.NewFake(epoch)
			d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
				failWith(&conn.RejectedError{Code: code, Reason: "测试"}),
			}}

			c := conn.NewClient(conn.ClientConfig{
				Dial:                d.dial,
				Clock:               clk,
				Backoff:             conn.NewBackoff(time.Second, time.Minute, 0, nil),
				RejectRetryInterval: 5 * time.Minute,
			})
			cancel, wait := runClient(t, c)

			for i := 0; i < 2; i++ {
				require.Eventually(t, func() bool { return clk.TimerCount() == 1 },
					2*time.Second, 5*time.Millisecond)
				clk.Advance(5 * time.Minute)
			}
			require.Eventually(t, func() bool { return d.count() >= 3 },
				2*time.Second, 5*time.Millisecond)

			cancel()
			require.ErrorIs(t, wait(), context.Canceled)

			for i, gap := range d.gaps() {
				require.Equal(t, 5*time.Minute, gap, "第 %d 次间隔应为固定 5 分钟", i+1)
			}
		})
	}
}

// 机器被删除 → 停止重试
func TestMachineRemovedStopsRetrying(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
		failWith(&conn.RejectedError{Code: protocol.CodeMachineRemoved, Reason: "已删除"}),
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
	})
	_, wait := runClient(t, c)

	err := wait()
	require.ErrorIs(t, err, conn.ErrMachineRemoved)
	require.Equal(t, 1, d.count(), "不该再拨第二次")
	require.Equal(t, 0, clk.TimerCount(), "不该挂任何重试定时器")
}

// hub 签名验证失败 → Compromised 终态，永不重试
func TestHubSignatureFailureEntersCompromised(t *testing.T) {
	clk := clock.NewFake(epoch)
	d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
		failWith(conn.ErrHubSignature),
	}}

	var states []conn.State
	var mu sync.Mutex
	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
		OnState: func(s conn.State, _ error) {
			mu.Lock()
			states = append(states, s)
			mu.Unlock()
		},
	})
	_, wait := runClient(t, c)

	require.ErrorIs(t, wait(), conn.ErrHubSignature)
	require.Equal(t, conn.StateCompromised, c.State())
	require.Equal(t, 1, d.count())

	mu.Lock()
	require.Contains(t, states, conn.StateCompromised)
	mu.Unlock()
}

// 连接成功后退避重置：一次成功之后再失败，应当重新从 base 开始
func TestBackoffResetsAfterSuccessfulConnection(t *testing.T) {
	clk := clock.NewFake(epoch)
	fail := failWith(errors.New("boom"))

	sessions := make(chan *conn.Session, 1)
	ok := func() (*conn.Session, error) {
		s := conn.NewTestSession()
		sessions <- s
		return s, nil
	}

	d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
		fail, fail, ok, fail, fail,
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, noJitter),
	})
	cancel, wait := runClient(t, c)

	// 两次失败：1s、2s
	for i := 0; i < 2; i++ {
		require.Eventually(t, func() bool { return clk.TimerCount() == 1 },
			2*time.Second, 5*time.Millisecond)
		clk.Advance(time.Minute)
	}

	// 第三次成功，随后主动断开
	s := <-sessions
	require.Eventually(t, func() bool { return c.State() == conn.StateConnected },
		2*time.Second, 5*time.Millisecond)
	s.CloseForTest(errors.New("hub 走了"))

	// 断开后应从 base 重新开始
	require.Eventually(t, func() bool { return clk.TimerCount() == 1 },
		2*time.Second, 5*time.Millisecond)
	clk.Advance(time.Minute)
	require.Eventually(t, func() bool { return d.count() >= 4 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)

	gaps := d.gaps()
	require.Equal(t, time.Second, gaps[0])
	require.Equal(t, 2*time.Second, gaps[1])
	require.Equal(t, time.Second, gaps[2], "成功一次之后退避必须回到 base")
}

func TestStateTransitions(t *testing.T) {
	clk := clock.NewFake(epoch)
	sessions := make(chan *conn.Session, 1)
	d := &dialRecorder{clk: clk, results: []func() (*conn.Session, error){
		func() (*conn.Session, error) {
			s := conn.NewTestSession()
			sessions <- s
			return s, nil
		},
	}}

	c := conn.NewClient(conn.ClientConfig{
		Dial: d.dial, Clock: clk,
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0, nil),
	})
	require.Equal(t, conn.StateDisconnected, c.State())

	cancel, wait := runClient(t, c)
	<-sessions
	require.Eventually(t, func() bool { return c.State() == conn.StateConnected },
		2*time.Second, 5*time.Millisecond)

	cancel()
	require.ErrorIs(t, wait(), context.Canceled)
}

func TestStateString(t *testing.T) {
	require.Equal(t, "disconnected", conn.StateDisconnected.String())
	require.Equal(t, "handshaking", conn.StateHandshaking.String())
	require.Equal(t, "connected", conn.StateConnected.String())
	require.Equal(t, "compromised", conn.StateCompromised.String())
}

func protocolCodeName(code uint8) string {
	switch code {
	case protocol.CodeUnknownFingerprint:
		return "unknown_fingerprint"
	case protocol.CodeBadSignature:
		return "bad_signature"
	case protocol.CodeVersionTooOld:
		return "version_too_old"
	default:
		return "other"
	}
}
```

- [ ] **Step 2: 给 Session 加测试构造器**

测试需要造一个「已连接、可主动断开」的 `Session`，但 `Session` 的字段是私有的。在 `agent/internal/conn/dial.go` 末尾追加（生产代码里也用得上一个显式的关闭原因）：

```go
// NewTestSession 造一条不带真实 socket 的会话，仅供本包测试使用。
// 它不能发消息，只能被关闭——重连循环关心的正是「什么时候结束」。
func NewTestSession() *Session {
	return &Session{done: make(chan struct{})}
}

// CloseForTest 模拟连接因 err 结束。
func (s *Session) CloseForTest(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}
```

同时把 `Session.Close()` 改成能容忍 `socket == nil`：

```go
func (s *Session) Close() error {
	if s.socket == nil {
		s.CloseForTest(nil)
		return nil
	}
	return s.socket.WriteClose(1000, nil)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./agent/internal/conn/... -run 'Client|State|Backoff|Machine|Hub' -v`
Expected: FAIL —— `undefined: conn.NewClient`

- [ ] **Step 4: 写实现**

创建 `agent/internal/conn/client.go`：

```go
package conn

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// State 是重连状态机的状态（spec §7.2）。
type State int

const (
	StateDisconnected State = iota
	StateHandshaking
	StateConnected
	// StateCompromised 是终态：hub 签名验证失败，永不重试。
	StateCompromised
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateHandshaking:
		return "handshaking"
	case StateConnected:
		return "connected"
	case StateCompromised:
		return "compromised"
	default:
		return "unknown"
	}
}

// ErrMachineRemoved 表示 hub 说这台机器已被删除，需要人工重新 enroll。
var ErrMachineRemoved = errors.New("conn: 机器已在面板中被删除，需重新 enroll")

type ClientConfig struct {
	// Dial 建立一条已完成握手的连接。抽成函数是为了让重连的全部时序
	// 分支都能在没有网络的条件下测试。
	Dial func(ctx context.Context) (*Session, error)

	Clock   clock.Clock
	Backoff *Backoff

	// RejectRetryInterval 是「被明确拒绝」时的固定重试间隔。0 → 5 分钟。
	RejectRetryInterval time.Duration

	Logger  *slog.Logger
	OnState func(State, error)
}

// Client 是 agent 的重连状态机。
type Client struct {
	cfg ClientConfig

	mu    sync.RWMutex
	state State
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.Clock == nil {
		cfg.Clock = clock.System()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.RejectRetryInterval == 0 {
		cfg.RejectRetryInterval = 5 * time.Minute
	}
	if cfg.Backoff == nil {
		cfg.Backoff = NewBackoff(time.Second, time.Minute, 0.2, nil)
	}
	return &Client{cfg: cfg}
}

func (c *Client) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Client) setState(s State, err error) {
	c.mu.Lock()
	c.state = s
	c.mu.Unlock()
	if c.cfg.OnState != nil {
		c.cfg.OnState(s, err)
	}
}

// Run 一直连着 hub，直到 ctx 取消或遇到终局错误。
//
// 终局错误只有两种：hub 签名验证失败（Compromised）与机器已被删除。
// 其余一切——网络不通、hub 没起来、指纹没登记、版本过旧——都会继续重试，
// 因为它们都可能被运维在另一头修好。
func (c *Client) Run(ctx context.Context) error {
	for {
		c.setState(StateHandshaking, nil)
		session, err := c.cfg.Dial(ctx)
		if err != nil {
			wait, fatal := c.classify(err)
			if fatal != nil {
				return fatal
			}
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}

		c.cfg.Backoff.Reset()
		c.setState(StateConnected, nil)
		c.cfg.Logger.Info("已连接 hub")

		select {
		case <-session.Done():
			c.setState(StateDisconnected, session.Err())
			c.cfg.Logger.Warn("与 hub 的连接已断开", "error", session.Err())
			if err := c.sleep(ctx, c.cfg.Backoff.Next()); err != nil {
				return err
			}
		case <-ctx.Done():
			_ = session.Close()
			c.setState(StateDisconnected, ctx.Err())
			return ctx.Err()
		}
	}
}

// classify 决定这次失败该等多久，或者干脆别等了。
// 返回的第二个值非 nil 表示终局错误，Run 应当直接返回它。
func (c *Client) classify(err error) (time.Duration, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 0, err
	}

	// hub 签名不匹配意味着要么在遭受中间人攻击，要么 hub 换了密钥
	// （恢复备份时丢了 orciny_hub_key.pem）。两种情况都需要人来判断，
	// agent 自作主张继续重试只会掩盖问题（spec §7.2）。
	if errors.Is(err, ErrHubSignature) {
		c.setState(StateCompromised, err)
		c.cfg.Logger.Error("hub 签名验证失败，停止一切重试。"+
			"请确认 hub 是否更换过密钥，或本机是否遭到中间人攻击",
			"error", err)
		return 0, err
	}

	var rej *RejectedError
	if errors.As(err, &rej) {
		if rej.Code == protocol.CodeMachineRemoved {
			c.setState(StateDisconnected, err)
			c.cfg.Logger.Error("hub 报告本机已被删除，停止重试；如需重新接入请执行 orciny-agent enroll",
				"reason", rej.Reason)
			return 0, ErrMachineRemoved
		}
		// 指纹未登记、签名错误、版本过旧：都可能被运维修好，
		// 用固定的长间隔重试，并且每次都记日志（spec §7.2）。
		c.setState(StateDisconnected, err)
		c.cfg.Logger.Warn("hub 拒绝连接，将按固定间隔重试",
			"code", rej.Code, "reason", rej.Reason, "interval", c.cfg.RejectRetryInterval)
		return c.cfg.RejectRetryInterval, nil
	}

	c.setState(StateDisconnected, err)
	wait := c.cfg.Backoff.Next()
	c.cfg.Logger.Warn("连接 hub 失败，稍后重试", "error", err, "retry_in", wait)
	return wait, nil
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := c.cfg.Clock.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./agent/internal/conn/... -race -v`
Expected: PASS

若 `TestNetworkErrorsBackOffExponentially` 的 gaps 里出现 0：说明 `Advance` 跑在定时器挂上之前。检查测试里每次推进前都有 `require.Eventually(TimerCount == 1)`。

- [ ] **Step 6: 提交**

```bash
git add agent/internal/conn
git commit -m "feat: agent 重连状态机与原因码分流"
```

---

## Task 3: Claude Code 版本探测

**Files:**
- Create: `agent/internal/probe/claude.go`, `agent/internal/probe/claude_test.go`
- Modify: `agent/internal/probe/probe.go`（新增 `Collect`）

**Interfaces:**
- Consumes: `protocol.MachineInfo`
- Produces:
  ```go
  const ClaudeProbeTimeout = 3 * time.Second
  func ClaudeCodeVersion(ctx context.Context) string   // 失败返回 ""
  func Collect(ctx context.Context, agentVersion string) protocol.MachineInfo
  ```

- [ ] **Step 1: 写失败的测试**

创建 `agent/internal/probe/claude_test.go`：

```go
package probe_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/probe"
)

// fakeClaude 在 PATH 前面塞一个假的 claude 可执行文件。
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("本用例用 shell 脚本伪造可执行文件")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClaudeCodeVersionParsesOutput(t *testing.T) {
	fakeClaude(t, `echo "2.1.3 (Claude Code)"`)
	require.Equal(t, "2.1.3", probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionHandlesBareVersion(t *testing.T) {
	fakeClaude(t, `echo "1.0.10"`)
	require.Equal(t, "1.0.10", probe.ClaudeCodeVersion(context.Background()))
}

// 机器上没装 Claude Code 是合法状态（spec §7.4）——留空，不报错。
func TestClaudeCodeVersionEmptyWhenMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnNonZeroExit(t *testing.T) {
	fakeClaude(t, `exit 1`)
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

func TestClaudeCodeVersionEmptyOnUnparseableOutput(t *testing.T) {
	fakeClaude(t, `echo "hello there"`)
	require.Empty(t, probe.ClaudeCodeVersion(context.Background()))
}

// 卡住的 claude 不能把 agent 一起拖住。
func TestClaudeCodeVersionTimesOut(t *testing.T) {
	fakeClaude(t, `sleep 30`)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.Empty(t, probe.ClaudeCodeVersion(ctx))
}

func TestCollectFillsMachineInfo(t *testing.T) {
	fakeClaude(t, `echo "2.1.3 (Claude Code)"`)

	info := probe.Collect(context.Background(), "0.1.0")
	require.NotEmpty(t, info.Hostname)
	require.Equal(t, runtime.GOOS, info.OS)
	require.Equal(t, runtime.GOARCH, info.Arch)
	require.Equal(t, "0.1.0", info.AgentVersion)
	require.Equal(t, "2.1.3", info.ToolVersions["claude-code"])
}

func TestCollectOmitsToolVersionsWhenNothingFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	info := probe.Collect(context.Background(), "0.1.0")
	require.Empty(t, info.ToolVersions, "没探到就别塞空 map，wire 上少一个字段")
}
```

（补 `time` 的 import。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./agent/internal/probe/... -v`
Expected: FAIL —— `undefined: probe.ClaudeCodeVersion`

- [ ] **Step 3: 写实现**

创建 `agent/internal/probe/claude.go`：

```go
package probe

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/FlintyLemming/orciny/protocol"
)

// ClaudeProbeTimeout 是探测的硬上限。卡住的 claude 不能拖住 agent。
const ClaudeProbeTimeout = 3 * time.Second

// ToolClaudeCode 是 tool_versions 里的键名。
const ToolClaudeCode = "claude-code"

var versionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b`)

// ClaudeCodeVersion 执行 `claude --version` 并解析版本号。
//
// 任何失败都返回空串而不是错误：机器上没装 Claude Code 是合法状态，
// 产品文档 §4.2 允许「仅观测」的机器接入（spec §7.4）。
func ClaudeCodeVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, ClaudeProbeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return ""
	}
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return ""
	}
	return m[1]
}

// Collect 汇总一次上报所需的全部信息。
//
// 这是 M0 里 agent 读取本机信息的**全部**范围：主机名、平台、
// 以及一次 `claude --version`。除此之外不碰用户的任何文件，
// 尤其不碰 ~/.claude（spec §1.2 / §7.4）。
func Collect(ctx context.Context, agentVersion string) protocol.MachineInfo {
	hostname, goos, goarch := Host()

	info := protocol.MachineInfo{
		Hostname:     hostname,
		OS:           goos,
		Arch:         goarch,
		AgentVersion: agentVersion,
	}
	if v := ClaudeCodeVersion(ctx); v != "" {
		info.ToolVersions = map[string]string{ToolClaudeCode: v}
	}
	return info
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./agent/internal/probe/... -v`
Expected: PASS（9 个用例）

- [ ] **Step 5: 让连接使用 Collect**

修改 `agent/connect.go`（计划 5 建的），把手工拼 `protocol.MachineInfo` 换成 `probe.Collect(ctx, orciny.Version)`：

```go
	return conn.Dial(ctx, conn.Config{
		HubURL:           o.HubURL,
		Identity:         id,
		HubPub:           hubPub,
		AgentVersion:     orciny.Version,
		Info:             probe.Collect(ctx, orciny.Version),
		HandshakeTimeout: o.HandshakeTimeout,
		ReadTimeout:      o.ReadTimeout,
	})
```

Run: `make test` → 全绿

- [ ] **Step 6: 提交**

```bash
git add agent
git commit -m "feat: Claude Code 版本探测与机器信息汇总"
```

---

## Task 4: `run` / `status` 子命令与日志

**Files:**
- Create: `agent/internal/logging/logging.go`, `agent/internal/logging/logging_test.go`, `agent/status.go`, `agent/status_test.go`, `agent/run.go`
- Modify: `agent/cli.go`

**Interfaces:**
- Consumes: `conn.Client`、`agent.Connect`、`atomicfile.Write`
- Produces:
  ```go
  // agent/internal/logging
  func New(w io.Writer, level slog.Level) *slog.Logger
  func RedactToken(token string) string   // 只留前 8 位
  func FileWriter(dir string, retainDays int) (io.WriteCloser, error)

  // agent
  type Status struct {
      State       string    `json:"state"`
      Since       time.Time `json:"since"`
      HubURL      string    `json:"hub_url"`
      Fingerprint string    `json:"fingerprint"`
      Version     string    `json:"version"`
      LastError   string    `json:"last_error,omitempty"`
  }
  const StatusFileName = "status.json"
  func LoadStatus(dir string) (*Status, error)
  func SaveStatus(dir string, s *Status) error

  type RunOptions struct {
      Dir              string
      HandshakeTimeout time.Duration
      ReadTimeout      time.Duration
      Logger           *slog.Logger
  }
  func Run(ctx context.Context, o RunOptions) error
  ```

- [ ] **Step 1: 写日志与状态的失败测试**

创建 `agent/internal/logging/logging_test.go`：

```go
package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/logging"
)

func TestNewEmitsJSONLines(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)

	log.Info("已连接 hub", "fingerprint", "abc")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	require.Equal(t, "已连接 hub", line["msg"])
	require.Equal(t, "abc", line["fingerprint"])
	require.Equal(t, "INFO", line["level"])
}

func TestRedactTokenKeepsOnlyPrefix(t *testing.T) {
	require.Equal(t, "abcd1234", logging.RedactToken("abcd1234efghijklmnop"))
}

func TestRedactTokenHandlesShortInput(t *testing.T) {
	require.Equal(t, "abc", logging.RedactToken("abc"))
	require.Equal(t, "", logging.RedactToken(""))
}

func TestFileWriterCreatesDatedFile(t *testing.T) {
	dir := t.TempDir()
	w, err := logging.FileWriter(dir, 14)
	require.NoError(t, err)
	defer w.Close()

	_, err = w.Write([]byte("{\"msg\":\"x\"}\n"))
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), time.Now().Format("2006-01-02"))
}

func TestFileWriterPrunesOldLogs(t *testing.T) {
	dir := t.TempDir()

	old := filepath.Join(dir, "agent-2020-01-01.log")
	require.NoError(t, os.WriteFile(old, []byte("旧的"), 0o600))
	recent := filepath.Join(dir, "agent-"+time.Now().AddDate(0, 0, -3).Format("2006-01-02")+".log")
	require.NoError(t, os.WriteFile(recent, []byte("近的"), 0o600))

	w, err := logging.FileWriter(dir, 14)
	require.NoError(t, err)
	defer w.Close()

	require.NoFileExists(t, old, "超过保留期的日志应被清理")
	require.FileExists(t, recent)
}
```

创建 `agent/status_test.go`：

```go
package agent

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatusRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Status{
		State:       "connected",
		Since:       time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		HubURL:      "https://hub.example.com",
		Fingerprint: "abc",
		Version:     "0.1.0",
	}
	require.NoError(t, SaveStatus(dir, in))

	out, err := LoadStatus(dir)
	require.NoError(t, err)
	require.Equal(t, in.State, out.State)
	require.Equal(t, in.HubURL, out.HubURL)
	require.True(t, in.Since.Equal(out.Since))
}

func TestLoadStatusMissing(t *testing.T) {
	_, err := LoadStatus(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStatusFileNeverContainsKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveStatus(dir, &Status{State: "connected", Fingerprint: "abc"}))

	b, err := os.ReadFile(dir + "/" + StatusFileName)
	require.NoError(t, err)
	require.NotContains(t, string(b), "PRIVATE KEY")
	require.NotContains(t, string(b), "pub_key")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./agent/... -run 'Status|Logging|Redact|FileWriter' -v`
Expected: FAIL

- [ ] **Step 3: 写日志包**

创建 `agent/internal/logging/logging.go`：

```go
// Package logging 装配 agent 的日志。
//
// JSON lines 输出到 stdout，由 systemd / launchd 收集——这是默认且唯一
// 开启的通道。agent.yml 里可另行开启文件日志（spec §7.5）。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenPrefixLen 是 token 在日志里保留的位数。
//
// 这条规矩在 M0 就要立好：M1 引入凭据下发后，脱敏漏一处就是事故。
const TokenPrefixLen = 8

func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// RedactToken 只保留 token 的前 8 位，够在事件流里对上号，又泄不出去。
func RedactToken(token string) string {
	if len(token) <= TokenPrefixLen {
		return token
	}
	return token[:TokenPrefixLen]
}

// FileWriter 返回按天切分的日志写入器，并清理超过保留期的旧文件。
type fileWriter struct {
	dir        string
	retainDays int

	mu   sync.Mutex
	day  string
	file *os.File
}

func FileWriter(dir string, retainDays int) (io.WriteCloser, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("logging: 创建日志目录: %w", err)
	}
	w := &fileWriter{dir: dir, retainDays: retainDays}
	w.prune()
	if err := w.rotate(time.Now()); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *fileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	if now.Format("2006-01-02") != w.day {
		if err := w.rotateLocked(now); err != nil {
			return 0, err
		}
		w.prune()
	}
	return w.file.Write(p)
}

func (w *fileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}

func (w *fileWriter) rotate(now time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rotateLocked(now)
}

func (w *fileWriter) rotateLocked(now time.Time) error {
	if w.file != nil {
		_ = w.file.Close()
	}
	day := now.Format("2006-01-02")
	f, err := os.OpenFile(filepath.Join(w.dir, "agent-"+day+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logging: 打开日志文件: %w", err)
	}
	w.file, w.day = f, day
	return nil
}

// prune 删除超过保留期的日志。失败只忽略——日志清理不该拖垮 agent。
func (w *fileWriter) prune() {
	cutoff := time.Now().AddDate(0, 0, -w.retainDays)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".log")
		ts, err := time.Parse("2006-01-02", day)
		if err != nil {
			continue
		}
		if ts.Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, name))
		}
	}
}
```

- [ ] **Step 4: 写状态文件**

创建 `agent/status.go`：

```go
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// StatusFileName 是运行状态的落盘位置。
//
// 注意它**不是** spec §5.2 预留的 state.json —— 那个是 M1 的配置落盘状态，
// M0 不创建。这里只放「进程当前连着没有」，供 status 子命令读取。
const StatusFileName = "status.json"

// Status 是 agent 当前的运行状态。里面不含任何密钥材料。
type Status struct {
	State       string    `json:"state"`
	Since       time.Time `json:"since"`
	HubURL      string    `json:"hub_url"`
	Fingerprint string    `json:"fingerprint"`
	Version     string    `json:"version"`
	LastError   string    `json:"last_error,omitempty"`
}

func LoadStatus(dir string) (*Status, error) {
	b, err := os.ReadFile(filepath.Join(dir, StatusFileName))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist：调用方据此报「未运行」
	}
	var s Status
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", StatusFileName, err)
	}
	return &s, nil
}

func SaveStatus(dir string, s *Status) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化运行状态: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, StatusFileName), append(b, '\n'), 0o600)
}
```

- [ ] **Step 5: 写 `agent.Run`**

创建 `agent/run.go`：

```go
package agent

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// RunOptions 是 agent.Run 的入参。
type RunOptions struct {
	Dir              string
	HandshakeTimeout time.Duration // 0 → 10s
	ReadTimeout      time.Duration // 0 → 70s
	Logger           *slog.Logger
}

// Run 前台运行 agent，直到 ctx 取消或遇到终局错误。
// 服务单元调用的就是它（经由 `orciny-agent run`）。
func Run(ctx context.Context, o RunOptions) error {
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 70 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	cfg, err := LoadConfig(o.Dir)
	if err != nil {
		return err
	}
	id, err := identity.Load(filepath.Join(o.Dir, identity.DirName))
	if err != nil {
		return err
	}

	o.Logger.Info("agent 启动",
		"version", orciny.Version,
		"hub", cfg.HubURL,
		"fingerprint", id.Fingerprint())

	writeStatus := func(state conn.State, cause error) {
		s := &Status{
			State:       state.String(),
			Since:       time.Now().UTC(),
			HubURL:      cfg.HubURL,
			Fingerprint: id.Fingerprint(),
			Version:     orciny.Version,
		}
		if cause != nil {
			s.LastError = cause.Error()
		}
		if err := SaveStatus(o.Dir, s); err != nil {
			o.Logger.Warn("写运行状态失败", "error", err)
		}
	}

	client := conn.NewClient(conn.ClientConfig{
		Dial: func(ctx context.Context) (*Session, error) {
			return Connect(ctx, ConnectOptions{
				Dir:              o.Dir,
				HubURL:           cfg.HubURL,
				HandshakeTimeout: o.HandshakeTimeout,
				ReadTimeout:      o.ReadTimeout,
			})
		},
		Clock:   clock.System(),
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, newRand().Float64),
		Logger:  o.Logger,
		OnState: writeStatus,
	})

	return client.Run(ctx)
}

// newRand 用系统熵播种，保证同一时刻启动的多台 agent 抖动不同步。
func newRand() *rand.Rand {
	var seed [16]byte
	if _, err := rand.Read(seed[:]); err != nil {
		// 拿不到系统熵时退回时间播种：抖动的目的是打散，不是保密。
		return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	}
	return rand.New(rand.NewPCG(
		binary.LittleEndian.Uint64(seed[0:8]),
		binary.LittleEndian.Uint64(seed[8:16]),
	))
}
```

- [ ] **Step 6: 写 CLI 子命令**

修改 `agent/cli.go`，新增两个命令并在 `newRootCmd` 里注册：

```go
func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "前台运行 agent（服务单元调用的就是它）",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)

			cfg, err := LoadConfig(dir)
			if err != nil {
				return err
			}

			out := io.Writer(os.Stdout)
			if cfg.LogFile {
				fw, err := logging.FileWriter(filepath.Join(dir, "logs"), 14)
				if err != nil {
					return err
				}
				defer fw.Close()
				out = io.MultiWriter(os.Stdout, fw)
			}
			log := logging.New(out, slog.LevelInfo)

			// systemd 的 SIGTERM 与 Ctrl-C 都要能干净退出。
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			err = Run(ctx, RunOptions{Dir: dir, Logger: log})
			if errors.Is(err, context.Canceled) {
				log.Info("agent 已停止")
				return nil
			}
			return err
		},
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查看连接状态、hub 地址、指纹与版本",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			out := cmd.OutOrStdout()

			cfg, err := LoadConfig(dir)
			if err != nil {
				return err
			}
			id, err := identity.Load(filepath.Join(dir, identity.DirName))
			if err != nil {
				return err
			}

			fmt.Fprintf(out, "hub 地址   %s\n", cfg.HubURL)
			fmt.Fprintf(out, "机器指纹   %s\n", id.Fingerprint())
			fmt.Fprintf(out, "agent 版本 %s\n", orciny.Version)

			st, err := LoadStatus(dir)
			switch {
			case errors.Is(err, os.ErrNotExist):
				fmt.Fprintf(out, "连接状态   未运行（没有找到 %s）\n", StatusFileName)
			case err != nil:
				return err
			default:
				fmt.Fprintf(out, "连接状态   %s（自 %s）\n",
					st.State, st.Since.Local().Format(time.RFC3339))
				if st.LastError != "" {
					fmt.Fprintf(out, "最近错误   %s\n", st.LastError)
				}
			}
			return nil
		},
	}
}
```

在 `newRootCmd()` 里追加 `root.AddCommand(newRunCmd(), newStatusCmd())`，并补齐 import：`context` `errors` `fmt` `io` `log/slog` `os` `os/signal` `path/filepath` `syscall` `time`、`agent/internal/identity`、`agent/internal/logging`、根包 `orciny`。

- [ ] **Step 7: 写 CLI 测试**

在 `agent/cli_test.go` 追加：

```go
func TestStatusCommandReportsNotRunning(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://hub.example", MachineID: "m1"}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"status", "--dir", dir})
	require.NoError(t, root.Execute())

	require.Contains(t, out.String(), "https://hub.example")
	require.Contains(t, out.String(), "未运行")
	require.NotContains(t, out.String(), "PRIVATE KEY")
}

func TestStatusCommandReportsSavedState(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://hub.example", MachineID: "m1"}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)
	require.NoError(t, SaveStatus(dir, &Status{
		State: "connected", Since: time.Now().UTC(),
		HubURL: "https://hub.example", Version: "0.1.0",
	}))

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"status", "--dir", dir})
	require.NoError(t, root.Execute())

	require.Contains(t, out.String(), "connected")
}

func TestStatusCommandFailsWithoutEnroll(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"status", "--dir", t.TempDir()})
	require.Error(t, root.Execute(), "还没 enroll 时应当明确报错")
}
```

（补 `path/filepath`、`time`、`agent/internal/identity` 的 import。）

- [ ] **Step 8: 写端到端「掉线自动重连」用例**

在 `internal/testsupport/lifecycle_test.go` 追加：

```go
// agent.Run 的完整装配：从 agent.yml + identity 起步，连上 hub 让面板转
// online，收到取消信号后干净退出。退避时长由 Task 2 的假时钟用例断言，
// 这里只验证「线接对了」。
func TestAgentRunConnectsAndStopsCleanly(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, agent.RunOptions{
			Dir:              ta.Dir,
			HandshakeTimeout: 2 * time.Second,
			ReadTimeout:      2 * time.Second,
		})
	}()

	th.RequireStatus(t, ta.MachineID, "online")

	// 运行状态已落盘，status 子命令据此报告
	require.Eventually(t, func() bool {
		s, err := agent.LoadStatus(ta.Dir)
		return err == nil && s.State == "connected"
	}, 3*time.Second, 10*time.Millisecond)

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}
```

> **说明：** `agent.Run` 内部用的是 `clock.System()`，因此这条用例不去验证退避时长——那已由 Task 2 的假时钟用例逐项断言过。**不要**在这里加 `time.Sleep` 去等重连。

- [ ] **Step 9: 运行全部测试**

Run: `make test && make lint`
Expected: 全绿

- [ ] **Step 10: 手工验证 CLI**

```bash
go build -o /tmp/orciny-agent ./cmd/orciny-agent
ORCINY_HOME=/tmp/orciny-home /tmp/orciny-agent version
ORCINY_HOME=/tmp/orciny-home /tmp/orciny-agent status   # 期望：明确报「尚未 enroll」
rm -rf /tmp/orciny-home /tmp/orciny-agent
```

- [ ] **Step 11: 提交**

```bash
git add agent internal/testsupport
git commit -m "feat: run/status 子命令、JSON 日志与运行状态落盘"
```

---

## 完成检查

spec §12.2「agent 重连」四条用例逐条对照：

- [ ] 指数退避序列符合预期（注入假时钟）—— `TestNetworkErrorsBackOffExponentially`
- [ ] 抖动落在 ±20% 内 —— `TestJitterStaysWithinTwentyPercent` + `TestJitterAppliesAtMaxToo`
- [ ] 连接成功后退避重置 —— `TestBackoffResetGoesBackToBase` + `TestBackoffResetsAfterSuccessfulConnection`
- [ ] 各 `AuthResult.Code` 对应正确的重试策略 —— `TestRejectedCodesUseFixedInterval` + `TestMachineRemovedStopsRetrying` + `TestHubSignatureFailureEntersCompromised`

另外：

- [ ] `claude --version` 失败留空不报错、超时不拖住 agent
- [ ] `status` 输出不含任何密钥材料
- [ ] token 在日志中只记前 8 位
- [ ] `go list -deps ./agent/... | grep orciny/hub` 无输出
