# M0 计划 3 · 身份与密钥 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 两侧的 Ed25519 身份就位——hub 的长期密钥（缺则生成，PEM 0600，落在 PocketBase 数据目录里以便被备份覆盖）、agent 的机器密钥与钉扎的 hub 公钥。

**Architecture:** 私钥永不上网。hub 密钥是单例，`OnServe` 阶段加载/生成；agent 密钥在 enroll 时生成，指纹由公钥派生（`protocol.Fingerprint`）。hub 公钥以 TOFU 方式钉扎在 agent 本地，是 M1 起「谁能往我的 `~/.claude` 写文件」的唯一判据。

**Tech Stack:** Go 1.26 · 标准库 `crypto/ed25519` `crypto/x509` `encoding/pem` · `internal/atomicfile`

**上位文档：** [统括计划](00-overview.md) · [spec §4.1 §4.3 §4.5 §7.1](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；共享只走 `protocol/`；`hub/internal/*` 的单测写在包内。
- **私钥内容、hub 公钥内容一律不入日志**（spec §7.5）。错误信息里只能出现路径和指纹，不能出现密钥字节。
- 所有落盘走 `internal/atomicfile.Write`（临时文件 + rename），私钥权限 `0600`。
- 测试禁用 `time.Sleep`；断言用 `testify/require`。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `hub/internal/identity/store.go` | hub 长期密钥：生成、加载、签名、指纹 |
| `hub/internal/identity/store_test.go` | 包内单测（权限位、幂等、持久性） |
| `hub/hub.go` | 修改：`OnServe` 里加载 hub 密钥；新增 `(*Hub).PublicKey()` |
| `agent/internal/identity/identity.go` | agent 密钥对与 hub 公钥钉扎 |
| `agent/internal/identity/identity_test.go` | 包内单测 |

---

## Task 1: hub 长期密钥

**Files:**
- Create: `hub/internal/identity/store.go`, `hub/internal/identity/store_test.go`
- Modify: `hub/hub.go`（`Attach` 里注册加载、新增 `PublicKey()`）, `hub/hub_test.go`（补一条断言）

**Interfaces:**
- Consumes: `protocol.Fingerprint`、`protocol.EncodePublicKey`、`internal/atomicfile.Write`
- Produces:
  ```go
  // hub/internal/identity
  const KeyFileName = "orciny_hub_key.pem"
  type Store struct{ /* 私有字段 */ }
  func NewStore(path string) *Store
  func (s *Store) Load() error                  // 幂等；文件不存在则生成
  func (s *Store) PublicKey() ed25519.PublicKey
  func (s *Store) PublicKeyBase64() string
  func (s *Store) Fingerprint() string
  func (s *Store) Sign(msg []byte) []byte

  // hub
  func (h *Hub) PublicKey() ed25519.PublicKey
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/identity/store_test.go`：

```go
package identity_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestLoadGeneratesKeyWhenMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	s := identity.NewStore(p)

	require.NoError(t, s.Load())
	require.FileExists(t, p)
	require.Len(t, s.PublicKey(), ed25519.PublicKeySize)
}

func TestGeneratedKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	require.NoError(t, identity.NewStore(p).Load())

	st, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "hub 私钥必须是 0600")
}

func TestLoadIsStableAcrossRestarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)

	first := identity.NewStore(p)
	require.NoError(t, first.Load())

	second := identity.NewStore(p)
	require.NoError(t, second.Load())

	require.Equal(t, first.PublicKey(), second.PublicKey(),
		"重启后必须是同一把钥匙，否则全机队要重新 enroll")
	require.Equal(t, first.Fingerprint(), second.Fingerprint())
}

func TestLoadIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	s := identity.NewStore(p)
	require.NoError(t, s.Load())
	pub := s.PublicKey()
	require.NoError(t, s.Load())
	require.Equal(t, pub, s.PublicKey())
}

func TestSignIsVerifiableWithPublicKey(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())

	msg := protocol.HubSigPayload(make([]byte, protocol.NonceSize), make([]byte, protocol.NonceSize))
	require.True(t, ed25519.Verify(s.PublicKey(), msg, s.Sign(msg)))
}

func TestFingerprintMatchesProtocolDerivation(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())
	require.Equal(t, protocol.Fingerprint(s.PublicKey()), s.Fingerprint())
}

func TestPublicKeyBase64RoundTrips(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())

	back, err := protocol.DecodePublicKey(s.PublicKeyBase64())
	require.NoError(t, err)
	require.Equal(t, s.PublicKey(), back)
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	require.NoError(t, os.WriteFile(p, []byte("这不是 PEM"), 0o600))

	err := identity.NewStore(p).Load()
	require.Error(t, err, "损坏的密钥文件必须报错而不是静默重新生成——"+
		"静默生成会让全机队突然连不上，且没人知道为什么")
	require.NotContains(t, err.Error(), "这不是 PEM", "错误信息不得回显文件内容")
}

func TestAccessorsBeforeLoadDoNotPanic(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.Nil(t, s.PublicKey())
	require.Empty(t, s.Fingerprint())
	require.Nil(t, s.Sign([]byte("x")))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/identity/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写实现**

创建 `hub/internal/identity/store.go`：

```go
// Package identity 管理 hub 的长期 Ed25519 密钥。
//
// 这把钥匙是整个机队信任根的一半：agent 在 enroll 时钉扎它的公钥，
// 此后每次握手都用它验证「对面确实是我认识的那个 hub」（spec §4.1）。
//
// 它放在 PocketBase 的数据目录里，因此自动被 PocketBase 的备份机制覆盖。
// 反过来说：恢复备份时若丢了这个文件，所有 agent 都会因签名验证失败而
// 拒绝连接，需要全部重新 enroll——这一条必须写进运维文档（spec §4.5）。
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

// KeyFileName 是密钥在数据目录下的文件名。
const KeyFileName = "orciny_hub_key.pem"

const pemBlockType = "PRIVATE KEY"

// Store 持有 hub 的密钥。零值不可用，用 NewStore 构造，用前先 Load。
type Store struct {
	path string

	mu   sync.RWMutex
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func NewStore(path string) *Store { return &Store{path: path} }

// Load 加载密钥；文件不存在则生成并落盘。可重复调用。
//
// 文件存在但读不懂时**报错而不是重新生成**：静默生成会让全机队在某次
// 磁盘故障后集体掉线，且现场没有任何线索。
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.priv != nil {
		return nil
	}

	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		priv, err := parsePEM(b)
		if err != nil {
			// 注意：不把文件内容放进错误信息。
			return fmt.Errorf("解析 hub 密钥 %s: %w", s.path, err)
		}
		s.priv = priv
	case errors.Is(err, os.ErrNotExist):
		_, priv, genErr := ed25519.GenerateKey(rand.Reader)
		if genErr != nil {
			return fmt.Errorf("生成 hub 密钥: %w", genErr)
		}
		der, mErr := x509.MarshalPKCS8PrivateKey(priv)
		if mErr != nil {
			return fmt.Errorf("序列化 hub 密钥: %w", mErr)
		}
		encoded := pem.EncodeToMemory(&pem.Block{Type: pemBlockType, Bytes: der})
		if wErr := atomicfile.Write(s.path, encoded, 0o600); wErr != nil {
			return fmt.Errorf("写入 hub 密钥: %w", wErr)
		}
		s.priv = priv
	default:
		return fmt.Errorf("读取 hub 密钥 %s: %w", s.path, err)
	}

	s.pub = s.priv.Public().(ed25519.PublicKey)
	return nil
}

func parsePEM(b []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemBlockType {
		return nil, errors.New("不是合法的 PEM 私钥块")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("PKCS#8 解析失败")
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("期望 Ed25519 私钥，实际是 %T", key)
	}
	return priv, nil
}

// PublicKey 返回公钥；Load 之前返回 nil。
func (s *Store) PublicKey() ed25519.PublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pub
}

// PublicKeyBase64 返回 wire/DB 统一编码的公钥。
func (s *Store) PublicKeyBase64() string {
	pub := s.PublicKey()
	if pub == nil {
		return ""
	}
	return protocol.EncodePublicKey(pub)
}

// Fingerprint 返回 hub 公钥指纹，用于设置页展示与 --hub-key 带外校验。
func (s *Store) Fingerprint() string {
	pub := s.PublicKey()
	if pub == nil {
		return ""
	}
	return protocol.Fingerprint(pub)
}

// Sign 用 hub 私钥签名。Load 之前返回 nil。
func (s *Store) Sign(msg []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.priv == nil {
		return nil
	}
	return ed25519.Sign(s.priv, msg)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./hub/internal/identity/... -v`
Expected: PASS（9 个用例）

- [ ] **Step 5: 把加载接进 hub 装配**

修改 `hub/hub.go`：在 import 中加入 `"path/filepath"`、`"crypto/ed25519"` 与 `hub/internal/identity`；给 `Hub` 加字段并在 `Attach` 的 `OnServe` 里加载。

```go
type Hub struct {
	core.App

	cfg Config

	identity *identity.Store

	pb *pocketbase.PocketBase
}
```

`Attach` 中的 `OnServe` 改成：

```go
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// DataDir 只有在 Bootstrap 之后才有值，所以密钥路径必须在这里算，
		// 不能在 Attach 时算。
		h.identity = identity.NewStore(filepath.Join(e.App.DataDir(), identity.KeyFileName))
		if err := h.identity.Load(); err != nil {
			return fmt.Errorf("加载 hub 密钥: %w", err)
		}
		e.App.Logger().Info("hub 身份就绪", "fingerprint", h.identity.Fingerprint())

		if err := h.registerUI(e); err != nil {
			return err
		}
		return e.Next()
	})
```

并新增访问器：

```go
// PublicKey 返回 hub 的公钥。OnServe 之前返回 nil。
func (h *Hub) PublicKey() ed25519.PublicKey {
	if h.identity == nil {
		return nil
	}
	return h.identity.PublicKey()
}
```

- [ ] **Step 6: 给 hub 补一条集成断言**

在 `internal/testsupport/hub_test.go` 追加：

```go
func TestTestHubHasIdentityAfterServe(t *testing.T) {
	th := testsupport.NewTestHub(t)
	require.NotNil(t, th.Hub.PublicKey(), "OnServe 之后 hub 密钥必须已加载")

	// 密钥落在数据目录里，会被 PocketBase 的备份一起带走（spec §4.5）
	require.FileExists(t, filepath.Join(th.App.DataDir(), "orciny_hub_key.pem"))
}
```

（记得补 `path/filepath` 的 import。）

- [ ] **Step 7: 运行测试确认通过**

Run: `go test -tags=testing ./hub/... ./internal/... -v`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add hub internal/testsupport
git commit -m "feat: hub 长期密钥的生成、加载与装配"
```

---

## Task 2: agent 密钥与 hub 公钥钉扎

**Files:**
- Create: `agent/internal/identity/identity.go`, `agent/internal/identity/identity_test.go`

**Interfaces:**
- Consumes: `protocol.Fingerprint`、`protocol.EncodePublicKey`、`protocol.DecodePublicKey`、`internal/atomicfile.Write`
- Produces:
  ```go
  const (
      DirName      = "identity"
      KeyFileName  = "agent.key"
      HubKeyFile   = "hub.pub"
  )
  type Identity struct {
      Priv ed25519.PrivateKey
      Pub  ed25519.PublicKey
  }
  func LoadOrCreate(dir string) (*Identity, error)  // dir = <agentDir>/identity
  func Load(dir string) (*Identity, error)
  func (i *Identity) Fingerprint() string
  func (i *Identity) PublicKeyBase64() string
  func (i *Identity) Sign(msg []byte) []byte

  func PinHubKey(dir string, pub ed25519.PublicKey) error
  func LoadHubKey(dir string) (ed25519.PublicKey, error)
  var ErrNoIdentity = errors.New(...)
  var ErrNoHubKey   = errors.New(...)
  ```

- [ ] **Step 1: 写失败的测试**

创建 `agent/internal/identity/identity_test.go`：

```go
package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestLoadOrCreateGeneratesKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)

	id, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	require.Len(t, id.Pub, ed25519.PublicKeySize)
	require.FileExists(t, filepath.Join(dir, identity.KeyFileName))
}

func TestAgentKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	dir := filepath.Join(t.TempDir(), identity.DirName)
	_, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	st, err := os.Stat(filepath.Join(dir, identity.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)

	a, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	b, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	require.Equal(t, a.Pub, b.Pub, "已有密钥时不得重新生成——那等于把这台机器变成新机器")
	require.Equal(t, a.Fingerprint(), b.Fingerprint())
}

func TestLoadWithoutKeyReportsErrNoIdentity(t *testing.T) {
	_, err := identity.Load(filepath.Join(t.TempDir(), identity.DirName))
	require.ErrorIs(t, err, identity.ErrNoIdentity)
}

func TestFingerprintMatchesProtocolDerivation(t *testing.T) {
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)
	require.Equal(t, protocol.Fingerprint(id.Pub), id.Fingerprint())
	require.Len(t, id.Fingerprint(), 32)
}

func TestSignIsVerifiable(t *testing.T) {
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)

	msg := protocol.AgentSigPayload(make([]byte, protocol.NonceSize), make([]byte, protocol.NonceSize))
	require.True(t, ed25519.Verify(id.Pub, msg, id.Sign(msg)))
}

func TestPinAndLoadHubKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	require.NoError(t, identity.PinHubKey(dir, pub))
	got, err := identity.LoadHubKey(dir)
	require.NoError(t, err)
	require.Equal(t, pub, got)
}

func TestHubKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	dir := filepath.Join(t.TempDir(), identity.DirName)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, identity.PinHubKey(dir, pub))

	st, err := os.Stat(filepath.Join(dir, identity.HubKeyFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestLoadHubKeyMissing(t *testing.T) {
	_, err := identity.LoadHubKey(filepath.Join(t.TempDir(), identity.DirName))
	require.ErrorIs(t, err, identity.ErrNoHubKey)
}

func TestLoadHubKeyRejectsGarbage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, identity.HubKeyFile), []byte("不是密钥"), 0o600))

	_, err := identity.LoadHubKey(dir)
	require.Error(t, err)
	// 被篡改的 hub.pub 会导致 agent 进入 Compromised（spec §7.2），
	// 因此这里必须是硬错误，不能容错解析。
}

// 钉扎是一次性的：PinHubKey 覆盖已有文件的行为要明确。
// 覆盖由调用方（enroll 流程）决定，本层只负责写。
func TestPinHubKeyOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	first, _, _ := ed25519.GenerateKey(rand.Reader)
	second, _, _ := ed25519.GenerateKey(rand.Reader)

	require.NoError(t, identity.PinHubKey(dir, first))
	require.NoError(t, identity.PinHubKey(dir, second))

	got, err := identity.LoadHubKey(dir)
	require.NoError(t, err)
	require.Equal(t, second, got)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./agent/internal/identity/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写实现**

创建 `agent/internal/identity/identity.go`：

```go
// Package identity 管理 agent 的机器身份：自己的 Ed25519 密钥对，
// 以及钉扎的 hub 公钥（spec §4.3 / §7.1）。
//
// 私钥永不上网。指纹是公钥的函数，因此「删掉这个目录」等价于「这是一台
// 新机器」——密钥丢了本来就该重新建立信任。
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

const (
	// DirName 是 agent 目录下存放密钥的子目录名：~/.orciny/identity/
	DirName = "identity"
	// KeyFileName 是 agent 私钥文件名。
	KeyFileName = "agent.key"
	// HubKeyFile 是钉扎的 hub 公钥文件名。
	HubKeyFile = "hub.pub"

	pemBlockType = "PRIVATE KEY"
)

var (
	// ErrNoIdentity 表示本机还没有密钥——通常意味着尚未 enroll。
	ErrNoIdentity = errors.New("identity: 本机尚无 agent 密钥，请先执行 orciny-agent enroll")
	// ErrNoHubKey 表示还没钉扎 hub 公钥。
	ErrNoHubKey = errors.New("identity: 尚未钉扎 hub 公钥，请先执行 orciny-agent enroll")
)

// Identity 是本机的密钥对。
type Identity struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// LoadOrCreate 读取已有密钥；没有则生成一对并落盘（0600）。
// dir 是 identity 子目录的完整路径。
func LoadOrCreate(dir string) (*Identity, error) {
	id, err := Load(dir)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNoIdentity) {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: 生成密钥: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("identity: 序列化私钥: %w", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: pemBlockType, Bytes: der})
	if err := atomicfile.Write(filepath.Join(dir, KeyFileName), encoded, 0o600); err != nil {
		return nil, fmt.Errorf("identity: 写入私钥: %w", err)
	}
	return &Identity{Priv: priv, Pub: pub}, nil
}

// Load 只读取，不生成。密钥不存在时返回 ErrNoIdentity。
func Load(dir string) (*Identity, error) {
	path := filepath.Join(dir, KeyFileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoIdentity
	}
	if err != nil {
		return nil, fmt.Errorf("identity: 读取 %s: %w", path, err)
	}

	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemBlockType {
		return nil, fmt.Errorf("identity: %s 不是合法的 PEM 私钥块", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: %s 的 PKCS#8 解析失败", path)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("identity: %s 期望 Ed25519 私钥，实际是 %T", path, key)
	}
	return &Identity{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
}

// Fingerprint 返回本机指纹，与 hub 侧 machines.fingerprint 一致。
func (i *Identity) Fingerprint() string { return protocol.Fingerprint(i.Pub) }

// PublicKeyBase64 返回 enroll 请求里要带的公钥表示。
func (i *Identity) PublicKeyBase64() string { return protocol.EncodePublicKey(i.Pub) }

// Sign 用 agent 私钥签名。
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.Priv, msg) }

// PinHubKey 把 hub 公钥钉扎到本地（TOFU，spec §4.4）。
// 是否允许覆盖由调用方决定——本层只负责写。
func PinHubKey(dir string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("identity: hub 公钥长度应为 %d 字节，实际 %d", ed25519.PublicKeySize, len(pub))
	}
	data := []byte(protocol.EncodePublicKey(pub) + "\n")
	if err := atomicfile.Write(filepath.Join(dir, HubKeyFile), data, 0o600); err != nil {
		return fmt.Errorf("identity: 写入 hub 公钥: %w", err)
	}
	return nil
}

// LoadHubKey 读取钉扎的 hub 公钥。
//
// 解析失败一律是硬错误：hub.pub 被改动意味着要么遭中间人，要么 hub 换了
// 密钥，两种情况都需要人来判断（spec §7.2）。
func LoadHubKey(dir string) (ed25519.PublicKey, error) {
	path := filepath.Join(dir, HubKeyFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoHubKey
	}
	if err != nil {
		return nil, fmt.Errorf("identity: 读取 %s: %w", path, err)
	}
	pub, err := protocol.DecodePublicKey(trimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("identity: %s 内容无法解析为 hub 公钥: %w", path, err)
	}
	return pub, nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
```

> **给执行者的说明：** `trimSpace` 手写是为了避免为一个换行符引入 `strings` 的心理负担——如果你觉得直接用 `strings.TrimSpace` 更清楚，就用它，两者行为在此处等价。保持一致即可，别两种都留着。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./agent/internal/identity/... -v`
Expected: PASS（11 个用例）

- [ ] **Step 5: 确认边界没被破坏**

Run: `go list -deps ./agent/... | grep 'orciny/hub' ; go list -deps ./hub/... | grep 'orciny/agent'`
Expected: 两条命令都无输出（grep 退出码 1 属正常）

- [ ] **Step 6: 全量测试**

Run: `make test && make lint`
Expected: 全绿

- [ ] **Step 7: 提交**

```bash
git add agent/internal/identity
git commit -m "feat: agent 密钥对与 hub 公钥 TOFU 钉扎"
```

---

## 完成检查

- [ ] hub 密钥：缺则生成、0600、重启稳定、损坏报错、路径在数据目录内
- [ ] agent 密钥：缺则生成、0600、已有不覆盖、指纹与 `protocol.Fingerprint` 一致
- [ ] `hub.pub` 可钉扎、可读回、被篡改则硬错误
- [ ] 任何错误信息里都没有密钥内容
- [ ] 交付给计划 4–5 的接口与统括计划「全局接口契约」一致
