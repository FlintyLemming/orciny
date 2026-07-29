# M0 计划 5 · WS 握手 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** agent 与 hub 建立经双向验证的长连接：WS 升级、双向挑战-应答、原因码分流、握手超时、未知 Kind 忽略；agent 在 hub 签名不符时进入 `Compromised` 终态。

**Architecture:** 握手逻辑写成**纯状态机**（`handshake.Session.Handle(env) → (reply, outcome)`），不碰网络、不碰时钟，因此可以在包内穷举所有分支；网络与超时留在 `ws` 包里，用真实 deadline（可配置为毫秒级）。连接注册表的接口先在 `machines` 包里立好并给一个 Nop 实现，计划 6 填真身——这样 ws 不必在两个计划之间改结构。

**Tech Stack:** Go 1.26 · gws v1.10.1 · PocketBase v0.39.9 · blang/semver v4.0.0

**上位文档：** [统括计划](00-overview.md) · [spec §4.2 §4.6 §3.2 §6.2](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；共享只走 `protocol/`；`hub/internal/*` 的单测写在包内。
- 测试禁用 `time.Sleep`。gws 的读写 deadline 走真实时间，测试里通过 `hub.Config` / `conn.Config` 压到毫秒级；5 秒宽限与退避这类逻辑时间走 `clock.Fake`。
- 未知 `Kind`：记 warn 日志后丢弃，**绝不断开连接**。
- 私钥、hub 公钥内容不入日志；日志里只出现指纹。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `hub/internal/handshake/server.go` | 纯状态机：`Server` / `Session` / `Outcome` |
| `hub/internal/handshake/store.go` | `Store` 接口与基于 PocketBase 的实现 |
| `hub/internal/handshake/server_test.go` | 状态机的分支穷举（无网络、无时钟） |
| `hub/internal/machines/registry.go` | `Conn` / `Registry` 接口 + `NopRegistry`（计划 6 填 `Manager`） |
| `hub/internal/ws/conn.go` | 连接对象：会话状态、发送、关闭 |
| `hub/internal/ws/handler.go` | gws 事件处理、升级入口、deadline 与心跳 |
| `hub/internal/ws/ws_test.go` | 包内集成测试（起 httptest + 真 WS 客户端） |
| `hub/internal/routes/routes.go` | 修改：注册 `GET /api/orciny/ws` |
| `hub/hub.go` | 修改：装配 handshake / ws |
| `agent/internal/conn/dial.go` | agent 侧握手与会话 |
| `agent/internal/conn/dial_test.go` | 包内单测 |
| `internal/testsupport/agent.go` | 修改：`(*TestAgent).Connect` |

---

## Task 1: 握手状态机

**Files:**
- Create: `hub/internal/handshake/store.go`, `hub/internal/handshake/server.go`, `hub/internal/handshake/server_test.go`

**Interfaces:**
- Consumes: `protocol.*`、`identity.Store`（作为 `Signer`）
- Produces:
  ```go
  type Machine struct {
      ID     string
      PubKey ed25519.PublicKey
  }
  type Store interface {
      FindByFingerprint(fingerprint string) (*Machine, error)
  }
  var ErrNotFound = errors.New("handshake: 指纹未登记")
  func NewAppStore(app core.App) Store

  type Signer interface{ Sign(msg []byte) []byte }

  type Server struct{ /* 私有字段 */ }
  func NewServer(store Store, signer Signer, minVersion semver.Version, rnd io.Reader) *Server
  func (s *Server) NewSession() *Session

  type Outcome struct {
      OK          bool
      MachineID   string
      Fingerprint string
      Code        uint8
      Reason      string
  }
  type Session struct{ /* 私有字段 */ }
  func (ss *Session) Handle(env protocol.Envelope) (reply []byte, out *Outcome, err error)
  func (ss *Session) Done() bool
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/handshake/server_test.go`：

```go
package handshake_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/protocol"
)

type mapStore map[string]*handshake.Machine

func (m mapStore) FindByFingerprint(fp string) (*handshake.Machine, error) {
	if v, ok := m[fp]; ok {
		return v, nil
	}
	return nil, handshake.ErrNotFound
}

type keySigner struct{ priv ed25519.PrivateKey }

func (k keySigner) Sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

type harness struct {
	server   *handshake.Server
	hubPub   ed25519.PublicKey
	agentPub ed25519.PublicKey
	agentKey ed25519.PrivateKey
	fp       string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	hubPub, hubPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	agentPub, agentPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	fp := protocol.Fingerprint(agentPub)
	store := mapStore{fp: {ID: "m1", PubKey: agentPub}}

	return &harness{
		server:   handshake.NewServer(store, keySigner{hubPriv}, semver.MustParse("0.1.0"), rand.Reader),
		hubPub:   hubPub,
		agentPub: agentPub,
		agentKey: agentPriv,
		fp:       fp,
	}
}

func (h *harness) hello(t *testing.T, version, fp string, nonce []byte) protocol.Envelope {
	t.Helper()
	b, err := protocol.Encode(protocol.KindHello, nil, protocol.Hello{
		AgentVersion: version, Fingerprint: fp, ClientNonce: nonce,
	})
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}

func decodeReply[T any](t *testing.T, reply []byte) T {
	t.Helper()
	env, err := protocol.Decode(reply)
	require.NoError(t, err)
	out, err := protocol.DecodePayload[T](env)
	require.NoError(t, err)
	return out
}

func TestHappyPath(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	clientNonce, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)

	reply, out, err := ss.Handle(h.hello(t, "0.1.0", h.fp, clientNonce))
	require.NoError(t, err)
	require.Nil(t, out, "第一步不该有结论")
	require.False(t, ss.Done())

	ch := decodeReply[protocol.Challenge](t, reply)
	require.Len(t, ch.ServerNonce, protocol.NonceSize)
	require.True(t,
		ed25519.Verify(h.hubPub, protocol.HubSigPayload(clientNonce, ch.ServerNonce), ch.HubSig),
		"hub 必须对两个 nonce 签名")

	sig := ed25519.Sign(h.agentKey, protocol.AgentSigPayload(ch.ServerNonce, clientNonce))
	authBytes, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: sig})
	require.NoError(t, err)
	authEnv, err := protocol.Decode(authBytes)
	require.NoError(t, err)

	reply, out, err = ss.Handle(authEnv)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.True(t, out.OK)
	require.Equal(t, "m1", out.MachineID)
	require.Equal(t, h.fp, out.Fingerprint)
	require.True(t, ss.Done())

	res := decodeReply[protocol.AuthResult](t, reply)
	require.True(t, res.OK)
}

func TestUnknownFingerprintIsRejected(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	reply, out, err := ss.Handle(h.hello(t, "0.1.0", "从没见过的指纹1234567890123456", nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, out.Code)
	require.True(t, ss.Done())

	res := decodeReply[protocol.AuthResult](t, reply)
	require.False(t, res.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, res.Code)
	require.NotEmpty(t, res.Reason, "要给排查者留一句人话")
}

func TestVersionTooOldIsRejectedBeforeLookup(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	// 版本检查在查库之前：指纹是合法的，但版本不够。
	_, out, err := ss.Handle(h.hello(t, "0.0.9", h.fp, nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, protocol.CodeVersionTooOld, out.Code)
}

func TestUnparseableVersionIsRejected(t *testing.T) {
	h := newHarness(t)
	nonce, _ := protocol.NewNonce(rand.Reader)

	_, out, err := h.server.NewSession().Handle(h.hello(t, "不是版本号", h.fp, nonce))
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, protocol.CodeVersionTooOld, out.Code)
}

func TestBadAgentSignatureIsRejected(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	_, _, err := ss.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)

	bad, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: bytes.Repeat([]byte{9}, 64)})
	require.NoError(t, err)
	env, err := protocol.Decode(bad)
	require.NoError(t, err)

	reply, out, err := ss.Handle(env)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeBadSignature, out.Code)
	require.Equal(t, h.fp, out.Fingerprint, "结论里要带指纹，否则事件写不出是谁")

	res := decodeReply[protocol.AuthResult](t, reply)
	require.Equal(t, protocol.CodeBadSignature, res.Code)
}

// 截获一次完整握手的 Auth，重连时重放 —— 必须失败，
// 因为 serverNonce 每个会话都新生成（spec §4.2）。
func TestReplayedAuthFails(t *testing.T) {
	h := newHarness(t)

	first := h.server.NewSession()
	nonce, _ := protocol.NewNonce(rand.Reader)
	reply, _, err := first.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)
	ch := decodeReply[protocol.Challenge](t, reply)

	capturedSig := ed25519.Sign(h.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce))
	captured, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: capturedSig})
	require.NoError(t, err)
	capturedEnv, err := protocol.Decode(captured)
	require.NoError(t, err)

	// 新会话：同样的 clientNonce、同样的 Auth 报文
	second := h.server.NewSession()
	reply2, _, err := second.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.NoError(t, err)
	ch2 := decodeReply[protocol.Challenge](t, reply2)
	require.NotEqual(t, ch.ServerNonce, ch2.ServerNonce, "serverNonce 必须每次新生成")

	_, out, err := second.Handle(capturedEnv)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.False(t, out.OK)
	require.Equal(t, protocol.CodeBadSignature, out.Code)
}

func TestWrongMessageOrderIsProtocolError(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	// 上来就发 Auth
	b, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{AgentSig: make([]byte, 64)})
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	_, out, err := ss.Handle(env)
	require.Error(t, err, "顺序错乱是协议违规，由调用方直接断开")
	require.Nil(t, out)
}

func TestHandleAfterDoneIsError(t *testing.T) {
	h := newHarness(t)
	ss := h.server.NewSession()

	nonce, _ := protocol.NewNonce(rand.Reader)
	_, _, err := ss.Handle(h.hello(t, "0.0.1", h.fp, nonce))
	require.NoError(t, err)
	require.True(t, ss.Done())

	_, _, err = ss.Handle(h.hello(t, "0.1.0", h.fp, nonce))
	require.Error(t, err)
}

func TestShortClientNonceIsRejected(t *testing.T) {
	h := newHarness(t)
	_, out, err := h.server.NewSession().Handle(h.hello(t, "0.1.0", h.fp, []byte{1, 2, 3}))
	require.Error(t, err, "nonce 长度不对说明对面不是我们的 agent，直接断开")
	require.Nil(t, out)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/handshake/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写 Store**

创建 `hub/internal/handshake/store.go`：

```go
// Package handshake 实现 hub 侧的双向挑战-应答（spec §4.2）。
//
// 状态机是纯的：不碰网络、不碰时钟、不写库。它吃一个信封，吐一个要发出去的
// 信封和（可能的）结论。超时、断开、事件写入都留给 ws 包。
// 这样握手的全部分支都能在包内穷举，而不必去搭一个真连接。
package handshake

import (
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/protocol"
)

// ErrNotFound 表示该指纹没有对应的机器记录。
var ErrNotFound = errors.New("handshake: 指纹未登记")

// Machine 是握手需要知道的全部机器信息。
type Machine struct {
	ID     string
	PubKey ed25519.PublicKey
}

// Store 按指纹查机器。
type Store interface {
	FindByFingerprint(fingerprint string) (*Machine, error)
}

// Signer 签名握手挑战，由 hub 的 identity.Store 实现。
type Signer interface {
	Sign(msg []byte) []byte
}

type appStore struct{ app core.App }

// NewAppStore 返回基于 PocketBase 的 Store 实现。
func NewAppStore(app core.App) Store { return appStore{app: app} }

func (s appStore) FindByFingerprint(fingerprint string) (*Machine, error) {
	rec, err := s.app.FindFirstRecordByData("machines", "fingerprint", fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("handshake: 查询机器: %w", err)
	}
	pub, err := protocol.DecodePublicKey(rec.GetString("pub_key"))
	if err != nil {
		// 库里的公钥坏了：这台机器无法验证，等同于未登记。
		return nil, fmt.Errorf("handshake: 机器 %s 的公钥无法解析: %w", rec.Id, err)
	}
	return &Machine{ID: rec.Id, PubKey: pub}, nil
}
```

- [ ] **Step 4: 写状态机**

创建 `hub/internal/handshake/server.go`：

```go
package handshake

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"

	"github.com/blang/semver/v4"

	"github.com/FlintyLemming/orciny/protocol"
)

// Server 持有握手的不变部分，按连接派生 Session。
type Server struct {
	store      Store
	signer     Signer
	minVersion semver.Version
	rnd        io.Reader
}

func NewServer(store Store, signer Signer, minVersion semver.Version, rnd io.Reader) *Server {
	return &Server{store: store, signer: signer, minVersion: minVersion, rnd: rnd}
}

// Outcome 是握手的结论。非 nil 即表示握手已结束——OK 为真则连接可用，
// 为假则调用方应先把 reply 发出去（让 agent 知道原因），再关闭连接。
type Outcome struct {
	OK          bool
	MachineID   string
	Fingerprint string
	Code        uint8
	Reason      string
}

type state uint8

const (
	stateAwaitHello state = iota
	stateAwaitAuth
	stateDone
)

// Session 是一次握手的状态。非并发安全——每个连接一个，由该连接的读循环独占。
type Session struct {
	srv *Server

	state       state
	fingerprint string
	machine     *Machine
	clientNonce []byte
	serverNonce []byte
}

func (s *Server) NewSession() *Session {
	return &Session{srv: s, state: stateAwaitHello}
}

// Done 报告握手是否已结束（无论成败）。
func (ss *Session) Done() bool { return ss.state == stateDone }

// Handle 处理一条入站信封。
//
// 返回的 error 表示**协议违规**（顺序错乱、字段长度不对）——调用方应直接
// 断开，不必回复：对面不是我们的 agent，多说无益。
// 返回的 Outcome 表示握手有了结论，此时 reply 一定非空。
func (ss *Session) Handle(env protocol.Envelope) ([]byte, *Outcome, error) {
	switch ss.state {
	case stateAwaitHello:
		return ss.handleHello(env)
	case stateAwaitAuth:
		return ss.handleAuth(env)
	default:
		return nil, nil, errors.New("handshake: 握手已结束，不应再收到握手消息")
	}
}

func (ss *Session) handleHello(env protocol.Envelope) ([]byte, *Outcome, error) {
	if env.Kind != protocol.KindHello {
		return nil, nil, fmt.Errorf("handshake: 期望 hello，收到 %s", env.Kind)
	}
	hello, err := protocol.DecodePayload[protocol.Hello](env)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 解析 hello: %w", err)
	}
	if len(hello.ClientNonce) != protocol.NonceSize {
		return nil, nil, fmt.Errorf("handshake: clientNonce 长度应为 %d，实际 %d",
			protocol.NonceSize, len(hello.ClientNonce))
	}

	// 版本门槛在查库之前：低版本 agent 不该消耗一次数据库查询。
	v, err := semver.Parse(hello.AgentVersion)
	if err != nil || v.LT(ss.srv.minVersion) {
		return ss.reject(hello.Fingerprint, protocol.CodeVersionTooOld,
			fmt.Sprintf("agent 版本过旧，需 >= %s", ss.srv.minVersion))
	}

	machine, err := ss.srv.store.FindByFingerprint(hello.Fingerprint)
	if err != nil {
		return ss.reject(hello.Fingerprint, protocol.CodeUnknownFingerprint,
			"该指纹未登记，请重新执行 orciny-agent enroll")
	}

	serverNonce, err := protocol.NewNonce(ss.srv.rnd)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 生成 serverNonce: %w", err)
	}

	ss.fingerprint = hello.Fingerprint
	ss.machine = machine
	ss.clientNonce = hello.ClientNonce
	ss.serverNonce = serverNonce
	ss.state = stateAwaitAuth

	reply, err := protocol.Encode(protocol.KindChallenge, nil, protocol.Challenge{
		ServerNonce: serverNonce,
		HubSig:      ss.srv.signer.Sign(protocol.HubSigPayload(hello.ClientNonce, serverNonce)),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码 challenge: %w", err)
	}
	return reply, nil, nil
}

func (ss *Session) handleAuth(env protocol.Envelope) ([]byte, *Outcome, error) {
	if env.Kind != protocol.KindAuth {
		return nil, nil, fmt.Errorf("handshake: 期望 auth，收到 %s", env.Kind)
	}
	auth, err := protocol.DecodePayload[protocol.Auth](env)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 解析 auth: %w", err)
	}

	payload := protocol.AgentSigPayload(ss.serverNonce, ss.clientNonce)
	if !ed25519.Verify(ss.machine.PubKey, payload, auth.AgentSig) {
		return ss.reject(ss.fingerprint, protocol.CodeBadSignature, "签名验证失败")
	}

	ss.state = stateDone
	reply, err := protocol.Encode(protocol.KindAuthResult, nil, protocol.AuthResult{OK: true})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码 auth_result: %w", err)
	}
	return reply, &Outcome{
		OK:          true,
		MachineID:   ss.machine.ID,
		Fingerprint: ss.fingerprint,
	}, nil
}

func (ss *Session) reject(fingerprint string, code uint8, reason string) ([]byte, *Outcome, error) {
	ss.state = stateDone
	reply, err := protocol.Encode(protocol.KindAuthResult, nil, protocol.AuthResult{
		OK:     false,
		Reason: reason,
		Code:   code,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码拒绝响应: %w", err)
	}
	return reply, &Outcome{
		OK:          false,
		Fingerprint: fingerprint,
		Code:        code,
		Reason:      reason,
	}, nil
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./hub/internal/handshake/... -v`
Expected: PASS（9 个用例）

- [ ] **Step 6: 提交**

```bash
git add hub/internal/handshake
git commit -m "feat: hub 侧握手状态机与指纹查询"
```

---

## Task 2: 连接注册表接口

极小的一个任务，但必须单独做：它决定 ws 与 machines 之间的依赖方向，方向错了计划 6 要返工。

**Files:**
- Create: `hub/internal/machines/registry.go`, `hub/internal/machines/registry_test.go`

**Interfaces:**
- Consumes: `protocol.MachineInfo`
- Produces:
  ```go
  type Conn interface {
      Fingerprint() string
      MachineID() string
      RemoteAddr() string
      SendAuthResult(ok bool, reason string, code uint8) error
      Close(code uint16, reason string) error
  }
  type Registry interface {
      Register(c Conn) error
      Unregister(c Conn)
      UpdateInfo(c Conn, info protocol.MachineInfo) error
  }
  type NopRegistry struct{}
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/machines/registry_test.go`：

```go
package machines_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/protocol"
)

type stubConn struct{ fp string }

func (s stubConn) Fingerprint() string                        { return s.fp }
func (s stubConn) MachineID() string                          { return "m1" }
func (s stubConn) RemoteAddr() string                         { return "127.0.0.1:1" }
func (s stubConn) SendAuthResult(bool, string, uint8) error   { return nil }
func (s stubConn) Close(uint16, string) error                 { return nil }

func TestNopRegistrySatisfiesRegistry(t *testing.T) {
	var r machines.Registry = machines.NopRegistry{}
	c := stubConn{fp: "fp"}

	require.NoError(t, r.Register(c))
	require.NoError(t, r.UpdateInfo(c, protocol.MachineInfo{Hostname: "box"}))
	r.Unregister(c) // 不 panic 即可
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/machines/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写接口**

创建 `hub/internal/machines/registry.go`：

```go
// Package machines 维护连接注册表与机器的在线状态机。
//
// 依赖方向：ws 依赖 machines，machines 不依赖 ws。因此这里的 Conn 是接口，
// *ws.Conn 结构性地满足它。这样 machines 的单测可以用假连接推演时序，
// 不必真的开一个 WebSocket。
package machines

import "github.com/FlintyLemming/orciny/protocol"

// Conn 是注册表眼中的一条连接。
type Conn interface {
	Fingerprint() string
	MachineID() string
	RemoteAddr() string
	// SendAuthResult 用于事后的「授权已撤销」通知（spec §3.3）。
	SendAuthResult(ok bool, reason string, code uint8) error
	Close(code uint16, reason string) error
}

// Registry 是 ws 对注册表的全部需求。计划 6 由 *Manager 实现。
type Registry interface {
	// Register 在握手成功后登记连接。同一指纹已有连接时，新连接胜出。
	Register(c Conn) error
	// Unregister 在连接关闭时调用。是否真的判定离线由实现决定（5 秒宽限）。
	Unregister(c Conn)
	// UpdateInfo 处理握手后的首条业务消息。
	UpdateInfo(c Conn, info protocol.MachineInfo) error
}

// NopRegistry 什么都不做，供计划 5 的 ws 在 Manager 就位之前使用，
// 以及供只关心握手的测试使用。
type NopRegistry struct{}

func (NopRegistry) Register(Conn) error                       { return nil }
func (NopRegistry) Unregister(Conn)                           {}
func (NopRegistry) UpdateInfo(Conn, protocol.MachineInfo) error { return nil }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./hub/internal/machines/... -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/machines
git commit -m "feat: 连接注册表接口与空实现"
```

---

## Task 3: WS 端点

**Files:**
- Create: `hub/internal/ws/conn.go`, `hub/internal/ws/handler.go`, `hub/internal/ws/ws_test.go`
- Modify: `hub/internal/routes/routes.go`（`Deps` 加 `WS`，注册 `GET /ws`）, `hub/hub.go`（装配）

**Interfaces:**
- Consumes: `handshake.Server`、`machines.Registry`、`events.Writer`、`clock.Clock`
- Produces:
  ```go
  type Deps struct {
      App               core.App
      Handshake         *handshake.Server
      Registry          machines.Registry
      Events            *events.Writer
      Clock             clock.Clock
      HandshakeTimeout  time.Duration
      ReadTimeout       time.Duration
      HeartbeatInterval time.Duration
  }
  type Handler struct{ /* 私有字段 */ }
  func NewHandler(d Deps) *Handler
  func (h *Handler) Upgrade(e *core.RequestEvent) error   // 挂到 GET /api/orciny/ws

  type Conn struct{ /* 私有字段 */ }
  func (c *Conn) Fingerprint() string
  func (c *Conn) MachineID() string
  func (c *Conn) RemoteAddr() string
  func (c *Conn) Send(kind protocol.Kind, payload any) error
  func (c *Conn) SendAuthResult(ok bool, reason string, code uint8) error
  func (c *Conn) Close(code uint16, reason string) error
  ```

- [ ] **Step 1: 拉依赖**

```bash
go get github.com/lxzan/gws@v1.10.1
```

- [ ] **Step 2: 写失败的测试**

创建 `hub/internal/ws/ws_test.go`：

```go
package ws_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver/v4"
	"github.com/lxzan/gws"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/hub/internal/ws"
	"github.com/FlintyLemming/orciny/protocol"
)

// —— 测试用的极简 WS 客户端 ——
// 只负责收发信封，握手逻辑由测试自己写，这样每一步都能单独出错。

type rawClient struct {
	conn   *gws.Conn
	inbox  chan protocol.Envelope
	closed chan struct{}
}

type rawHandler struct {
	gws.BuiltinEventHandler
	inbox  chan protocol.Envelope
	closed chan struct{}
}

func (h *rawHandler) OnMessage(_ *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		return
	}
	select {
	case h.inbox <- env:
	default:
	}
}

func (h *rawHandler) OnClose(*gws.Conn, error) {
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
}

func dial(t *testing.T, url string) *rawClient {
	t.Helper()
	h := &rawHandler{inbox: make(chan protocol.Envelope, 8), closed: make(chan struct{})}
	conn, _, err := gws.NewClient(h, &gws.ClientOption{Addr: url})
	require.NoError(t, err)
	go conn.ReadLoop()
	t.Cleanup(func() { _ = conn.WriteClose(1000, nil) })
	return &rawClient{conn: conn, inbox: h.inbox, closed: h.closed}
}

func (c *rawClient) send(t *testing.T, kind protocol.Kind, payload any) {
	t.Helper()
	b, err := protocol.Encode(kind, nil, payload)
	require.NoError(t, err)
	require.NoError(t, c.conn.WriteMessage(gws.OpcodeBinary, b))
}

func (c *rawClient) recv(t *testing.T) protocol.Envelope {
	t.Helper()
	select {
	case env := <-c.inbox:
		return env
	case <-time.After(2 * time.Second):
		t.Fatal("等待 hub 的响应超时")
		return protocol.Envelope{}
	}
}

// —— 服务端夹具 ——

type fixture struct {
	url      string
	agentKey ed25519.PrivateKey
	fp       string
	hubPub   ed25519.PublicKey
	registry *recordingRegistry
}

type recordingRegistry struct {
	machines.NopRegistry
	registered chan string
	infos      chan protocol.MachineInfo
}

func (r *recordingRegistry) Register(c machines.Conn) error {
	select {
	case r.registered <- c.Fingerprint():
	default:
	}
	return nil
}

func (r *recordingRegistry) UpdateInfo(_ machines.Conn, info protocol.MachineInfo) error {
	select {
	case r.infos <- info:
	default:
	}
	return nil
}

type mapStore map[string]*handshake.Machine

func (m mapStore) FindByFingerprint(fp string) (*handshake.Machine, error) {
	if v, ok := m[fp]; ok {
		return v, nil
	}
	return nil, handshake.ErrNotFound
}

type keySigner struct{ priv ed25519.PrivateKey }

func (k keySigner) Sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

func newFixture(t *testing.T, handshakeTimeout time.Duration) *fixture {
	t.Helper()

	hubPub, hubPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	agentPub, agentPriv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fp := protocol.Fingerprint(agentPub)

	reg := &recordingRegistry{
		registered: make(chan string, 4),
		infos:      make(chan protocol.MachineInfo, 4),
	}

	h := ws.NewHandler(ws.Deps{
		Handshake: handshake.NewServer(
			mapStore{fp: {ID: "m1", PubKey: agentPub}},
			keySigner{hubPriv},
			semver.MustParse("0.1.0"),
			rand.Reader,
		),
		Registry:          reg,
		HandshakeTimeout:  handshakeTimeout,
		ReadTimeout:       2 * time.Second,
		HeartbeatInterval: time.Hour, // 本任务不测心跳
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, h.UpgradeHTTP(w, r))
	}))
	t.Cleanup(srv.Close)

	return &fixture{
		url:      "ws" + strings.TrimPrefix(srv.URL, "http"),
		agentKey: agentPriv,
		fp:       fp,
		hubPub:   hubPub,
		registry: reg,
	}
}

// —— 用例 ——

func TestFullHandshakeRegistersConnection(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})

	env := c.recv(t)
	require.Equal(t, protocol.KindChallenge, env.Kind)
	ch, err := protocol.DecodePayload[protocol.Challenge](env)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(f.hubPub, protocol.HubSigPayload(nonce, ch.ServerNonce), ch.HubSig))

	c.send(t, protocol.KindAuth, protocol.Auth{
		AgentSig: ed25519.Sign(f.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce)),
	})

	env = c.recv(t)
	require.Equal(t, protocol.KindAuthResult, env.Kind)
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	require.NoError(t, err)
	require.True(t, res.OK)

	select {
	case fp := <-f.registry.registered:
		require.Equal(t, f.fp, fp)
	case <-time.After(2 * time.Second):
		t.Fatal("连接未被登记进注册表")
	}

	// 握手后的首条业务消息
	c.send(t, protocol.KindMachineInfo, protocol.MachineInfo{
		Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
	})
	select {
	case info := <-f.registry.infos:
		require.Equal(t, "box", info.Hostname)
	case <-time.After(2 * time.Second):
		t.Fatal("MachineInfo 未被处理")
	}
}

func TestUnknownFingerprintGetsCodeAndClose(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: "没登记过的指纹00000000000000", ClientNonce: nonce,
	})

	env := c.recv(t)
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, protocol.CodeUnknownFingerprint, res.Code)

	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("被拒绝后连接应当关闭")
	}
}

func TestVersionTooOldGetsCode(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.0.1", Fingerprint: f.fp, ClientNonce: nonce,
	})

	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.Equal(t, protocol.CodeVersionTooOld, res.Code)
}

func TestBadSignatureGetsCode(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})
	_ = c.recv(t) // challenge

	c.send(t, protocol.KindAuth, protocol.Auth{AgentSig: make([]byte, ed25519.SignatureSize)})

	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.Equal(t, protocol.CodeBadSignature, res.Code)
}

// 升级后 N 毫秒不发 Auth，连接必须被关掉，防止半开连接堆积（spec §4.2）。
func TestHandshakeTimeoutClosesConnection(t *testing.T) {
	f := newFixture(t, 200*time.Millisecond)
	c := dial(t, f.url)

	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("握手超时未生效")
	}
}

// 未知 Kind 记 warn 后丢弃，连接照常（spec §3.2）。
func TestUnknownKindIsIgnoredNotFatal(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	nonce, _ := protocol.NewNonce(rand.Reader)
	c.send(t, protocol.KindHello, protocol.Hello{
		AgentVersion: "0.1.0", Fingerprint: f.fp, ClientNonce: nonce,
	})
	ch, err := protocol.DecodePayload[protocol.Challenge](c.recv(t))
	require.NoError(t, err)

	// 塞一条未来版本才有的消息
	c.send(t, protocol.Kind(99), map[string]string{"x": "y"})

	// 连接仍然可用，握手照常完成
	c.send(t, protocol.KindAuth, protocol.Auth{
		AgentSig: ed25519.Sign(f.agentKey, protocol.AgentSigPayload(ch.ServerNonce, nonce)),
	})
	res, err := protocol.DecodePayload[protocol.AuthResult](c.recv(t))
	require.NoError(t, err)
	require.True(t, res.OK)

	select {
	case <-c.closed:
		t.Fatal("未知 Kind 不该导致断开")
	default:
	}
}

func TestGarbageFrameClosesConnection(t *testing.T) {
	f := newFixture(t, 2*time.Second)
	c := dial(t, f.url)

	require.NoError(t, c.conn.WriteMessage(gws.OpcodeBinary, []byte{0xff, 0xff, 0xff}))

	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("无法解码的帧应当导致断开——对面不是我们的 agent")
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./hub/internal/ws/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 4: 写连接对象**

创建 `hub/internal/ws/conn.go`：

```go
// Package ws 负责 WebSocket 升级、连接对象与读写循环。
// 握手的判断逻辑在 handshake 包，在线状态的判断逻辑在 machines 包——
// 这里只做「搬运」与「超时」。
package ws

import (
	"sync"

	"github.com/lxzan/gws"

	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/protocol"
)

// Conn 是一条已升级的连接。它结构性地满足 machines.Conn。
type Conn struct {
	socket  *gws.Conn
	session *handshake.Session

	mu          sync.RWMutex
	fingerprint string
	machineID   string
	authed      bool

	stopHeartbeat chan struct{}
	stopOnce      sync.Once
}

func (c *Conn) Fingerprint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fingerprint
}

func (c *Conn) MachineID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.machineID
}

func (c *Conn) RemoteAddr() string { return c.socket.RemoteAddr().String() }

func (c *Conn) authenticated() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.authed
}

func (c *Conn) markAuthed(fingerprint, machineID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fingerprint = fingerprint
	c.machineID = machineID
	c.authed = true
}

// Send 发一条信封。
func (c *Conn) Send(kind protocol.Kind, payload any) error {
	b, err := protocol.Encode(kind, nil, payload)
	if err != nil {
		return err
	}
	return c.socket.WriteMessage(gws.OpcodeBinary, b)
}

// SendAuthResult 发裁决消息。握手完成后再发 OK:false，即「授权已撤销」通知。
func (c *Conn) SendAuthResult(ok bool, reason string, code uint8) error {
	return c.Send(protocol.KindAuthResult, protocol.AuthResult{OK: ok, Reason: reason, Code: code})
}

// Close 关闭连接。
func (c *Conn) Close(code uint16, reason string) error {
	c.stopHeartbeatLoop()
	return c.socket.WriteClose(code, []byte(reason))
}

func (c *Conn) stopHeartbeatLoop() {
	c.stopOnce.Do(func() {
		if c.stopHeartbeat != nil {
			close(c.stopHeartbeat)
		}
	})
}
```

- [ ] **Step 5: 写事件处理与升级入口**

创建 `hub/internal/ws/handler.go`：

```go
package ws

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/lxzan/gws"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/handshake"
	"github.com/FlintyLemming/orciny/hub/internal/machines"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// Deps 是 ws 需要的全部依赖。
type Deps struct {
	App       core.App
	Handshake *handshake.Server
	Registry  machines.Registry
	Events    *events.Writer
	Clock     clock.Clock

	HandshakeTimeout  time.Duration
	ReadTimeout       time.Duration
	HeartbeatInterval time.Duration
}

// Handler 是 WS 端点。一个 hub 一个实例，内部无每连接状态。
type Handler struct {
	d        Deps
	upgrader *gws.Upgrader
	log      *slog.Logger
}

const sessionKey = "orciny.conn"

func NewHandler(d Deps) *Handler {
	if d.Clock == nil {
		d.Clock = clock.System()
	}
	if d.Registry == nil {
		d.Registry = machines.NopRegistry{}
	}
	h := &Handler{d: d}
	h.log = slog.Default()
	if d.App != nil {
		h.log = d.App.Logger()
	}
	h.upgrader = gws.NewUpgrader(h, &gws.ServerOption{
		// 握手报文很小，1MB 上限足够，也挡住了畸形的巨帧。
		ReadMaxPayloadSize: 1 << 20,
	})
	return h
}

// Upgrade 是挂到 PocketBase 路由上的入口。
func (h *Handler) Upgrade(e *core.RequestEvent) error {
	return h.UpgradeHTTP(e.Response, e.Request)
}

// UpgradeHTTP 是不依赖 PocketBase 的升级入口，供包内测试使用。
func (h *Handler) UpgradeHTTP(w http.ResponseWriter, r *http.Request) error {
	socket, err := h.upgrader.Upgrade(w, r)
	if err != nil {
		return err
	}
	go socket.ReadLoop() // 阻塞直到连接结束
	return nil
}

// —— gws.Event 实现 ——

func (h *Handler) OnOpen(socket *gws.Conn) {
	c := &Conn{
		socket:        socket,
		session:       h.d.Handshake.NewSession(),
		stopHeartbeat: make(chan struct{}),
	}
	socket.Session().Store(sessionKey, c)

	// 握手期用短 deadline：升级成功却迟迟不认证的连接必须被清掉。
	_ = socket.SetDeadline(time.Now().Add(h.d.HandshakeTimeout))
}

func (h *Handler) OnClose(socket *gws.Conn, err error) {
	c, ok := h.connOf(socket)
	if !ok {
		return
	}
	c.stopHeartbeatLoop()
	if c.authenticated() {
		h.d.Registry.Unregister(c)
	}
	h.log.Debug("WS 连接关闭", "fingerprint", c.Fingerprint(), "error", err)
}

func (h *Handler) OnPing(socket *gws.Conn, payload []byte) {
	h.touch(socket)
	_ = socket.WritePong(payload)
}

func (h *Handler) OnPong(socket *gws.Conn, _ []byte) {
	// 任何收到的帧都重置 deadline —— 这是「hub 还活着」的唯一证据。
	h.touch(socket)
}

func (h *Handler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()

	c, ok := h.connOf(socket)
	if !ok {
		return
	}

	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		// 解不开的帧说明对面不是我们的 agent，多说无益。
		h.log.Warn("收到无法解码的帧，断开连接", "remote", socket.RemoteAddr().String(), "error", err)
		_ = socket.WriteClose(1002, []byte("bad frame"))
		return
	}

	if !env.Kind.IsKnown() {
		// 新版 hub/agent 之间的向前兼容靠这一条（spec §3.2）。
		h.log.Warn("忽略未知消息类型", "kind", uint8(env.Kind), "fingerprint", c.Fingerprint())
		return
	}

	if !c.authenticated() {
		h.handleHandshake(socket, c, env)
		return
	}

	h.touch(socket)
	h.handleAuthenticated(c, env)
}

func (h *Handler) handleHandshake(socket *gws.Conn, c *Conn, env protocol.Envelope) {
	reply, outcome, err := c.session.Handle(env)
	if err != nil {
		h.log.Warn("握手协议违规，断开连接", "remote", socket.RemoteAddr().String(), "error", err)
		_ = socket.WriteClose(1002, []byte("handshake violation"))
		return
	}
	if len(reply) > 0 {
		if err := socket.WriteMessage(gws.OpcodeBinary, reply); err != nil {
			h.log.Warn("发送握手响应失败", "error", err)
			return
		}
	}
	if outcome == nil {
		return // 握手还在进行
	}

	if !outcome.OK {
		h.log.Warn("拒绝 agent 连接",
			"fingerprint", outcome.Fingerprint, "code", outcome.Code, "reason", outcome.Reason)
		if h.d.Events != nil && outcome.Code == protocol.CodeBadSignature {
			if err := h.d.Events.Write(events.KindAuthFailed, "", map[string]any{
				"fingerprint": outcome.Fingerprint,
				"reason":      outcome.Reason,
				"code":        outcome.Code,
			}); err != nil {
				h.log.Warn("写 auth.failed 事件失败", "error", err)
			}
		}
		_ = socket.WriteClose(1008, []byte(outcome.Reason))
		return
	}

	c.markAuthed(outcome.Fingerprint, outcome.MachineID)
	if err := h.d.Registry.Register(c); err != nil {
		h.log.Error("登记连接失败", "fingerprint", outcome.Fingerprint, "error", err)
		_ = socket.WriteClose(1011, []byte("registry error"))
		return
	}

	// 认证后换成长 deadline，并开始主动 ping。
	h.touch(socket)
	go h.heartbeat(socket, c)
	h.log.Info("agent 已连接", "fingerprint", outcome.Fingerprint, "machine", outcome.MachineID)
}

func (h *Handler) handleAuthenticated(c *Conn, env protocol.Envelope) {
	switch env.Kind {
	case protocol.KindMachineInfo:
		info, err := protocol.DecodePayload[protocol.MachineInfo](env)
		if err != nil {
			h.log.Warn("解析 MachineInfo 失败", "fingerprint", c.Fingerprint(), "error", err)
			return
		}
		if err := h.d.Registry.UpdateInfo(c, info); err != nil {
			h.log.Warn("更新机器信息失败", "fingerprint", c.Fingerprint(), "error", err)
		}
	default:
		// 已认证连接上收到握手类消息属于异常，但不值得断开。
		h.log.Warn("已认证连接收到意外消息", "kind", env.Kind.String(), "fingerprint", c.Fingerprint())
	}
}

// heartbeat 按配置的间隔主动 ping。心跳不走信封——直接用 WebSocket 原生帧，
// 省一次序列化，且 gws 已内置帧级 deadline 管理（spec §6.2）。
func (h *Handler) heartbeat(socket *gws.Conn, c *Conn) {
	ticker := h.d.Clock.NewTicker(h.d.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopHeartbeat:
			return
		case <-ticker.C():
			if err := socket.WritePing(nil); err != nil {
				return
			}
		}
	}
}

func (h *Handler) touch(socket *gws.Conn) {
	_ = socket.SetDeadline(time.Now().Add(h.d.ReadTimeout))
}

func (h *Handler) connOf(socket *gws.Conn) (*Conn, bool) {
	v, ok := socket.Session().Load(sessionKey)
	if !ok {
		return nil, false
	}
	c, ok := v.(*Conn)
	return c, ok
}
```

（import 块里不需要 `errors`——上面没有用到它。）

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./hub/internal/ws/... -race -v`
Expected: PASS（7 个用例）

若 `TestHandshakeTimeoutClosesConnection` 不稳定：确认 `OnOpen` 里设的是 `SetDeadline`（读写都算）而不是只设读 deadline，且 `HandshakeTimeout` 确实被传进来了。

- [ ] **Step 7: 接进路由与 hub**

修改 `hub/internal/routes/routes.go`：

```go
// Deps 增加字段
type Deps struct {
	Enroll   *enroll.Service
	Identity *identity.Store
	WS       *ws.Handler
	Version  string
}

// Register 中追加
	g.GET("/ws", func(e *core.RequestEvent) error { return d.WS.Upgrade(e) })
```

修改 `hub/hub.go`：`Hub` 加 `ws *ws.Handler` 字段；在 `OnServe` 里、`identity.Load()` 之后、`routes.Register` 之前构造：

```go
		h.ws = ws.NewHandler(ws.Deps{
			App:               e.App,
			Handshake:         handshake.NewServer(handshake.NewAppStore(e.App), h.identity, h.cfg.MinAgentVersion, rand.Reader),
			Registry:          machines.NopRegistry{}, // 计划 6 换成真的 Manager
			Events:            h.events,
			Clock:             h.cfg.Clock,
			HandshakeTimeout:  h.cfg.HandshakeTimeout,
			ReadTimeout:       h.cfg.ReadTimeout,
			HeartbeatInterval: h.cfg.HeartbeatInterval,
		})
```

并把 `WS: h.ws` 传进 `routes.Deps`。

- [ ] **Step 8: 运行测试**

Run: `make test`
Expected: 全绿

- [ ] **Step 9: 提交**

```bash
git add hub
git commit -m "feat: WS 端点、握手接线与心跳"
```

---

## Task 4: agent 侧握手与 Compromised 终态

**Files:**
- Create: `agent/internal/conn/dial.go`, `agent/internal/conn/dial_test.go`
- Modify: `internal/testsupport/agent.go`（新增 `Connect`）, `internal/testsupport/agent_test.go`（端到端用例）

**Interfaces:**
- Consumes: `identity.Identity`、`protocol.*`
- Produces:
  ```go
  type Config struct {
      HubURL           string
      Identity         *identity.Identity
      HubPub           ed25519.PublicKey
      AgentVersion     string
      Info             protocol.MachineInfo
      HandshakeTimeout time.Duration
      ReadTimeout      time.Duration
      Rand             io.Reader
      Logger           *slog.Logger
  }
  var ErrHubSignature = errors.New("conn: hub 签名验证失败")
  type RejectedError struct {
      Code   uint8
      Reason string
  }
  func (e *RejectedError) Error() string

  type Session struct{ /* 私有字段 */ }
  func Dial(ctx context.Context, cfg Config) (*Session, error)
  func (s *Session) Done() <-chan struct{}
  func (s *Session) Err() error
  func (s *Session) Close() error
  ```

- [ ] **Step 1: 写失败的测试**

创建 `agent/internal/conn/dial_test.go`（对着计划 5 Task 3 的真 hub 端点测太重，这里用一个可控的假 hub，专门造各种「坏 hub」）：

```go
package conn_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxzan/gws"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

// fakeHub 按脚本回应，用来制造真 hub 不会产生的坏情况。
type fakeHub struct {
	url        string
	priv       ed25519.PrivateKey
	signWith   ed25519.PrivateKey // 用它签名；与 priv 不同即「假 hub」
	rejectCode uint8
	gotInfo    chan protocol.MachineInfo
}

type fakeHandler struct {
	gws.BuiltinEventHandler
	h           *fakeHub
	clientNonce []byte
}

func (f *fakeHandler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		return
	}
	switch env.Kind {
	case protocol.KindHello:
		hello, err := protocol.DecodePayload[protocol.Hello](env)
		if err != nil {
			return
		}
		f.clientNonce = hello.ClientNonce
		serverNonce, _ := protocol.NewNonce(rand.Reader)
		b, _ := protocol.Encode(protocol.KindChallenge, nil, protocol.Challenge{
			ServerNonce: serverNonce,
			HubSig:      ed25519.Sign(f.h.signWith, protocol.HubSigPayload(hello.ClientNonce, serverNonce)),
		})
		_ = socket.WriteMessage(gws.OpcodeBinary, b)
	case protocol.KindAuth:
		res := protocol.AuthResult{OK: true}
		if f.h.rejectCode != 0 {
			res = protocol.AuthResult{OK: false, Code: f.h.rejectCode, Reason: "测试拒绝"}
		}
		b, _ := protocol.Encode(protocol.KindAuthResult, nil, res)
		_ = socket.WriteMessage(gws.OpcodeBinary, b)
	case protocol.KindMachineInfo:
		info, err := protocol.DecodePayload[protocol.MachineInfo](env)
		if err == nil {
			select {
			case f.h.gotInfo <- info:
			default:
			}
		}
	}
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	h := &fakeHub{priv: priv, signWith: priv, gotInfo: make(chan protocol.MachineInfo, 1)}
	up := gws.NewUpgrader(&fakeHandler{h: h}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go socket.ReadLoop()
	}))
	t.Cleanup(srv.Close)

	h.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return h
}

func newIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)
	return id
}

func cfgFor(t *testing.T, h *fakeHub, id *identity.Identity) conn.Config {
	t.Helper()
	return conn.Config{
		HubURL:           h.url,
		Identity:         id,
		HubPub:           h.priv.Public().(ed25519.PublicKey),
		AgentVersion:     "0.1.0",
		Info:             protocol.MachineInfo{Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0"},
		HandshakeTimeout: 2 * time.Second,
		ReadTimeout:      2 * time.Second,
	}
}

func TestDialCompletesHandshakeAndSendsInfo(t *testing.T) {
	h := newFakeHub(t)
	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	select {
	case info := <-h.gotInfo:
		require.Equal(t, "box", info.Hostname)
	case <-time.After(2 * time.Second):
		t.Fatal("握手后未发送 MachineInfo")
	}
}

// hub 签名不匹配意味着中间人，或 hub 换了密钥。两种都需要人来判断，
// agent 必须立刻断开并报出来（spec §7.2）。
func TestDialRejectsWrongHubSignature(t *testing.T) {
	h := newFakeHub(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	h.signWith = other // 冒充者

	_, err = conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.ErrorIs(t, err, conn.ErrHubSignature)
}

func TestDialSurfacesRejectCode(t *testing.T) {
	h := newFakeHub(t)
	h.rejectCode = protocol.CodeUnknownFingerprint

	_, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.Error(t, err)

	var rej *conn.RejectedError
	require.ErrorAs(t, err, &rej)
	require.Equal(t, protocol.CodeUnknownFingerprint, rej.Code)
}

func TestDialTimesOutOnSilentHub(t *testing.T) {
	// 一个升级后什么都不回的 hub
	up := gws.NewUpgrader(&gws.BuiltinEventHandler{}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go socket.ReadLoop()
	}))
	t.Cleanup(srv.Close)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	_, err = conn.Dial(context.Background(), conn.Config{
		HubURL:           "ws" + strings.TrimPrefix(srv.URL, "http"),
		Identity:         newIdentity(t),
		HubPub:           pub,
		AgentVersion:     "0.1.0",
		HandshakeTimeout: 200 * time.Millisecond,
		ReadTimeout:      time.Second,
	})
	require.Error(t, err, "hub 不吭声时必须超时返回，不能挂死")
}

func TestSessionDoneFiresOnHubClose(t *testing.T) {
	h := newFakeHub(t)
	s, err := conn.Dial(context.Background(), cfgFor(t, h, newIdentity(t)))
	require.NoError(t, err)

	require.NoError(t, s.Close())
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done 未在连接结束后关闭")
	}
}

func TestDialFailsOnUnreachableHub(t *testing.T) {
	_, err := conn.Dial(context.Background(), conn.Config{
		HubURL:           "ws://127.0.0.1:1",
		Identity:         newIdentity(t),
		HubPub:           make([]byte, ed25519.PublicKeySize),
		AgentVersion:     "0.1.0",
		HandshakeTimeout: time.Second,
		ReadTimeout:      time.Second,
	})
	require.Error(t, err)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./agent/internal/conn/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写实现**

创建 `agent/internal/conn/dial.go`：

```go
// Package conn 是 agent 侧的 WebSocket 客户端与握手。
// 重连状态机（退避、按原因码分流）在计划 7 加入 reconnect.go。
package conn

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lxzan/gws"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

// ErrHubSignature 表示 hub 的签名验证失败。
//
// 这是 agent 唯一「永不重试」的错误：要么正在遭中间人攻击，要么 hub 换了
// 密钥（恢复备份时丢了 orciny_hub_key.pem）。两种情况都需要人来判断，
// 自作主张重试只会掩盖问题（spec §7.2）。
var ErrHubSignature = errors.New("conn: hub 签名验证失败")

// RejectedError 是 hub 明确拒绝时的错误，携带原因码供重试策略分流。
type RejectedError struct {
	Code   uint8
	Reason string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("conn: hub 拒绝连接（code=%d）: %s", e.Code, e.Reason)
}

type Config struct {
	HubURL       string
	Identity     *identity.Identity
	HubPub       ed25519.PublicKey
	AgentVersion string
	Info         protocol.MachineInfo

	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration

	Rand   io.Reader
	Logger *slog.Logger
}

// Session 是一条已完成握手的连接。
type Session struct {
	socket *gws.Conn
	done   chan struct{}

	mu  sync.Mutex
	err error
}

func (s *Session) Done() <-chan struct{} { return s.done }

// Err 返回连接结束的原因；仍在连接中时返回 nil。
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Session) Close() error { return s.socket.WriteClose(1000, nil) }

// Send 在已建立的会话上发一条信封（计划 7 的心跳外消息用）。
func (s *Session) Send(kind protocol.Kind, payload any) error {
	b, err := protocol.Encode(kind, nil, payload)
	if err != nil {
		return err
	}
	return s.socket.WriteMessage(gws.OpcodeBinary, b)
}

type handler struct {
	gws.BuiltinEventHandler
	inbox       chan protocol.Envelope
	done        chan struct{}
	closeOnce   sync.Once
	readTimeout time.Duration
	log         *slog.Logger
	session     *Session
}

func (h *handler) OnMessage(socket *gws.Conn, msg *gws.Message) {
	defer msg.Close()
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))

	env, err := protocol.Decode(msg.Bytes())
	if err != nil {
		h.log.Warn("收到无法解码的帧，忽略", "error", err)
		return
	}
	if !env.Kind.IsKnown() {
		// 新版 hub 给旧版 agent 发新消息时，忽略而不是崩溃（spec §3.2）。
		h.log.Warn("忽略未知消息类型", "kind", uint8(env.Kind))
		return
	}
	select {
	case h.inbox <- env:
	default:
		h.log.Warn("入站队列已满，丢弃消息", "kind", env.Kind.String())
	}
}

func (h *handler) OnPing(socket *gws.Conn, payload []byte) {
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))
	_ = socket.WritePong(payload)
}

func (h *handler) OnPong(socket *gws.Conn, _ []byte) {
	// agent 侧同样设 deadline，用于识别「hub 已消失但 TCP 未断」
	// （NAT 超时、中间设备静默丢弃）——到期即主动断开重连（spec §6.2）。
	_ = socket.SetDeadline(time.Now().Add(h.readTimeout))
}

func (h *handler) OnClose(_ *gws.Conn, err error) {
	h.closeOnce.Do(func() {
		if h.session != nil {
			h.session.mu.Lock()
			h.session.err = err
			h.session.mu.Unlock()
		}
		close(h.done)
	})
}

// Dial 连接 hub 并完成握手，成功后立即上报 MachineInfo。
func Dial(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	h := &handler{
		inbox:       make(chan protocol.Envelope, 8),
		done:        make(chan struct{}),
		readTimeout: cfg.ReadTimeout,
		log:         cfg.Logger,
	}

	socket, _, err := gws.NewClient(h, &gws.ClientOption{
		Addr:             wsURL(cfg.HubURL),
		HandshakeTimeout: cfg.HandshakeTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("conn: 连接 hub: %w", err)
	}
	go socket.ReadLoop()

	session := &Session{socket: socket, done: h.done}
	h.session = session

	if err := handshakeWith(ctx, cfg, socket, h); err != nil {
		_ = socket.WriteClose(1000, nil)
		return nil, err
	}

	// 握手后的首条业务消息（spec §4.2 第 6 步）
	if err := session.Send(protocol.KindMachineInfo, cfg.Info); err != nil {
		_ = socket.WriteClose(1000, nil)
		return nil, fmt.Errorf("conn: 上报机器信息: %w", err)
	}

	_ = socket.SetDeadline(time.Now().Add(cfg.ReadTimeout))
	return session, nil
}

func handshakeWith(ctx context.Context, cfg Config, socket *gws.Conn, h *handler) error {
	deadline := time.Now().Add(cfg.HandshakeTimeout)
	_ = socket.SetDeadline(deadline)

	clientNonce, err := protocol.NewNonce(cfg.Rand)
	if err != nil {
		return fmt.Errorf("conn: 生成 clientNonce: %w", err)
	}

	hello, err := protocol.Encode(protocol.KindHello, nil, protocol.Hello{
		AgentVersion: cfg.AgentVersion,
		Fingerprint:  cfg.Identity.Fingerprint(),
		ClientNonce:  clientNonce,
	})
	if err != nil {
		return err
	}
	if err := socket.WriteMessage(gws.OpcodeBinary, hello); err != nil {
		return fmt.Errorf("conn: 发送 hello: %w", err)
	}

	env, err := h.await(ctx, deadline)
	if err != nil {
		return err
	}

	// hub 可能直接以 AuthResult 拒绝（版本过旧、指纹未登记）
	if env.Kind == protocol.KindAuthResult {
		return rejectionFrom(env)
	}
	if env.Kind != protocol.KindChallenge {
		return fmt.Errorf("conn: 期望 challenge，收到 %s", env.Kind)
	}
	ch, err := protocol.DecodePayload[protocol.Challenge](env)
	if err != nil {
		return err
	}

	// 验证 hub 身份 —— 这一步失败是终局的。
	if !ed25519.Verify(cfg.HubPub, protocol.HubSigPayload(clientNonce, ch.ServerNonce), ch.HubSig) {
		return ErrHubSignature
	}

	auth, err := protocol.Encode(protocol.KindAuth, nil, protocol.Auth{
		AgentSig: cfg.Identity.Sign(protocol.AgentSigPayload(ch.ServerNonce, clientNonce)),
	})
	if err != nil {
		return err
	}
	if err := socket.WriteMessage(gws.OpcodeBinary, auth); err != nil {
		return fmt.Errorf("conn: 发送 auth: %w", err)
	}

	env, err = h.await(ctx, deadline)
	if err != nil {
		return err
	}
	if env.Kind != protocol.KindAuthResult {
		return fmt.Errorf("conn: 期望 auth_result，收到 %s", env.Kind)
	}
	return rejectionFrom(env)
}

// rejectionFrom 把 AuthResult 转成错误；OK 为真时返回 nil。
func rejectionFrom(env protocol.Envelope) error {
	res, err := protocol.DecodePayload[protocol.AuthResult](env)
	if err != nil {
		return err
	}
	if res.OK {
		return nil
	}
	return &RejectedError{Code: res.Code, Reason: res.Reason}
}

func (h *handler) await(ctx context.Context, deadline time.Time) (protocol.Envelope, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()

	select {
	case env := <-h.inbox:
		return env, nil
	case <-h.done:
		return protocol.Envelope{}, errors.New("conn: 握手期间连接被关闭")
	case <-timer.C:
		return protocol.Envelope{}, errors.New("conn: 等待 hub 响应超时")
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	}
}

// wsURL 把 http(s) 的 hub 地址换成 ws(s) 的端点地址。
func wsURL(hubURL string) string {
	u := strings.TrimRight(hubURL, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	if strings.HasSuffix(u, "/api/orciny/ws") {
		return u
	}
	return u + "/api/orciny/ws"
}
```

> **关于 `handshakeWith` 里的 `time.NewTimer`：** 这是真实网络超时，不是可伪造的逻辑时间，因此用标准库计时器而非 `clock.Clock`——与 gws 的 deadline 保持同一时间基准。测试通过把 `HandshakeTimeout` 设成 200ms 来控制它。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./agent/internal/conn/... -race -v`
Expected: PASS（6 个用例）

- [ ] **Step 5: 打通端到端**

修改 `internal/testsupport/agent.go`，新增：

```go
// Connect 让这台测试机器真的连上 TestHub 并完成握手。
// 返回的 Session 由调用方负责关闭（或用 t.Cleanup）。
func (a *TestAgent) Connect(t *testing.T, th *TestHub) *agent.Session {
	t.Helper()
	s, err := agent.Connect(context.Background(), agent.ConnectOptions{
		Dir:              a.Dir,
		HubURL:           th.HTTPURL,
		HandshakeTimeout: 2 * time.Second,
		ReadTimeout:      2 * time.Second,
	})
	require.NoError(t, err, "agent 连接 hub")
	t.Cleanup(func() { _ = s.Close() })
	return s
}
```

为此在 `agent` 包新增公开入口 `agent/connect.go`：

```go
package agent

import (
	"context"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/probe"
	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/protocol"
)

// ConnectOptions 是 agent.Connect 的入参。
type ConnectOptions struct {
	Dir              string
	HubURL           string
	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration
}

// Session 是一条已建立的连接。这是 conn.Session 的别名，
// 让测试脚手架不必看到 internal 包。
type Session = conn.Session

// Connect 用本机身份连接 hub 并完成握手。
func Connect(ctx context.Context, o ConnectOptions) (*Session, error) {
	dir := filepath.Join(o.Dir, identity.DirName)
	id, err := identity.Load(dir)
	if err != nil {
		return nil, err
	}
	hubPub, err := identity.LoadHubKey(dir)
	if err != nil {
		return nil, err
	}

	hostname, goos, goarch := probe.Host()
	return conn.Dial(ctx, conn.Config{
		HubURL:       o.HubURL,
		Identity:     id,
		HubPub:       hubPub,
		AgentVersion: orciny.Version,
		Info: protocol.MachineInfo{
			Hostname:     hostname,
			OS:           goos,
			Arch:         goarch,
			AgentVersion: orciny.Version,
		},
		HandshakeTimeout: o.HandshakeTimeout,
		ReadTimeout:      o.ReadTimeout,
	})
}
```

- [ ] **Step 6: 写端到端用例**

在 `internal/testsupport/agent_test.go` 追加：

```go
func TestAgentHandshakeAgainstRealHub(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	s := ta.Connect(t, th)
	select {
	case <-s.Done():
		t.Fatalf("连接不该立刻结束: %v", s.Err())
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAgentWithTamperedHubKeyRefusesToConnect(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	// 篡改钉扎的 hub 公钥（对应 DoD #7）
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(ta.IdentityDir, "hub.pub"),
		[]byte(protocol.EncodePublicKey(other)+"\n"), 0o600))

	_, err = agent.Connect(context.Background(), agent.ConnectOptions{
		Dir: ta.Dir, HubURL: th.HTTPURL,
		HandshakeTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
	})
	require.Error(t, err, "hub 签名验证失败必须阻止连接建立")
}

func TestUnenrolledAgentIsRejectedWithCode(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	// 删掉 hub 侧的机器记录，模拟「指纹未登记」
	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.NoError(t, th.App.Delete(m))

	_, err = agent.Connect(context.Background(), agent.ConnectOptions{
		Dir: ta.Dir, HubURL: th.HTTPURL,
		HandshakeTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "code=1", "应带 CodeUnknownFingerprint")
}
```

（补上 `crypto/ed25519`、`crypto/rand`、`os`、`path/filepath`、`time`、`protocol` 的 import。）

- [ ] **Step 7: 运行全部测试**

Run: `make test`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add agent internal/testsupport
git commit -m "feat: agent 侧握手、hub 身份验证与端到端连接"
```

---

## 完成检查

spec §12.2「握手」七条用例逐条对照：

- [ ] 完整正确路径 —— `TestHappyPath` + `TestFullHandshakeRegistersConnection` + `TestAgentHandshakeAgainstRealHub`
- [ ] hub 签名错误 → agent 断开（`Compromised` 的终态判定在计划 7）—— `TestDialRejectsWrongHubSignature` + `TestAgentWithTamperedHubKeyRefusesToConnect`
- [ ] agent 签名错误 → hub 拒绝、记 `auth.failed` 事件 —— `TestBadAgentSignatureIsRejected` + `TestBadSignatureGetsCode`
- [ ] 未知 fingerprint → `CodeUnknownFingerprint` —— `TestUnknownFingerprintIsRejected` + `TestUnenrolledAgentIsRejectedWithCode`
- [ ] agent 版本低于门槛 → `CodeVersionTooOld` —— `TestVersionTooOldIsRejectedBeforeLookup` + `TestVersionTooOldGetsCode`
- [ ] 重放截获的 `Auth` → 必须失败 —— `TestReplayedAuthFails`
- [ ] 握手超时 → 连接被关闭 —— `TestHandshakeTimeoutClosesConnection`

另外：

- [ ] 未知 Kind 被忽略且不断开 —— `TestUnknownKindIsIgnoredNotFatal`
- [ ] `hub/internal/ws` 只依赖 `machines` 的接口，`machines` 不依赖 `ws`
- [ ] `go list -deps ./agent/... | grep orciny/hub` 无输出
