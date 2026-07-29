# M0 计划 2 · 协议层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 `protocol/` —— hub 与 agent 唯一的公共依赖：CBOR 信封、Kind 枚举、M0 五种消息、拒绝原因码、签名域分隔与指纹派生。

**Architecture:** 单一 `Envelope{Kind, ID, Data}` + `Kind` 分派，payload 用 `cbor.RawMessage` 延迟解码；字段一律 `keyasint` 整数标签，重命名不破坏兼容；枚举值只增不改不复用，分段预留给 M1/M2。

**Tech Stack:** Go 1.26 · fxamacker/cbor v2.9.2 · 标准库 `crypto/ed25519` `crypto/sha256` `crypto/rand`

**上位文档：** [统括计划](00-overview.md) · [spec §3 §4.3](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- **`protocol` 必须保持极瘦**：只有数据结构、编解码、签名负载拼装与指纹派生。不含业务逻辑、不 import PocketBase、不 import hub 或 agent 的任何包。任何「查库」「判定」「重试」都不属于这里。
- `agent/**` 与 `hub/**` 之间零直接依赖。
- 测试禁用 `time.Sleep`；断言用 `testify/require`。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `protocol/envelope.go` | `Kind` 枚举与 `Envelope` 结构，Kind 的编号纪律写在注释里 |
| `protocol/messages.go` | M0 五种 payload 结构体 + 拒绝原因码 |
| `protocol/codec.go` | `Encode` / `Decode` / `DecodePayload`，CBOR 模式在此集中配置 |
| `protocol/crypto.go` | nonce 生成、两条域分隔签名负载、指纹派生 |
| `protocol/*_test.go` | 包外测试（`package protocol_test`），只碰公开 API |

---

## Task 1: 信封与编解码

**Files:**
- Create: `protocol/envelope.go`, `protocol/codec.go`, `protocol/codec_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  ```go
  type Kind uint8
  const (
      KindHello       Kind = 1
      KindChallenge   Kind = 2
      KindAuth        Kind = 3
      KindAuthResult  Kind = 4
      KindMachineInfo Kind = 5
  )
  func (k Kind) IsKnown() bool
  func (k Kind) String() string

  type Envelope struct {
      Kind Kind            `cbor:"0,keyasint"`
      ID   *uint32         `cbor:"1,keyasint,omitempty"`
      Data cbor.RawMessage `cbor:"2,keyasint,omitempty,omitzero"`
  }

  func Encode(kind Kind, id *uint32, payload any) ([]byte, error)
  func Decode(b []byte) (Envelope, error)
  func DecodePayload[T any](env Envelope) (T, error)
  var ErrNoPayload = errors.New("protocol: 信封不含 payload")
  ```

- [ ] **Step 1: 拉依赖**

```bash
go get github.com/fxamacker/cbor/v2@v2.9.2
```

- [ ] **Step 2: 写失败的测试**

创建 `protocol/codec_test.go`：

```go
package protocol_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := protocol.Hello{AgentVersion: "0.1.0", Fingerprint: "fp", ClientNonce: []byte{1, 2}}

	b, err := protocol.Encode(protocol.KindHello, nil, in)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.KindHello, env.Kind)
	require.Nil(t, env.ID)

	out, err := protocol.DecodePayload[protocol.Hello](env)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

// 这两条 golden 断言锁死 wire 格式：keyasint 编号变了、omitempty 掉了，
// 都会在这里炸掉，而不是等到一个旧 agent 连不上才发现。
func TestWireFormatIsStable(t *testing.T) {
	b, err := protocol.Encode(protocol.KindHello, nil,
		protocol.Hello{AgentVersion: "0.1.0", Fingerprint: "fp", ClientNonce: []byte{1, 2}})
	require.NoError(t, err)
	require.Equal(t, "a2000102a30065302e312e300162667002420102", hex.EncodeToString(b))
}

func TestEnvelopeWithoutPayloadOmitsDataField(t *testing.T) {
	b, err := protocol.Encode(protocol.KindAuthResult, nil, nil)
	require.NoError(t, err)
	// a1 = 1 对键值；00 04 = Kind:4。ID 与 Data 都不出现。
	require.Equal(t, "a10004", hex.EncodeToString(b))
	require.Len(t, b, 3)
}

func TestEncodeWithRequestID(t *testing.T) {
	id := uint32(7)
	b, err := protocol.Encode(protocol.KindMachineInfo, &id, protocol.MachineInfo{Hostname: "h"})
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.NotNil(t, env.ID)
	require.Equal(t, uint32(7), *env.ID)
}

func TestDecodePayloadOnEmptyDataFails(t *testing.T) {
	b, err := protocol.Encode(protocol.KindAuthResult, nil, nil)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	_, err = protocol.DecodePayload[protocol.AuthResult](env)
	require.ErrorIs(t, err, protocol.ErrNoPayload)
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, err := protocol.Decode([]byte{0xff, 0xff, 0xff})
	require.Error(t, err)
}

// 未知 Kind 必须能被正常解出来——上层据此「记 warn 后丢弃」，
// 而不是解码失败导致断开（spec §3.2）。
func TestDecodePreservesUnknownKind(t *testing.T) {
	b, err := protocol.Encode(protocol.Kind(99), nil, nil)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.Kind(99), env.Kind)
	require.False(t, env.Kind.IsKnown())
}

func TestKnownKinds(t *testing.T) {
	for _, k := range []protocol.Kind{
		protocol.KindHello, protocol.KindChallenge, protocol.KindAuth,
		protocol.KindAuthResult, protocol.KindMachineInfo,
	} {
		require.True(t, k.IsKnown(), "%d 应为已知 Kind", uint8(k))
		require.NotContains(t, k.String(), "unknown")
	}
	require.Equal(t, "unknown(99)", protocol.Kind(99).String())
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./protocol/... -v`
Expected: FAIL —— `protocol` 包不存在

- [ ] **Step 4: 写信封**

创建 `protocol/envelope.go`：

```go
// Package protocol 是 hub 与 agent 唯一的公共依赖（spec §2.2 第 2 条）。
//
// 纪律：本包只放数据结构、编解码、以及两侧必须逐位一致的密码学格式
// （nonce 长度、签名负载拼装、指纹派生）。任何业务逻辑、任何对
// PocketBase 的引用、任何对 hub/agent 包的引用都不允许出现在这里。
package protocol

import "fmt"

// Kind 标识信封里装的是哪种消息。
//
// 纪律（spec §3.2）：
//   - 编号只增不改不复用。删除某种消息时，注释掉并保留编号。
//   - 10-19 预留给 M1 配置下发（ConfigNotify / ConfigPull / ApplyAck /
//     DriftReport / DriftRestore）
//   - 20-29 预留给 M2 数据面（UsageBatch / CollectorReport）
//   - 30-39 预留给 agent 生命周期（AgentUpdate）
//   - 收到未知 Kind：记 warn 日志后丢弃，**不断开连接**。这样新版 hub 给
//     旧版 agent 发新消息时，旧 agent 只是忽略而不是重连风暴。
type Kind uint8

const (
	KindHello       Kind = 1 // agent → hub
	KindChallenge   Kind = 2 // hub   → agent
	KindAuth        Kind = 3 // agent → hub
	KindAuthResult  Kind = 4 // hub   → agent（握手结果，亦用作事后的授权撤销通知）
	KindMachineInfo Kind = 5 // agent → hub
)

// IsKnown 报告本版本是否认识这个 Kind。分派前先问它。
func (k Kind) IsKnown() bool {
	switch k {
	case KindHello, KindChallenge, KindAuth, KindAuthResult, KindMachineInfo:
		return true
	default:
		return false
	}
}

func (k Kind) String() string {
	switch k {
	case KindHello:
		return "hello"
	case KindChallenge:
		return "challenge"
	case KindAuth:
		return "auth"
	case KindAuthResult:
		return "auth_result"
	case KindMachineInfo:
		return "machine_info"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}
```

- [ ] **Step 5: 写编解码**

创建 `protocol/codec.go`：

```go
package protocol

import (
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Envelope 是 wire 上唯一的顶层结构（spec §3.1）。
//
// 与 beszel 的两结构体方案不同：这里加一种消息只需要加一个 Kind 和一个
// payload 类型，信封本身永远不动。
type Envelope struct {
	Kind Kind `cbor:"0,keyasint"`
	// ID 用于请求-响应配对；通知类消息为 nil。M0 全部消息都是单向通知，
	// 字段先在 wire 上占位，M1 的 ConfigPull 会用到。
	ID *uint32 `cbor:"1,keyasint,omitempty"`
	// Data 延迟解码：先按 Kind 分派，再解成对应类型，不必先解成 map。
	Data cbor.RawMessage `cbor:"2,keyasint,omitempty,omitzero"`
}

// ErrNoPayload 表示信封没带 payload，但调用方想解一个出来。
var ErrNoPayload = errors.New("protocol: 信封不含 payload")

var (
	encMode cbor.EncMode
	decMode cbor.DecMode
)

func init() {
	var err error
	encMode, err = cbor.EncOptions{}.EncMode()
	if err != nil {
		panic("protocol: 初始化 CBOR 编码模式失败: " + err.Error())
	}
	// DupMapKeyEnforcedAPF：重复键直接报错，堵掉「同一字段出现两次、
	// 两端各取一个」这类解析歧义。
	// MaxArrayElements / MaxMapPairs 收紧，避免畸形输入撑爆内存。
	decMode, err = cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		MaxArrayElements: 1024,
		MaxMapPairs:      1024,
	}.DecMode()
	if err != nil {
		panic("protocol: 初始化 CBOR 解码模式失败: " + err.Error())
	}
}

// Encode 把一条消息打成 wire 字节。payload 为 nil 时不产生 Data 字段。
func Encode(kind Kind, id *uint32, payload any) ([]byte, error) {
	env := Envelope{Kind: kind, ID: id}
	if payload != nil {
		raw, err := encMode.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("protocol: 编码 %s 的 payload: %w", kind, err)
		}
		env.Data = raw
	}
	b, err := encMode.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("protocol: 编码信封: %w", err)
	}
	return b, nil
}

// Decode 解出信封。未知 Kind 不算错误——调用方用 Kind.IsKnown() 决定怎么办。
func Decode(b []byte) (Envelope, error) {
	var env Envelope
	if err := decMode.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("protocol: 解码信封: %w", err)
	}
	return env, nil
}

// DecodePayload 把信封里的 Data 解成 T。
func DecodePayload[T any](env Envelope) (T, error) {
	var out T
	if len(env.Data) == 0 {
		return out, ErrNoPayload
	}
	if err := decMode.Unmarshal(env.Data, &out); err != nil {
		return out, fmt.Errorf("protocol: 解码 %s 的 payload: %w", env.Kind, err)
	}
	return out, nil
}
```

- [ ] **Step 6: 写消息结构体（本任务测试需要它们编译通过）**

创建 `protocol/messages.go`：

```go
package protocol

// M0 的全部消息（spec §3.3）。
//
// 字段编号同样只增不改不复用。加字段用新编号并带 omitempty，
// 老版本解码时会忽略它——这是选 CBOR + keyasint 的主要收益。

// Hello 是 agent 连上来的第一条消息。
type Hello struct {
	AgentVersion string `cbor:"0,keyasint"`
	Fingerprint  string `cbor:"1,keyasint"`
	ClientNonce  []byte `cbor:"2,keyasint"` // NonceSize 字节
}

// Challenge 是 hub 的应答：自己的 nonce + 对两个 nonce 的签名。
type Challenge struct {
	ServerNonce []byte `cbor:"0,keyasint"` // NonceSize 字节
	HubSig      []byte `cbor:"1,keyasint"` // Ed25519 签名，见 HubSigPayload
}

// Auth 是 agent 对 hub 挑战的应答。
type Auth struct {
	AgentSig []byte `cbor:"0,keyasint"` // Ed25519 签名，见 AgentSigPayload
}

// AuthResult 是 hub 的裁决。
//
// 它有一个额外用途（spec §3.3）：握手完成后 hub 仍可发 OK:false 作为
// 「授权已撤销」通知，随后关闭连接。agent 在任何时候收到 OK:false 都按
// Code 走同一条处理路径，不区分握手期还是连接期。
type AuthResult struct {
	OK     bool   `cbor:"0,keyasint"`
	Reason string `cbor:"1,keyasint,omitempty"` // 人类可读，供日志
	Code   uint8  `cbor:"2,keyasint,omitempty"` // 机器可读，驱动重试策略
}

// MachineInfo 是握手后的首条业务消息，hub 据此更新机器记录。
type MachineInfo struct {
	Hostname     string            `cbor:"0,keyasint"`
	OS           string            `cbor:"1,keyasint"` // linux / darwin
	Arch         string            `cbor:"2,keyasint"` // amd64 / arm64
	AgentVersion string            `cbor:"3,keyasint"`
	ToolVersions map[string]string `cbor:"4,keyasint,omitempty"` // {"claude-code": "2.1.3"}
}

// 拒绝原因码（spec §4.6）。
//
// 给出明确原因而不是笼统的 401，是因为这几种情况的正确处置完全不同，
// 而排查者手上往往只有 agent 的日志。
const (
	CodeUnknownFingerprint uint8 = 1 // 提示重新 enroll，5 分钟间隔重试
	CodeBadSignature       uint8 = 2 // 视为异常，5 分钟间隔重试
	CodeVersionTooOld      uint8 = 3 // 5 分钟间隔重试（等 hub 升级或 agent 更新）
	CodeMachineRemoved     uint8 = 4 // 停止重试，需人工重新 enroll
)
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./protocol/... -v`
Expected: PASS（8 个用例）

- [ ] **Step 8: 提交**

```bash
git add protocol
git commit -m "feat: CBOR 信封、Kind 枚举与编解码"
```

---

## Task 2: 消息编解码用例

Task 1 已经把结构体写完了（否则测试编译不过）。本任务补齐每种消息的往返与「加字段不破坏旧解码」的证明。

**Files:**
- Create: `protocol/messages_test.go`

**Interfaces:**
- Consumes: Task 1 的全部导出符号
- Produces: 无新符号

- [ ] **Step 1: 写失败的测试**

创建 `protocol/messages_test.go`：

```go
package protocol_test

import (
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestAllM0MessagesRoundTrip(t *testing.T) {
	t.Run("hello", func(t *testing.T) {
		in := protocol.Hello{AgentVersion: "1.2.3", Fingerprint: "abc", ClientNonce: []byte("nonce")}
		env := mustDecode(t, mustEncode(t, protocol.KindHello, in))
		out, err := protocol.DecodePayload[protocol.Hello](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("challenge", func(t *testing.T) {
		in := protocol.Challenge{ServerNonce: []byte("sn"), HubSig: []byte("sig")}
		env := mustDecode(t, mustEncode(t, protocol.KindChallenge, in))
		out, err := protocol.DecodePayload[protocol.Challenge](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth", func(t *testing.T) {
		in := protocol.Auth{AgentSig: []byte("sig")}
		env := mustDecode(t, mustEncode(t, protocol.KindAuth, in))
		out, err := protocol.DecodePayload[protocol.Auth](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth_result_ok", func(t *testing.T) {
		in := protocol.AuthResult{OK: true}
		env := mustDecode(t, mustEncode(t, protocol.KindAuthResult, in))
		out, err := protocol.DecodePayload[protocol.AuthResult](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("auth_result_rejected", func(t *testing.T) {
		in := protocol.AuthResult{OK: false, Reason: "指纹未登记", Code: protocol.CodeUnknownFingerprint}
		env := mustDecode(t, mustEncode(t, protocol.KindAuthResult, in))
		out, err := protocol.DecodePayload[protocol.AuthResult](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("machine_info", func(t *testing.T) {
		in := protocol.MachineInfo{
			Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
			ToolVersions: map[string]string{"claude-code": "2.1.3"},
		}
		env := mustDecode(t, mustEncode(t, protocol.KindMachineInfo, in))
		out, err := protocol.DecodePayload[protocol.MachineInfo](env)
		require.NoError(t, err)
		require.Equal(t, in, out)
	})

	t.Run("machine_info_without_tools", func(t *testing.T) {
		// 机器上没装 Claude Code 是合法状态（spec §7.4）
		in := protocol.MachineInfo{Hostname: "box", OS: "darwin", Arch: "arm64", AgentVersion: "0.1.0"}
		env := mustDecode(t, mustEncode(t, protocol.KindMachineInfo, in))
		out, err := protocol.DecodePayload[protocol.MachineInfo](env)
		require.NoError(t, err)
		require.Nil(t, out.ToolVersions)
	})
}

// 未来版本给 MachineInfo 加字段时，旧版本必须能照常解出自己认识的字段。
// 这是 keyasint 的核心承诺，值得有一条测试盯着。
func TestUnknownFieldsAreIgnored(t *testing.T) {
	type futureMachineInfo struct {
		Hostname string `cbor:"0,keyasint"`
		OS       string `cbor:"1,keyasint"`
		Arch     string `cbor:"2,keyasint"`
		AgentVer string `cbor:"3,keyasint"`
		Tools    map[string]string `cbor:"4,keyasint,omitempty"`
		Kernel   string `cbor:"9,keyasint"` // M9 才有的字段
	}
	raw, err := cbor.Marshal(futureMachineInfo{
		Hostname: "box", OS: "linux", Arch: "amd64", AgentVer: "9.9.9", Kernel: "6.1",
	})
	require.NoError(t, err)

	b, err := protocol.Encode(protocol.KindMachineInfo, nil, cbor.RawMessage(raw))
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)

	out, err := protocol.DecodePayload[protocol.MachineInfo](env)
	require.NoError(t, err)
	require.Equal(t, "box", out.Hostname)
	require.Equal(t, "9.9.9", out.AgentVersion)
}

func TestRejectCodesAreStable(t *testing.T) {
	// 原因码写进了 agent 的重试策略，改动等于改协议。
	require.Equal(t, uint8(1), protocol.CodeUnknownFingerprint)
	require.Equal(t, uint8(2), protocol.CodeBadSignature)
	require.Equal(t, uint8(3), protocol.CodeVersionTooOld)
	require.Equal(t, uint8(4), protocol.CodeMachineRemoved)
}

func mustEncode(t *testing.T, k protocol.Kind, payload any) []byte {
	t.Helper()
	b, err := protocol.Encode(k, nil, payload)
	require.NoError(t, err)
	return b
}

func mustDecode(t *testing.T, b []byte) protocol.Envelope {
	t.Helper()
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	return env
}
```

- [ ] **Step 2: 运行测试**

Run: `go test ./protocol/... -run 'TestAllM0Messages|TestUnknownFields|TestRejectCodes' -v`
Expected: PASS。若 `TestUnknownFieldsAreIgnored` 失败，说明 `Encode` 对 `cbor.RawMessage` 做了二次封装——检查 `Encode` 是否直接 `Marshal(payload)`（RawMessage 的 Marshal 是恒等的，应当通过）。

- [ ] **Step 3: 提交**

```bash
git add protocol/messages_test.go
git commit -m "test: M0 全部消息的编解码往返与前向兼容"
```

---

## Task 3: nonce、签名域分隔与指纹派生

这三样必须放在 protocol：两侧对它们的实现哪怕差一个字节，握手就永远不通，而错误现场只有一句「签名验证失败」。

**Files:**
- Create: `protocol/crypto.go`, `protocol/crypto_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  ```go
  const NonceSize = 32
  func NewNonce(r io.Reader) ([]byte, error)
  func HubSigPayload(clientNonce, serverNonce []byte) []byte
  func AgentSigPayload(serverNonce, clientNonce []byte) []byte
  func Fingerprint(pub ed25519.PublicKey) string
  func EncodePublicKey(pub ed25519.PublicKey) string
  func DecodePublicKey(s string) (ed25519.PublicKey, error)
  ```

- [ ] **Step 1: 写失败的测试**

创建 `protocol/crypto_test.go`：

```go
package protocol_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestNewNonceLengthAndUniqueness(t *testing.T) {
	a, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)
	require.Len(t, a, protocol.NonceSize)
	require.Equal(t, 32, protocol.NonceSize)

	b, err := protocol.NewNonce(rand.Reader)
	require.NoError(t, err)
	require.NotEqual(t, a, b, "nonce 必须每次都不同")
}

func TestNewNonceFailsOnShortReader(t *testing.T) {
	_, err := protocol.NewNonce(bytes.NewReader([]byte{1, 2, 3}))
	require.Error(t, err, "熵不足时必须报错，绝不能返回半截 nonce")
}

func TestSigPayloadsCarryDomainPrefix(t *testing.T) {
	cn := bytes.Repeat([]byte{1}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{2}, protocol.NonceSize)

	hub := protocol.HubSigPayload(cn, sn)
	agent := protocol.AgentSigPayload(sn, cn)

	require.True(t, bytes.HasPrefix(hub, []byte("orciny-hub-v1")))
	require.True(t, bytes.HasPrefix(agent, []byte("orciny-agent-v1")))
	require.Len(t, hub, len("orciny-hub-v1")+2*protocol.NonceSize)
	require.Len(t, agent, len("orciny-agent-v1")+2*protocol.NonceSize)
}

// 域分隔的意义：一方的签名不能被拿去冒充另一方（spec §4.2）。
func TestSigPayloadsAreNeverEqual(t *testing.T) {
	cn := bytes.Repeat([]byte{7}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{7}, protocol.NonceSize) // 故意让两个 nonce 相同
	require.NotEqual(t, protocol.HubSigPayload(cn, sn), protocol.AgentSigPayload(sn, cn))
}

// 两个 nonce 都必须进签名：只用一个的话，另一方的挑战就是可预测的。
func TestSigPayloadChangesWithEitherNonce(t *testing.T) {
	cn := bytes.Repeat([]byte{1}, protocol.NonceSize)
	sn := bytes.Repeat([]byte{2}, protocol.NonceSize)
	other := bytes.Repeat([]byte{3}, protocol.NonceSize)

	base := protocol.HubSigPayload(cn, sn)
	require.NotEqual(t, base, protocol.HubSigPayload(other, sn))
	require.NotEqual(t, base, protocol.HubSigPayload(cn, other))
}

func TestSigPayloadIsVerifiableEd25519(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	cn, _ := protocol.NewNonce(rand.Reader)
	sn, _ := protocol.NewNonce(rand.Reader)

	sig := ed25519.Sign(priv, protocol.HubSigPayload(cn, sn))
	require.True(t, ed25519.Verify(pub, protocol.HubSigPayload(cn, sn), sig))
	require.False(t, ed25519.Verify(pub, protocol.AgentSigPayload(sn, cn), sig),
		"hub 的签名不能通过 agent 域的验证")
}

func TestFingerprintShapeAndDeterminism(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	fp := protocol.Fingerprint(pub)
	require.Len(t, fp, 32)
	require.Equal(t, fp, protocol.Fingerprint(pub), "同一公钥必须得到同一指纹")
	require.NotContains(t, fp, "+", "必须是 base64url 字母表")
	require.NotContains(t, fp, "/")
	require.NotContains(t, fp, "=")
}

func TestFingerprintDiffersPerKey(t *testing.T) {
	a, _, _ := ed25519.GenerateKey(rand.Reader)
	b, _, _ := ed25519.GenerateKey(rand.Reader)
	require.NotEqual(t, protocol.Fingerprint(a), protocol.Fingerprint(b))
}

func TestPublicKeyEncodingRoundTrip(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	s := protocol.EncodePublicKey(pub)
	back, err := protocol.DecodePublicKey(s)
	require.NoError(t, err)
	require.Equal(t, pub, back)
}

func TestDecodePublicKeyRejectsWrongLength(t *testing.T) {
	_, err := protocol.DecodePublicKey("AAAA")
	require.Error(t, err, "长度不对的公钥必须拒绝，不能凑合")
}

func TestDecodePublicKeyRejectsNonBase64(t *testing.T) {
	_, err := protocol.DecodePublicKey("这不是 base64")
	require.Error(t, err)
}

func TestFingerprintGolden(t *testing.T) {
	// 全零公钥的指纹。这条锁死派生算法：换哈希、换编码、换截断长度都会炸。
	pub := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	require.Equal(t, "Zmh6rfhivXdsj8GLjp-OIAiXFIVu4jOz", protocol.Fingerprint(pub))
}
```

> **给执行者的提示：** 这个期望值已用 `base64.RawURLEncoding.EncodeToString(sha256.Sum256(32 个零字节))[:32]` 实算过，是对的。若测试红了，是实现错了（哈希、编码字母表或截断长度），不要改断言。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./protocol/... -run TestFingerprint -v`
Expected: FAIL —— `undefined: protocol.Fingerprint`

- [ ] **Step 3: 写实现**

创建 `protocol/crypto.go`：

```go
package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// NonceSize 是挑战-应答里两个 nonce 的字节数。
const NonceSize = 32

// 域分隔前缀。一方的签名不能被拿去冒充另一方（spec §4.2）。
// 版本后缀（-v1）为将来更换签名格式留了余地：换格式就换前缀，
// 老 agent 的签名自动失效，不会被误认。
const (
	hubSigPrefix   = "orciny-hub-v1"
	agentSigPrefix = "orciny-agent-v1"
)

// NewNonce 生成一次性随机 nonce。生产传 crypto/rand.Reader。
// nonce 不入库、不复用。
func NewNonce(r io.Reader) ([]byte, error) {
	b := make([]byte, NonceSize)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, fmt.Errorf("protocol: 生成 nonce: %w", err)
	}
	return b, nil
}

// HubSigPayload 拼出 hub 要签名的字节串："orciny-hub-v1" ‖ clientNonce ‖ serverNonce。
func HubSigPayload(clientNonce, serverNonce []byte) []byte {
	return sigPayload(hubSigPrefix, clientNonce, serverNonce)
}

// AgentSigPayload 拼出 agent 要签名的字节串："orciny-agent-v1" ‖ serverNonce ‖ clientNonce。
//
// 注意两个 nonce 的顺序与 HubSigPayload 相反——即便前缀被抹掉，
// 两条负载也不会撞上。
func AgentSigPayload(serverNonce, clientNonce []byte) []byte {
	return sigPayload(agentSigPrefix, serverNonce, clientNonce)
}

func sigPayload(prefix string, first, second []byte) []byte {
	out := make([]byte, 0, len(prefix)+len(first)+len(second))
	out = append(out, prefix...)
	out = append(out, first...)
	out = append(out, second...)
	return out
}

// Fingerprint 由公钥派生机器指纹：base64url(SHA256(pubKey)) 取前 32 字符。
//
// 指纹是公钥的函数，不是硬件的函数（spec §4.3）：
//   - 消除「指纹对但公钥错」这种需要额外处理的中间状态；
//   - 规避硬件 ID 在 VPS 和容器里的重复（beszel 为此硬编码过 knownBadUUID）。
//
// 代价是重装系统或删掉 ~/.orciny/identity/ 会变成一台新机器——密钥丢了
// 本来就该重新建立信任。
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// EncodePublicKey 是公钥在 wire 与数据库里的统一表示：标准 base64 的裸公钥。
// enroll 请求体、machines.pub_key、agent 的 hub.pub 三处必须用同一种编码，
// 否则会出现「同一把钥匙、三种写法」的比对错误。
func EncodePublicKey(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePublicKey 是 EncodePublicKey 的逆操作，长度不符即拒绝。
func DecodePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("protocol: 解码公钥: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("protocol: 公钥长度应为 %d 字节，实际 %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./protocol/... -v`
Expected: PASS（全部用例）。若 golden 指纹不符，按 Step 1 的提示核对后更新断言。

- [ ] **Step 5: 确认 protocol 的依赖足够瘦**

Run: `go list -deps ./protocol | grep -v '^\(internal/\|vendor/\)' | grep -v '^[a-z]*$' | grep -v '^\(crypto\|encoding\|math\|runtime\|sync\|unicode\|internal\)'`
Expected: 只应看到标准库与 `github.com/fxamacker/cbor/v2`（及其内部包）。**出现 pocketbase、hub、agent 任意一项即为违规**，必须回退。

- [ ] **Step 6: 全量测试**

Run: `make test && make lint`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add protocol/crypto.go protocol/crypto_test.go
git commit -m "feat: nonce 生成、签名域分隔与公钥指纹派生"
```

---

## 完成检查

- [ ] `go list -deps ./protocol` 里没有 pocketbase / hub / agent
- [ ] wire golden 测试存在且通过（`a2000102...` 与 `a10004`）
- [ ] 五种消息、四个原因码、两条签名负载、指纹派生全部有测试
- [ ] 交付给计划 3–7 的接口与统括计划「全局接口契约」一致
