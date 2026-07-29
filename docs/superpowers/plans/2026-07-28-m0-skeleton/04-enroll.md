# M0 计划 4 · enroll Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打通「UI 点添加机器 → 一行命令 → 机器出现在列表里」这条链路：token 签发与原子核销、三个 HTTP 端点、agent 侧密钥生成与 hub 公钥 TOFU 钉扎。

**Architecture:** token 明文只在签发响应里出现一次，库里只存 SHA256。核销是一条带条件的 `UPDATE ... WHERE used_at = ''`，靠 rows affected 决出唯一赢家，整个 enroll 包在一个事务里。已核销 token + 相同公钥视为同一次 enroll 的重放并正常返回——判据是公钥而非 token，因此偷到已用 token 的人注册不了自己的密钥。

**Tech Stack:** Go 1.26 · PocketBase v0.39.9（`RunInTransaction` / dbx raw query）· 标准库 `crypto/rand` `crypto/sha256` `net/http`

**上位文档：** [统括计划](00-overview.md) · [spec §4.4 §4.6 §8 §9.1](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；共享只走 `protocol/`；`hub/internal/*` 的单测写在包内。
- **注册 token 在任何日志中只记前 8 位**；私钥与 hub 公钥内容一律不入日志。
- 事件 kind 只允许统括计划里列的七个字符串。
- 测试禁用 `time.Sleep`；超时走注入的 `clock.Clock`；断言用 `testify/require`。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `hub/internal/events/writer.go` | 事件写入，kind 常量的唯一来源 |
| `hub/internal/enroll/service.go` | token 签发与核销、机器建/更新、幂等重放判定 |
| `hub/internal/enroll/errors.go` | 分类错误，路由据此映射 HTTP 状态码 |
| `hub/internal/routes/routes.go` | `Register`，自定义路由的唯一注册点 |
| `hub/internal/routes/enroll.go` | 三个端点的 handler |
| `hub/hub.go` | 修改：装配 events / enroll / routes，新增 `IssueEnrollToken` |
| `agent/internal/probe/probe.go` | `Host()`：hostname / GOOS / GOARCH（计划 7 在此补工具版本探测） |
| `agent/internal/enroll/enroll.go` | agent 侧 enroll：生成密钥 → POST → 校验并钉扎 hub 公钥 |
| `agent/enroll.go` | 公开入口 `agent.Enroll`，CLI 与测试脚手架都走它 |
| `agent/cli.go` | 修改：新增 `enroll` 子命令 |
| `internal/testsupport/agent.go` | `NewTestAgent`：一台已 enroll 的测试机器 |

---

## Task 1: 事件写入

**Files:**
- Create: `hub/internal/events/writer.go`, `hub/internal/events/writer_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  ```go
  const (
      KindMachineEnrolled     = "machine.enrolled"
      KindMachineReEnrolled   = "machine.re-enrolled"
      KindMachineConnected    = "machine.connected"
      KindMachineDisconnected = "machine.disconnected"
      KindMachineRemoved      = "machine.removed"
      KindTokenIssued         = "token.issued"
      KindAuthFailed          = "auth.failed"
  )
  type Writer struct{ /* 私有字段 */ }
  func NewWriter(app core.App) *Writer
  func (w *Writer) Write(kind, machineID string, detail map[string]any) error
  func (w *Writer) WriteTx(txApp core.App, kind, machineID string, detail map[string]any) error
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/events/writer_test.go`：

```go
package events_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestWriteCreatesEventRecord(t *testing.T) {
	app := newApp(t)
	w := events.NewWriter(app)

	require.NoError(t, w.Write(events.KindTokenIssued, "", map[string]any{"prefix": "abcd1234"}))

	recs, err := app.FindRecordsByFilter("events", "kind = 'token.issued'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Empty(t, recs[0].GetString("machine"))
	require.Equal(t, "abcd1234", recs[0].GetString("detail.prefix"))
}

func TestWriteLinksMachine(t *testing.T) {
	app := newApp(t)
	m := newMachine(t, app, "fp-1")

	require.NoError(t, events.NewWriter(app).Write(events.KindMachineEnrolled, m.Id, nil))

	recs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, m.Id, recs[0].GetString("machine"))
}

func TestWriteTxUsesGivenApp(t *testing.T) {
	app := newApp(t)
	w := events.NewWriter(app)

	// 事务回滚时事件必须一并消失，否则会留下「记录说建过、实际没建」的假事件。
	err := app.RunInTransaction(func(tx core.App) error {
		if err := w.WriteTx(tx, events.KindMachineEnrolled, "", nil); err != nil {
			return err
		}
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	recs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Empty(t, recs)
}

func TestEventDetailSurvivesRoundTrip(t *testing.T) {
	app := newApp(t)
	require.NoError(t, events.NewWriter(app).Write(events.KindAuthFailed, "", map[string]any{
		"reason":      "签名不匹配",
		"fingerprint": "abc",
	}))

	recs, err := app.FindRecordsByFilter("events", "kind = 'auth.failed'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "签名不匹配", recs[0].GetString("detail.reason"))
	require.Equal(t, "abc", recs[0].GetString("detail.fingerprint"))
}

var errRollback = errRollbackType{}

type errRollbackType struct{}

func (errRollbackType) Error() string { return "回滚" }

func newMachine(t *testing.T, app core.App, fp string) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("fingerprint", fp)
	r.Set("pub_key", "k")
	r.Set("status", "offline")
	require.NoError(t, app.Save(r))
	return r
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/events/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写实现**

创建 `hub/internal/events/writer.go`：

```go
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
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./hub/internal/events/... -v`
Expected: PASS（4 个用例）

- [ ] **Step 5: 提交**

```bash
git add hub/internal/events
git commit -m "feat: 事件流写入与 kind 常量"
```

---

## Task 2: token 签发

**Files:**
- Create: `hub/internal/enroll/service.go`, `hub/internal/enroll/errors.go`, `hub/internal/enroll/issue_test.go`

**Interfaces:**
- Consumes: `events.Writer`、`clock.Clock`
- Produces:
  ```go
  const TokenBytes = 32
  type Service struct{ /* 私有字段 */ }
  func NewService(app core.App, ev *events.Writer, clk clock.Clock, ttl time.Duration, rnd io.Reader) *Service
  func (s *Service) IssueToken() (token string, expiresAt time.Time, err error)
  func (s *Service) PurgeExpiredTokens(olderThan time.Duration) (int64, error)
  func HashToken(token string) string   // hex(sha256(token))
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/enroll/issue_test.go`：

```go
package enroll_test

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/internal/clock"
)

var start = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*enroll.Service, *tests.TestApp, *clock.Fake) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(start)
	return enroll.NewService(app, events.NewWriter(app), fake, 15*time.Minute, rand.Reader), app, fake
}

func TestIssueTokenReturnsPlaintextOnce(t *testing.T) {
	s, app, _ := newService(t)

	token, expires, err := s.IssueToken()
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, start.Add(15*time.Minute), expires.UTC())

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	// 明文绝不入库
	require.NotContains(t, recs[0].GetString("token_hash"), token)
	require.Equal(t, enroll.HashToken(token), recs[0].GetString("token_hash"))
	require.Empty(t, recs[0].GetString("used_at"))
}

func TestIssueTokenIsUnguessable(t *testing.T) {
	s, _, _ := newService(t)

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		token, _, err := s.IssueToken()
		require.NoError(t, err)
		require.False(t, seen[token], "签发出重复 token")
		seen[token] = true
		require.GreaterOrEqual(t, len(token), 40, "32 字节 base64url 至少 43 字符")
		require.NotContains(t, token, "=", "URL 安全、无填充，便于放进命令行")
	}
}

func TestIssueTokenWritesEventWithPrefixOnly(t *testing.T) {
	s, app, _ := newService(t)

	token, _, err := s.IssueToken()
	require.NoError(t, err)

	recs, err := app.FindRecordsByFilter("events", "kind = 'token.issued'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)

	prefix := recs[0].GetString("detail.token_prefix")
	require.Len(t, prefix, 8, "只记前 8 位（spec §7.5）")
	require.True(t, strings.HasPrefix(token, prefix))
}

func TestIssueTokenHonoursTTL(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(start)
	s := enroll.NewService(app, events.NewWriter(app), fake, time.Hour, rand.Reader)

	_, expires, err := s.IssueToken()
	require.NoError(t, err)
	require.Equal(t, start.Add(time.Hour), expires.UTC())
}

func TestHashTokenIsHexSHA256(t *testing.T) {
	h := enroll.HashToken("abc")
	require.Len(t, h, 64)
	require.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", h)
}
```

（本文件不需要 `core` 的 import；上面的 import 块里去掉它，Task 3 的测试文件会各自引入所需的包。）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/enroll/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写错误类型**

创建 `hub/internal/enroll/errors.go`：

```go
package enroll

import "errors"

// 这些错误决定 HTTP 状态码与 agent 的下一步动作，因此必须可 errors.Is。
// 对外的错误信息刻意保持粗粒度：不告诉调用方「token 存在但过期了」还是
// 「token 根本不存在」，避免变成探测接口。
var (
	// ErrTokenInvalid 表示 token 不存在或格式不对。
	ErrTokenInvalid = errors.New("enroll: 注册 token 无效")
	// ErrTokenExpired 表示 token 已过期。
	ErrTokenExpired = errors.New("enroll: 注册 token 已过期")
	// ErrTokenUsed 表示 token 已被核销，且本次请求不构成幂等重放。
	ErrTokenUsed = errors.New("enroll: 注册 token 已被使用")
	// ErrBadPubKey 表示请求里的公钥无法解析。
	ErrBadPubKey = errors.New("enroll: 公钥格式错误")
	// ErrBadRequest 表示必填字段缺失。
	ErrBadRequest = errors.New("enroll: 请求字段不完整")
)
```

- [ ] **Step 4: 写签发实现**

创建 `hub/internal/enroll/service.go`：

```go
// Package enroll 实现注册 token 的签发与核销，以及机器记录的建立。
//
// 信任根落在那条 curl 命令上（spec §4.4）：走 HTTPS、token 一次性、
// 15 分钟有效。攻击者要冒充 hub，必须恰好在 enroll 那一刻做中间人且
// 持有有效证书；钉扎完成后即免疫。
package enroll

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// TokenBytes 是注册 token 的随机字节数。
const TokenBytes = 32

// Service 是 enroll 的业务逻辑，全部数据库访问都在这里，路由层只做编解码。
type Service struct {
	app core.App
	ev  *events.Writer
	clk clock.Clock
	ttl time.Duration
	rnd io.Reader
}

func NewService(app core.App, ev *events.Writer, clk clock.Clock, ttl time.Duration, rnd io.Reader) *Service {
	return &Service{app: app, ev: ev, clk: clk, ttl: ttl, rnd: rnd}
}

// HashToken 是 token 在库里的唯一表示。明文只在签发响应里出现一次。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueToken 签发一枚一次性注册 token，返回明文与过期时刻。
func (s *Service) IssueToken() (string, time.Time, error) {
	raw := make([]byte, TokenBytes)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 生成 token: %w", err)
	}
	// base64url 无填充：可以直接贴进命令行，不需要引号。
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := s.clk.Now().Add(s.ttl)

	c, err := s.app.FindCollectionByNameOrId("enroll_tokens")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 找不到 collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("token_hash", HashToken(token))
	r.Set("expires_at", expires.UTC())
	if err := s.app.Save(r); err != nil {
		return "", time.Time{}, fmt.Errorf("enroll: 保存 token: %w", err)
	}

	// 只记前 8 位（spec §7.5）——够用来在事件流里对上号，又泄不出去。
	if err := s.ev.Write(events.KindTokenIssued, "", map[string]any{
		"token_prefix": token[:8],
		"expires_at":   expires.UTC().Format(time.RFC3339),
	}); err != nil {
		s.app.Logger().Warn("写 token.issued 事件失败", "error", err)
	}

	return token, expires, nil
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./hub/internal/enroll/... -v`
Expected: PASS（5 个用例）

- [ ] **Step 6: 写过期 token 清理的失败测试**

spec §8 要求 hub 每小时清一次过期 token。核销记录留一天是为了让「刚才那次
安装到底怎么了」还能查得到，再久就没意义了。

在 `hub/internal/enroll/issue_test.go` 追加：

```go
func TestPurgeExpiredTokensRemovesOldOnes(t *testing.T) {
	s, app, fake := newService(t)

	_, _, err := s.IssueToken()
	require.NoError(t, err)

	// 过期后又过了一天多
	fake.Advance(15*time.Minute + 25*time.Hour)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Empty(t, recs)
}

func TestPurgeExpiredTokensKeepsRecentOnes(t *testing.T) {
	s, app, fake := newService(t)

	_, _, err := s.IssueToken()
	require.NoError(t, err)

	// 已过期，但还在保留窗口内 —— 排查「刚才那次安装」时还用得上
	fake.Advance(15*time.Minute + time.Hour)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
}

func TestPurgeExpiredTokensKeepsValidOnes(t *testing.T) {
	s, app, _ := newService(t)
	_, _, err := s.IssueToken()
	require.NoError(t, err)

	n, err := s.PurgeExpiredTokens(24 * time.Hour)
	require.NoError(t, err)
	require.EqualValues(t, 0, n)

	recs, err := app.FindRecordsByFilter("enroll_tokens", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
}
```

- [ ] **Step 7: 写清理实现**

在 `hub/internal/enroll/service.go` 追加（import 需补 `github.com/pocketbase/dbx` 与 `github.com/pocketbase/pocketbase/tools/types`）：

```go
// PurgeExpiredTokens 删除过期超过 olderThan 的 token 记录（spec §8）。
// 返回删除的行数。
//
// 保留一段时间而不是一过期就删：排查「刚才那次安装为什么失败」时，
// 事件流里的 token 前缀要能对上一条真实记录。
func (s *Service) PurgeExpiredTokens(olderThan time.Duration) (int64, error) {
	cutoff, err := types.ParseDateTime(s.clk.Now().UTC().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("enroll: 格式化时间: %w", err)
	}
	res, err := s.app.DB().NewQuery(`
		DELETE FROM {{enroll_tokens}} WHERE expires_at < {:cutoff}
	`).Bind(dbx.Params{"cutoff": cutoff.String()}).Execute()
	if err != nil {
		return 0, fmt.Errorf("enroll: 清理过期 token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("enroll: 读取清理结果: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./hub/internal/enroll/... -v`
Expected: PASS（8 个用例）

- [ ] **Step 9: 提交**

```bash
git add hub/internal/enroll
git commit -m "feat: 一次性注册 token 的签发与过期清理"
```

---

## Task 3: token 核销与机器建立

这是 M0 并发正确性最吃紧的一处。核销必须是一条带条件的 UPDATE，靠 rows affected 决出赢家；用「先查再写」会在并发下让同一枚 token 建出两台机器。

**Files:**
- Create: `hub/internal/enroll/consume.go`, `hub/internal/enroll/consume_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Service`、`protocol.DecodePublicKey`、`protocol.Fingerprint`
- Produces:
  ```go
  type Request struct {
      Token        string
      PubKey       string // base64（protocol.EncodePublicKey 的输出）
      Hostname     string
      OS           string
      Arch         string
      AgentVersion string
  }
  type Result struct {
      MachineID   string
      Fingerprint string
      ReEnrolled  bool // 指纹已存在，更新而非新建
      Replayed    bool // 已核销 token + 相同公钥的幂等重放
  }
  func (s *Service) Enroll(req Request) (*Result, error)
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/enroll/consume_test.go`：

```go
package enroll_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/protocol"
)

func newKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, protocol.EncodePublicKey(pub)
}

func req(token, pubKey string) enroll.Request {
	return enroll.Request{
		Token: token, PubKey: pubKey,
		Hostname: "box", OS: "linux", Arch: "arm64", AgentVersion: "0.1.0",
	}
}

func TestEnrollHappyPath(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	pub, pubKey := newKey(t)

	res, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)
	require.False(t, res.ReEnrolled)
	require.False(t, res.Replayed)
	require.Equal(t, protocol.Fingerprint(pub), res.Fingerprint)

	m, err := app.FindRecordById("machines", res.MachineID)
	require.NoError(t, err)
	require.Equal(t, "box", m.GetString("name"), "备注名默认取 hostname")
	require.Equal(t, "box", m.GetString("hostname"))
	require.Equal(t, "linux", m.GetString("os"))
	require.Equal(t, "arm64", m.GetString("arch"))
	require.Equal(t, "0.1.0", m.GetString("agent_version"))
	require.Equal(t, pubKey, m.GetString("pub_key"))
	require.Equal(t, "offline", m.GetString("status"), "刚 enroll 还没连上，必须是 offline")

	// token 已核销并回填了机器
	tk, err := app.FindFirstRecordByData("enroll_tokens", "token_hash", enroll.HashToken(token))
	require.NoError(t, err)
	require.NotEmpty(t, tk.GetString("used_at"))
	require.Equal(t, res.MachineID, tk.GetString("machine"))

	// 事件
	evs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, res.MachineID, evs[0].GetString("machine"))
}

func TestEnrollRejectsExpiredToken(t *testing.T) {
	s, _, fake := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	fake.Advance(15*time.Minute + time.Second)

	_, err = s.Enroll(req(token, pubKey))
	require.ErrorIs(t, err, enroll.ErrTokenExpired)
}

func TestEnrollRejectsUnknownToken(t *testing.T) {
	s, _, _ := newService(t)
	_, pubKey := newKey(t)

	_, err := s.Enroll(req("从没签发过的 token", pubKey))
	require.ErrorIs(t, err, enroll.ErrTokenInvalid)
}

func TestEnrollRejectsUsedTokenWithDifferentKey(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, first := newKey(t)
	_, err = s.Enroll(req(token, first))
	require.NoError(t, err)

	// 偷到已用 token 的攻击者拿自己的公钥来注册 —— 必须拒绝
	_, second := newKey(t)
	_, err = s.Enroll(req(token, second))
	require.ErrorIs(t, err, enroll.ErrTokenUsed)
}

// 响应在网络上丢了：agent 手里有私钥却没有 hub 公钥，token 又已核销。
// 重放同一请求必须成功，否则这台机器就卡死了（spec §4.4）。
func TestEnrollIdempotentReplay(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	first, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)

	second, err := s.Enroll(req(token, pubKey))
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, first.MachineID, second.MachineID)
	require.Equal(t, first.Fingerprint, second.Fingerprint)

	machines, err := app.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1, "重放不得建出第二台机器")
}

// 重装 agent 是常见操作：指纹已存在时更新而非重复创建，
// 但仍然要求一枚有效 token —— 否则任何人都能用伪造公钥覆盖已有机器。
func TestReEnrollUpdatesExistingMachine(t *testing.T) {
	s, app, _ := newService(t)
	pub, pubKey := newKey(t)

	t1, _, err := s.IssueToken()
	require.NoError(t, err)
	first, err := s.Enroll(req(t1, pubKey))
	require.NoError(t, err)

	// 用户改过备注名，re-enroll 不该覆盖它
	m, err := app.FindRecordById("machines", first.MachineID)
	require.NoError(t, err)
	m.Set("name", "我的构建机")
	require.NoError(t, app.Save(m))

	t2, _, err := s.IssueToken()
	require.NoError(t, err)
	second, err := s.Enroll(enroll.Request{
		Token: t2, PubKey: pubKey,
		Hostname: "box-renamed", OS: "linux", Arch: "arm64", AgentVersion: "0.2.0",
	})
	require.NoError(t, err)
	require.True(t, second.ReEnrolled)
	require.Equal(t, first.MachineID, second.MachineID)
	require.Equal(t, protocol.Fingerprint(pub), second.Fingerprint)

	m2, err := app.FindRecordById("machines", first.MachineID)
	require.NoError(t, err)
	require.Equal(t, "我的构建机", m2.GetString("name"), "用户改的备注名必须保留")
	require.Equal(t, "box-renamed", m2.GetString("hostname"))
	require.Equal(t, "0.2.0", m2.GetString("agent_version"))

	evs, err := app.FindRecordsByFilter("events", "kind = 'machine.re-enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, evs, 1)
}

func TestReEnrollWithoutTokenIsRejected(t *testing.T) {
	s, _, _ := newService(t)
	_, pubKey := newKey(t)

	t1, _, err := s.IssueToken()
	require.NoError(t, err)
	_, err = s.Enroll(req(t1, pubKey))
	require.NoError(t, err)

	_, err = s.Enroll(req("", pubKey))
	require.Error(t, err, "没有有效 token 就不能改写已有机器的公钥")
}

// 同一 token 并发两次，恰好一个成功。
func TestConcurrentEnrollExactlyOneWins(t *testing.T) {
	s, app, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, keyA := newKey(t)
	_, keyB := newKey(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	keys := []string{keyA, keyB}
	start := make(chan struct{})

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.Enroll(req(token, keys[i]))
		}(i)
	}
	close(start)
	wg.Wait()

	okCount := 0
	for _, err := range errs {
		if err == nil {
			okCount++
		}
	}
	require.Equal(t, 1, okCount, "恰好一个请求应当成功，实际 %d 个：%v", okCount, errs)

	machines, err := app.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1, "一枚 token 只能建出一台机器")
}

func TestEnrollRejectsBadPubKey(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)

	_, err = s.Enroll(req(token, "不是 base64 公钥"))
	require.ErrorIs(t, err, enroll.ErrBadPubKey)
}

func TestEnrollRejectsEmptyFields(t *testing.T) {
	s, _, _ := newService(t)
	token, _, err := s.IssueToken()
	require.NoError(t, err)
	_, pubKey := newKey(t)

	_, err = s.Enroll(enroll.Request{Token: token, PubKey: pubKey})
	require.ErrorIs(t, err, enroll.ErrBadRequest)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/enroll/... -run TestEnroll -v`
Expected: FAIL —— `s.Enroll undefined`

- [ ] **Step 3: 写实现**

创建 `hub/internal/enroll/consume.go`：

```go
package enroll

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

// Request 是一次 enroll 的输入。字段与 POST /api/orciny/enroll 的 body 一一对应。
type Request struct {
	Token        string
	PubKey       string
	Hostname     string
	OS           string
	Arch         string
	AgentVersion string
}

// Result 是 enroll 的结果。ReEnrolled 与 Replayed 只影响日志与事件，
// 对 agent 而言三种成功路径没有区别。
type Result struct {
	MachineID   string
	Fingerprint string
	ReEnrolled  bool
	Replayed    bool
}

// Enroll 核销 token 并建立/更新机器记录，整个过程在一个事务里。
func (s *Service) Enroll(req Request) (*Result, error) {
	if req.Token == "" || req.Hostname == "" || req.OS == "" || req.Arch == "" || req.AgentVersion == "" {
		return nil, ErrBadRequest
	}
	pub, err := protocol.DecodePublicKey(req.PubKey)
	if err != nil {
		return nil, ErrBadPubKey
	}
	fingerprint := protocol.Fingerprint(pub)
	hash := HashToken(req.Token)
	now, err := types.ParseDateTime(s.clk.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("enroll: 格式化时间: %w", err)
	}

	var result Result
	txErr := s.app.RunInTransaction(func(tx core.App) error {
		// 核销：条件 UPDATE 是唯一的并发仲裁点。
		// 「先 SELECT 再 UPDATE」在两个请求同时到达时会双双通过检查。
		res, err := tx.DB().NewQuery(`
			UPDATE {{enroll_tokens}}
			SET used_at = {:now}
			WHERE token_hash = {:hash} AND used_at = '' AND expires_at > {:now}
		`).Bind(dbx.Params{"now": now.String(), "hash": hash}).Execute()
		if err != nil {
			return fmt.Errorf("enroll: 核销 token: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("enroll: 读取核销结果: %w", err)
		}

		if affected == 0 {
			return s.handleNotConsumed(tx, hash, req.PubKey, now, &result)
		}

		machineID, reEnrolled, err := s.upsertMachine(tx, req, fingerprint)
		if err != nil {
			return err
		}
		result = Result{MachineID: machineID, Fingerprint: fingerprint, ReEnrolled: reEnrolled}

		// 回填 token → machine，供幂等重放判定
		tk, err := tx.FindFirstRecordByData("enroll_tokens", "token_hash", hash)
		if err != nil {
			return fmt.Errorf("enroll: 回读 token: %w", err)
		}
		tk.Set("machine", machineID)
		if err := tx.Save(tk); err != nil {
			return fmt.Errorf("enroll: 回填 token 的机器关联: %w", err)
		}

		kind := events.KindMachineEnrolled
		if reEnrolled {
			kind = events.KindMachineReEnrolled
		}
		return s.ev.WriteTx(tx, kind, machineID, map[string]any{
			"fingerprint":   fingerprint,
			"hostname":      req.Hostname,
			"agent_version": req.AgentVersion,
		})
	})
	if txErr != nil {
		return nil, txErr
	}
	return &result, nil
}

// handleNotConsumed 处理「没抢到 token」的三种情况：不存在、过期、已核销。
// 已核销时还要判断是不是同一次 enroll 的幂等重放。
func (s *Service) handleNotConsumed(tx core.App, hash, pubKey string, now types.DateTime, out *Result) error {
	tk, err := tx.FindFirstRecordByData("enroll_tokens", "token_hash", hash)
	if err != nil {
		return ErrTokenInvalid
	}

	if tk.GetString("used_at") == "" {
		// 没被用过却没抢到，只可能是过期。
		return ErrTokenExpired
	}

	machineID := tk.GetString("machine")
	if machineID == "" {
		return ErrTokenUsed
	}
	m, err := tx.FindRecordById("machines", machineID)
	if err != nil {
		return ErrTokenUsed
	}
	// 判据是公钥而非 token：偷到已用 token 的人没有对应的私钥，
	// 拿自己的公钥来重放会在这里被挡下。
	if m.GetString("pub_key") != pubKey {
		return ErrTokenUsed
	}

	*out = Result{
		MachineID:   m.Id,
		Fingerprint: m.GetString("fingerprint"),
		Replayed:    true,
	}
	return nil
}

// upsertMachine 按指纹建或更新机器记录，返回记录 id 与是否为 re-enroll。
func (s *Service) upsertMachine(tx core.App, req Request, fingerprint string) (string, bool, error) {
	existing, err := tx.FindFirstRecordByData("machines", "fingerprint", fingerprint)
	if err == nil {
		// 重装 agent 是常见操作，更新比拒绝友好。备注名是用户改过的，不动。
		applyMachineFields(existing, req)
		if err := tx.Save(existing); err != nil {
			return "", false, fmt.Errorf("enroll: 更新机器记录: %w", err)
		}
		return existing.Id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		// 真正的查询错误（比如库锁死）必须往上抛，不能当成「没这台机器」
		// 而去建一台重复的。
		return "", false, fmt.Errorf("enroll: 查询机器记录: %w", err)
	}

	c, err := tx.FindCollectionByNameOrId("machines")
	if err != nil {
		return "", false, fmt.Errorf("enroll: 找不到 machines collection: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("fingerprint", fingerprint)
	r.Set("name", req.Hostname) // 备注名默认取 hostname，用户可改
	r.Set("status", "offline")  // 还没建立 WS 连接
	applyMachineFields(r, req)
	if err := tx.Save(r); err != nil {
		return "", false, fmt.Errorf("enroll: 创建机器记录: %w", err)
	}
	return r.Id, false, nil
}

func applyMachineFields(r *core.Record, req Request) {
	r.Set("pub_key", req.PubKey)
	r.Set("hostname", req.Hostname)
	r.Set("os", req.OS)
	r.Set("arch", req.Arch)
	r.Set("agent_version", req.AgentVersion)
}
```

- [ ] **Step 4: 确认「查不到」的错误形状**

`FindFirstRecordByData` 走的是 dbx 的 `One()`，无匹配时返回 `sql.ErrNoRows`。跑一条一次性诊断确认：

```go
func TestNotFoundErrorShape(t *testing.T) {
	_, app, _ := newService(t)
	_, err := app.FindFirstRecordByData("machines", "fingerprint", "不存在")
	require.ErrorIs(t, err, sql.ErrNoRows)
}
```

Run: `go test ./hub/internal/enroll/ -run TestNotFoundErrorShape -v`
Expected: PASS。**通过后删掉这条诊断测试**——它验证的是依赖库的行为，不是我们的行为。若它失败，按实际错误类型调整 `upsertMachine` 里的判断，别硬套 `sql.ErrNoRows`。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./hub/internal/enroll/... -race -v`
Expected: PASS（全部用例，含并发用例）

若 `TestConcurrentEnrollExactlyOneWins` 出现 `database is locked`：说明两个事务在 SQLite 上争写锁。PocketBase 默认已开启 WAL 与 busy_timeout；确认失败的那个请求返回的是 `ErrTokenUsed`/`ErrTokenInvalid` 而非锁错误。如果确实是锁错误，把 `Enroll` 的整体重试交给调用方（`install.sh` 已有两次重试），并在测试里断言「成功数为 1」而非「错误类型」。

- [ ] **Step 6: 提交**

```bash
git add hub/internal/enroll
git commit -m "feat: token 原子核销、机器建立与幂等重放"
```

---

## Task 4: HTTP 端点与 hub 装配

**Files:**
- Create: `hub/internal/routes/routes.go`, `hub/internal/routes/enroll.go`, `hub/internal/routes/routes_test.go`
- Modify: `hub/hub.go`（装配 events / enroll / routes，新增 `IssueEnrollToken`）

**Interfaces:**
- Consumes: `enroll.Service`、`identity.Store`、`orciny.Version`
- Produces:
  ```go
  // hub/internal/routes
  type Deps struct {
      Enroll   *enroll.Service
      Identity *identity.Store
      Version  string
  }
  func Register(e *core.ServeEvent, d Deps) error

  // hub
  func (h *Hub) IssueEnrollToken() (token string, expiresAt time.Time, err error)
  ```
  HTTP 契约：
  ```
  POST /api/orciny/enroll-tokens   (superuser)
    → 200 {"token":"...","expiresAt":"RFC3339","installCommand":"curl -fsSL ..."}
  POST /api/orciny/enroll          (token 本身即凭证)
    body {"token","pubKey","hostname","os","arch","agentVersion"}
    → 200 {"hubPubKey":"base64","machineId":"...","fingerprint":"..."}
    → 400 字段不全 / 公钥格式错
    → 401 token 无效、过期或已用
  GET  /api/orciny/hub-info        (superuser)
    → 200 {"version":"0.1.0","publicKey":"base64","fingerprint":"..."}
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/internal/routes/routes_test.go`（用 `internal/testsupport` 够不到本包，所以这里自己起 app）：

```go
package routes_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/routes"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type fixture struct {
	app    *tests.TestApp
	srv    *httptest.Server
	svc    *enroll.Service
	ident  *identity.Store
	token  string
	pubKey string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	ident := identity.NewStore(t.TempDir() + "/hub.pem")
	require.NoError(t, ident.Load())

	svc := enroll.NewService(app, events.NewWriter(app), clock.NewFake(time.Now()), 15*time.Minute, rand.Reader)

	router, err := apis.NewRouter(app)
	require.NoError(t, err)
	se := new(core.ServeEvent)
	se.App = app
	se.Router = router
	se.Server = &http.Server{}
	require.NoError(t, app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		if err := routes.Register(e, routes.Deps{Enroll: svc, Identity: ident, Version: "0.1.0"}); err != nil {
			return err
		}
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		e.Server.Handler = mux
		return nil
	}))

	srv := httptest.NewServer(se.Server.Handler)
	t.Cleanup(srv.Close)

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	return &fixture{app: app, srv: srv, svc: svc, ident: ident, pubKey: protocol.EncodePublicKey(pub)}
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestEnrollTokensRequiresSuperuser(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll-tokens", map[string]any{})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"签发 token 是管理动作，未认证必须被挡住")
}

func TestHubInfoRequiresSuperuser(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.srv.URL + "/api/orciny/hub-info")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestEnrollHappyPathReturnsHubKey(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.svc.IssueToken()
	require.NoError(t, err)

	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": token, "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		HubPubKey   string `json:"hubPubKey"`
		MachineID   string `json:"machineId"`
		Fingerprint string `json:"fingerprint"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Equal(t, f.ident.PublicKeyBase64(), out.HubPubKey)
	require.NotEmpty(t, out.MachineID)
	require.Len(t, out.Fingerprint, 32)
}

func TestEnrollWithBadTokenIs401(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": "无效", "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestEnrollWithMissingFieldsIs400(t *testing.T) {
	f := newFixture(t)
	token, _, err := f.svc.IssueToken()
	require.NoError(t, err)

	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": token, "pubKey": f.pubKey,
	})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestEnrollDoesNotLeakTokenInResponse(t *testing.T) {
	f := newFixture(t)
	resp := postJSON(t, f.srv.URL+"/api/orciny/enroll", map[string]any{
		"token": "秘密token值", "pubKey": f.pubKey,
		"hostname": "box", "os": "linux", "arch": "arm64", "agentVersion": "0.1.0",
	})
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "秘密token值")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/internal/routes/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写路由注册**

创建 `hub/internal/routes/routes.go`：

```go
// Package routes 是自定义 HTTP 路由的唯一注册点。
//
// 分工（spec §5.2）：前端读机器列表与事件流走 PocketBase 的 JS SDK 与
// realtime；前端触发动作走这里的 /api/orciny/*，业务规则集中在 Go 侧。
package routes

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
	"github.com/FlintyLemming/orciny/hub/internal/identity"
)

// Deps 是路由层需要的全部依赖。路由不持有状态，只做编解码与状态码映射。
type Deps struct {
	Enroll   *enroll.Service
	Identity *identity.Store
	Version  string
}

// Register 在 OnServe 阶段注册全部自定义路由。
func Register(e *core.ServeEvent, d Deps) error {
	g := e.Router.Group("/api/orciny")

	g.POST("/enroll-tokens", d.issueToken).Bind(apis.RequireSuperuserAuth())
	g.GET("/hub-info", d.hubInfo).Bind(apis.RequireSuperuserAuth())

	// enroll 不能要求 superuser：agent 手上只有一枚一次性 token，
	// token 本身就是凭证（spec §9.1）。
	g.POST("/enroll", d.enroll)

	// WS 端点与 /install.sh 分别由计划 5 与计划 9 在此追加。
	return nil
}

// baseURL 推断 hub 的对外地址，用于拼装安装命令。
// hub 假定部署在反向代理之后，因此优先信任 X-Forwarded-*。
func baseURL(e *core.RequestEvent) string {
	scheme := "http"
	if e.IsTLS() {
		scheme = "https"
	}
	if v := e.Request.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = strings.Split(v, ",")[0]
	}
	host := e.Request.Host
	if v := e.Request.Header.Get("X-Forwarded-Host"); v != "" {
		host = strings.Split(v, ",")[0]
	}
	return scheme + "://" + strings.TrimSpace(host)
}
```

（`routes.go` 用不到 `net/http`，import 块里别加它——handler 在 `enroll.go` 里自己引。）

- [ ] **Step 4: 写 handler**

创建 `hub/internal/routes/enroll.go`：

```go
package routes

import (
	"errors"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/enroll"
)

type issueTokenResponse struct {
	Token          string `json:"token"`
	ExpiresAt      string `json:"expiresAt"`
	InstallCommand string `json:"installCommand"`
}

func (d Deps) issueToken(e *core.RequestEvent) error {
	token, expires, err := d.Enroll.IssueToken()
	if err != nil {
		e.App.Logger().Error("签发注册 token 失败", "error", err)
		return e.InternalServerError("签发注册 token 失败", nil)
	}

	base := baseURL(e)
	cmd := "curl -fsSL " + base + "/install.sh | sh -s -- --hub " + base +
		" --token " + token + " --hub-key " + d.Identity.Fingerprint()

	return e.JSON(http.StatusOK, issueTokenResponse{
		Token:          token,
		ExpiresAt:      expires.UTC().Format(time.RFC3339),
		InstallCommand: cmd,
	})
}

type enrollRequest struct {
	Token        string `json:"token"`
	PubKey       string `json:"pubKey"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
}

type enrollResponse struct {
	HubPubKey   string `json:"hubPubKey"`
	MachineID   string `json:"machineId"`
	Fingerprint string `json:"fingerprint"`
}

func (d Deps) enroll(e *core.RequestEvent) error {
	var req enrollRequest
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}

	res, err := d.Enroll.Enroll(enroll.Request{
		Token:        req.Token,
		PubKey:       req.PubKey,
		Hostname:     req.Hostname,
		OS:           req.OS,
		Arch:         req.Arch,
		AgentVersion: req.AgentVersion,
	})
	switch {
	case err == nil:
	case errors.Is(err, enroll.ErrBadRequest), errors.Is(err, enroll.ErrBadPubKey):
		return e.BadRequestError("请求字段不完整或公钥格式错误", nil)
	case errors.Is(err, enroll.ErrTokenInvalid),
		errors.Is(err, enroll.ErrTokenExpired),
		errors.Is(err, enroll.ErrTokenUsed):
		// 三种情况对外统一成 401：这个端点不认证，不该变成 token 探测器。
		// 具体原因记在服务端日志里。
		e.App.Logger().Warn("enroll 被拒绝", "error", err, "hostname", req.Hostname)
		return e.UnauthorizedError("注册 token 无效、已过期或已被使用", nil)
	default:
		e.App.Logger().Error("enroll 失败", "error", err)
		return e.InternalServerError("注册失败", nil)
	}

	e.App.Logger().Info("机器已注册",
		"machine", res.MachineID,
		"fingerprint", res.Fingerprint,
		"re_enrolled", res.ReEnrolled,
		"replayed", res.Replayed,
	)

	return e.JSON(http.StatusOK, enrollResponse{
		HubPubKey:   d.Identity.PublicKeyBase64(),
		MachineID:   res.MachineID,
		Fingerprint: res.Fingerprint,
	})
}

type hubInfoResponse struct {
	Version     string `json:"version"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

func (d Deps) hubInfo(e *core.RequestEvent) error {
	return e.JSON(http.StatusOK, hubInfoResponse{
		Version:     d.Version,
		PublicKey:   d.Identity.PublicKeyBase64(),
		Fingerprint: d.Identity.Fingerprint(),
	})
}
```

- [ ] **Step 5: 接进 hub 装配**

修改 `hub/hub.go`：给 `Hub` 加 `events *events.Writer` 与 `enroll *enroll.Service` 字段；在 `Attach` 里构造（它们只需要 `core.App`，不需要数据目录）；在 `OnServe` 里、加载完密钥之后注册路由。

```go
// Attach 内，构造 h 之后：
	h.events = events.NewWriter(app)
	h.enroll = enroll.NewService(app, h.events, h.cfg.Clock, h.cfg.EnrollTokenTTL, rand.Reader)
```

```go
// OnServe 内，identity.Load() 之后、registerUI 之前：
		if err := routes.Register(e, routes.Deps{
			Enroll:   h.enroll,
			Identity: h.identity,
			Version:  orciny.Version,
		}); err != nil {
			return fmt.Errorf("注册路由: %w", err)
		}
```

在同一个 `OnServe` 里挂上每小时的清理任务（spec §8）：

```go
		// PocketBase 自带 cron 调度器，不必自己起 goroutine。
		// 清理失败只记日志——它不该拖垮 serve。
		e.App.Cron().MustAdd("purge_enroll_tokens", "0 * * * *", func() {
			n, err := h.enroll.PurgeExpiredTokens(24 * time.Hour)
			if err != nil {
				e.App.Logger().Warn("清理过期注册 token 失败", "error", err)
				return
			}
			if n > 0 {
				e.App.Logger().Info("已清理过期注册 token", "count", n)
			}
		})
```

新增公开方法（测试脚手架与将来的 CLI 都要用）：

```go
// IssueEnrollToken 签发一枚一次性注册 token。
// 这是 hub 包对外暴露的唯一「管理动作」，供测试脚手架绕开 HTTP 认证使用。
func (h *Hub) IssueEnrollToken() (string, time.Time, error) {
	return h.enroll.IssueToken()
}
```

（import 需要补 `crypto/rand`、`time`、`github.com/FlintyLemming/orciny`、以及 events / enroll / routes 三个内部包。）

- [ ] **Step 6: 运行测试确认通过**

Run: `go test -tags=testing ./hub/... ./internal/... -v`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add hub
git commit -m "feat: enroll-tokens / enroll / hub-info 三个端点与装配"
```

---

## Task 5: agent 侧 enroll 与 CLI

**Files:**
- Create: `agent/internal/probe/probe.go`, `agent/internal/probe/probe_test.go`, `agent/internal/enroll/enroll.go`, `agent/internal/enroll/enroll_test.go`, `agent/enroll.go`, `internal/testsupport/agent.go`, `internal/testsupport/agent_test.go`
- Modify: `agent/cli.go`（新增 `enroll` 子命令）

**Interfaces:**
- Consumes: `identity.LoadOrCreate` / `PinHubKey`、`protocol.DecodePublicKey` / `Fingerprint`、`agent.SaveConfig`
- Produces:
  ```go
  // agent/internal/probe
  func Host() (hostname, goos, goarch string)

  // agent/internal/enroll
  type Options struct {
      HubURL       string
      Token        string
      ExpectHubKey string        // --hub-key 指纹，可空
      IdentityDir  string        // <agentDir>/identity
      AgentVersion string
      HTTPClient   *http.Client  // nil → 默认 10s 超时
      Clock        clock.Clock   // nil → clock.System()
      Retries      int           // 0 → 2
      RetryDelay   time.Duration // 0 值有意义：测试里传 0 表示不等待
  }
  type Outcome struct {
      MachineID     string
      Fingerprint   string
      HubKeyFP      string
  }
  func Run(ctx context.Context, o Options) (*Outcome, error)
  var ErrHubKeyMismatch = errors.New(...)

  // agent（公开入口）
  type EnrollOptions struct {
      HubURL, Token, ExpectHubKey, Dir string
      RetryDelay                       time.Duration
  }
  type EnrollResult struct{ MachineID, Fingerprint, HubKeyFingerprint string }
  func Enroll(ctx context.Context, o EnrollOptions) (*EnrollResult, error)

  // internal/testsupport
  func NewTestAgent(t *testing.T, th *TestHub) *TestAgent
  type TestAgent struct {
      Dir         string
      IdentityDir string
      Fingerprint string
      MachineID   string
  }
  ```

- [ ] **Step 1: 写探测的失败测试**

创建 `agent/internal/probe/probe_test.go`：

```go
package probe_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/probe"
)

func TestHostReportsRuntimeValues(t *testing.T) {
	hostname, goos, goarch := probe.Host()

	require.Equal(t, runtime.GOOS, goos)
	require.Equal(t, runtime.GOARCH, goarch)

	expected, err := os.Hostname()
	require.NoError(t, err)
	require.Equal(t, expected, hostname)
	require.NotEmpty(t, hostname)
}
```

- [ ] **Step 2: 写探测实现**

创建 `agent/internal/probe/probe.go`：

```go
// Package probe 探测本机信息。
//
// M0 的边界（spec §7.4）：除了执行 `claude --version`（计划 7 加入），
// agent 不读取用户的任何文件，尤其不碰 ~/.claude。
package probe

import (
	"os"
	"runtime"
)

// Host 返回主机名与平台。主机名取不到时返回 "unknown"——
// 这不值得让 enroll 失败，用户随后可以在 UI 里改备注名。
func Host() (hostname, goos, goarch string) {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "unknown"
	}
	return name, runtime.GOOS, runtime.GOARCH
}
```

Run: `go test ./agent/internal/probe/... -v` → PASS

- [ ] **Step 3: 写 agent enroll 的失败测试**

创建 `agent/internal/enroll/enroll_test.go`：

```go
package enroll_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	agentenroll "github.com/FlintyLemming/orciny/agent/internal/enroll"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

type stubHub struct {
	srv      *httptest.Server
	hubPub   ed25519.PublicKey
	calls    atomic.Int32
	lastBody map[string]any
	failFirst bool
}

func newStubHub(t *testing.T) *stubHub {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	s := &stubHub{hubPub: pub}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/orciny/enroll", r.URL.Path)
		n := s.calls.Add(1)
		if s.failFirst && n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&s.lastBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hubPubKey":   protocol.EncodePublicKey(s.hubPub),
			"machineId":   "m123",
			"fingerprint": "fp123",
		})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func opts(t *testing.T, hub *stubHub) agentenroll.Options {
	t.Helper()
	return agentenroll.Options{
		HubURL:       hub.srv.URL,
		Token:        "tok",
		IdentityDir:  filepath.Join(t.TempDir(), identity.DirName),
		AgentVersion: "0.1.0",
		RetryDelay:   0, // 测试不等待
	}
}

func TestRunGeneratesKeyAndPinsHubKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)

	out, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, "m123", out.MachineID)

	// 密钥已生成
	id, err := identity.Load(o.IdentityDir)
	require.NoError(t, err)
	require.Equal(t, id.Fingerprint(), out.Fingerprint,
		"返回的指纹应是本机公钥派生的，不是 hub 说了算")

	// hub 公钥已钉扎
	pinned, err := identity.LoadHubKey(o.IdentityDir)
	require.NoError(t, err)
	require.Equal(t, hub.hubPub, pinned)
	require.Equal(t, protocol.Fingerprint(hub.hubPub), out.HubKeyFP)

	// 请求体带了本机公钥与平台信息
	require.Equal(t, id.PublicKeyBase64(), hub.lastBody["pubKey"])
	require.NotEmpty(t, hub.lastBody["hostname"])
	require.NotEmpty(t, hub.lastBody["os"])
	require.NotEmpty(t, hub.lastBody["arch"])
	require.Equal(t, "0.1.0", hub.lastBody["agentVersion"])
}

func TestRunHonoursExpectedHubKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.ExpectHubKey = protocol.Fingerprint(hub.hubPub)

	_, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
}

func TestRunRejectsWrongHubKeyAndDoesNotPin(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.ExpectHubKey = "这是别的 hub 的指纹"

	_, err := agentenroll.Run(context.Background(), o)
	require.ErrorIs(t, err, agentenroll.ErrHubKeyMismatch)

	_, err = identity.LoadHubKey(o.IdentityDir)
	require.ErrorIs(t, err, identity.ErrNoHubKey,
		"带外校验失败时绝不能钉扎——那等于把中间人认成了 hub")
}

func TestRunRetriesOnTransientFailure(t *testing.T) {
	hub := newStubHub(t)
	hub.failFirst = true

	out, err := agentenroll.Run(context.Background(), opts(t, hub))
	require.NoError(t, err)
	require.Equal(t, "m123", out.MachineID)
	require.EqualValues(t, 2, hub.calls.Load(), "瞬时失败应重试")
}

func TestRunDoesNotRetryOn401(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := agentenroll.Run(context.Background(), agentenroll.Options{
		HubURL:       srv.URL,
		Token:        "tok",
		IdentityDir:  filepath.Join(t.TempDir(), identity.DirName),
		AgentVersion: "0.1.0",
		RetryDelay:   0,
	})
	require.Error(t, err)
	require.EqualValues(t, 1, calls.Load(), "token 无效是终局错误，重试没有意义")
}

func TestRunReusesExistingKey(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)

	first, err := identity.LoadOrCreate(o.IdentityDir)
	require.NoError(t, err)

	out, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
	require.Equal(t, first.Fingerprint(), out.Fingerprint,
		"重新 enroll 不得换钥匙，否则会在 hub 侧变成一台新机器")
}

func TestRunTrimsTrailingSlashInHubURL(t *testing.T) {
	hub := newStubHub(t)
	o := opts(t, hub)
	o.HubURL = hub.srv.URL + "/"

	_, err := agentenroll.Run(context.Background(), o)
	require.NoError(t, err)
}
```

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./agent/internal/enroll/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 5: 写 agent enroll 实现**

创建 `agent/internal/enroll/enroll.go`：

```go
// Package enroll 实现 agent 侧的注册流程（spec §4.4）。
//
// 顺序很重要：先生成密钥 → 再请求 hub → 校验（如给了 --hub-key）→ 最后钉扎。
// 校验不过就绝不写 hub.pub，否则等于把中间人认成了 hub。
package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/probe"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// ErrHubKeyMismatch 表示 hub 返回的公钥与 --hub-key 给出的指纹不符。
var ErrHubKeyMismatch = errors.New("enroll: hub 公钥指纹与 --hub-key 不符，已中止安装")

type Options struct {
	HubURL       string
	Token        string
	ExpectHubKey string
	IdentityDir  string
	AgentVersion string

	HTTPClient *http.Client
	Clock      clock.Clock
	Retries    int
	// RetryDelay 的 0 值有意义（测试里表示不等待），因此不做「0 → 默认」处理。
	RetryDelay time.Duration
}

type Outcome struct {
	MachineID   string
	Fingerprint string
	HubKeyFP    string
}

type request struct {
	Token        string `json:"token"`
	PubKey       string `json:"pubKey"`
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	AgentVersion string `json:"agentVersion"`
}

type response struct {
	HubPubKey   string `json:"hubPubKey"`
	MachineID   string `json:"machineId"`
	Fingerprint string `json:"fingerprint"`
}

// Run 执行一次 enroll。
func Run(ctx context.Context, o Options) (*Outcome, error) {
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Clock == nil {
		o.Clock = clock.System()
	}
	if o.Retries == 0 {
		o.Retries = 2
	}

	id, err := identity.LoadOrCreate(o.IdentityDir)
	if err != nil {
		return nil, err
	}

	hostname, goos, goarch := probe.Host()
	body, err := json.Marshal(request{
		Token:        o.Token,
		PubKey:       id.PublicKeyBase64(),
		Hostname:     hostname,
		OS:           goos,
		Arch:         goarch,
		AgentVersion: o.AgentVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("enroll: 构造请求: %w", err)
	}

	url := strings.TrimRight(o.HubURL, "/") + "/api/orciny/enroll"
	resp, err := postWithRetry(ctx, o, url, body)
	if err != nil {
		return nil, err
	}

	hubPub, err := protocol.DecodePublicKey(resp.HubPubKey)
	if err != nil {
		return nil, fmt.Errorf("enroll: hub 返回的公钥无法解析: %w", err)
	}
	hubFP := protocol.Fingerprint(hubPub)

	// 带外校验：--hub-key 给了就必须对上，否则中止且不钉扎。
	if o.ExpectHubKey != "" && o.ExpectHubKey != hubFP {
		return nil, fmt.Errorf("%w（期望 %s，实际 %s）", ErrHubKeyMismatch, o.ExpectHubKey, hubFP)
	}

	if err := identity.PinHubKey(o.IdentityDir, hubPub); err != nil {
		return nil, err
	}

	return &Outcome{
		MachineID:   resp.MachineID,
		Fingerprint: id.Fingerprint(), // 以本机公钥为准，不采信 hub 的说法
		HubKeyFP:    hubFP,
	}, nil
}

// postWithRetry 把瞬时网络问题挡在用户视线之外（spec §4.4）。
// 4xx 是终局错误，不重试——token 无效不会因为再试一次就变有效。
func postWithRetry(ctx context.Context, o Options, url string, body []byte) (*response, error) {
	var lastErr error
	for attempt := 0; attempt <= o.Retries; attempt++ {
		if attempt > 0 {
			timer := o.Clock.NewTimer(o.RetryDelay)
			select {
			case <-timer.C():
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
		}

		out, retryable, err := postOnce(ctx, o, url, body)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, fmt.Errorf("enroll: 重试 %d 次后仍失败: %w", o.Retries, lastErr)
}

func postOnce(ctx context.Context, o Options, url string, body []byte) (*response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("enroll: 构造请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.HTTPClient.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("enroll: 连接 hub 失败: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		var out response
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, false, fmt.Errorf("enroll: 解析响应: %w", err)
		}
		return &out, false, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// 注意：不把响应体原样打出来，它可能回显我们发过去的内容。
		return nil, false, fmt.Errorf("enroll: hub 拒绝了注册请求（HTTP %d）；"+
			"请确认 token 未过期、未被使用", resp.StatusCode)
	default:
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, true, fmt.Errorf("enroll: hub 返回 HTTP %d", resp.StatusCode)
	}
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./agent/internal/enroll/... -v`
Expected: PASS（7 个用例）

- [ ] **Step 7: 写公开入口与 CLI 子命令**

创建 `agent/enroll.go`：

```go
package agent

import (
	"context"
	"path/filepath"
	"time"

	internalenroll "github.com/FlintyLemming/orciny/agent/internal/enroll"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/orciny"
)

// EnrollOptions 是 agent.Enroll 的入参。
type EnrollOptions struct {
	HubURL string
	Token  string
	// ExpectHubKey 对应 --hub-key，可空。给了就做带外校验。
	ExpectHubKey string
	// Dir 是 agent 目录（~/.orciny），不是 identity 子目录。
	Dir string
	// RetryDelay 为 0 表示重试之间不等待（测试用）。
	RetryDelay time.Duration
}

type EnrollResult struct {
	MachineID         string
	Fingerprint       string
	HubKeyFingerprint string
}

// Enroll 执行完整的注册流程并写好 agent.yml。
// CLI 与测试脚手架都走这个入口，保证两者行为一致。
func Enroll(ctx context.Context, o EnrollOptions) (*EnrollResult, error) {
	out, err := internalenroll.Run(ctx, internalenroll.Options{
		HubURL:       o.HubURL,
		Token:        o.Token,
		ExpectHubKey: o.ExpectHubKey,
		IdentityDir:  filepath.Join(o.Dir, identity.DirName),
		AgentVersion: orciny.Version,
		RetryDelay:   o.RetryDelay,
	})
	if err != nil {
		return nil, err
	}

	if err := SaveConfig(o.Dir, &Config{
		HubURL:    o.HubURL,
		MachineID: out.MachineID,
	}); err != nil {
		return nil, err
	}

	return &EnrollResult{
		MachineID:         out.MachineID,
		Fingerprint:       out.Fingerprint,
		HubKeyFingerprint: out.HubKeyFP,
	}, nil
}
```

> **给执行者的注意：** 根包的 import 路径是 `github.com/FlintyLemming/orciny`（包名 `orciny`），不是上面写的 `.../orciny/orciny`。写的时候改对。

修改 `agent/cli.go`，新增子命令并注册：

```go
func newEnrollCmd() *cobra.Command {
	var hubURL, token, hubKey string

	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "用一次性注册 token 接入 hub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := dirFromFlags(cmd)
			res, err := Enroll(cmd.Context(), EnrollOptions{
				HubURL:       hubURL,
				Token:        token,
				ExpectHubKey: hubKey,
				Dir:          dir,
				RetryDelay:   2 * time.Second,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "已接入 %s\n", hubURL)
			fmt.Fprintf(out, "机器指纹    %s\n", res.Fingerprint)
			fmt.Fprintf(out, "hub 公钥指纹 %s\n", res.HubKeyFingerprint)
			fmt.Fprintf(out, "配置已写入   %s\n", filepath.Join(dir, ConfigFileName))
			return nil
		},
	}
	cmd.Flags().StringVar(&hubURL, "hub", "", "hub 地址，例如 https://orciny.example.com")
	cmd.Flags().StringVar(&token, "token", "", "一次性注册 token")
	cmd.Flags().StringVar(&hubKey, "hub-key", "", "hub 公钥指纹，用于带外校验（可选）")
	_ = cmd.MarkFlagRequired("hub")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}
```

在 `newRootCmd()` 里 `root.AddCommand(newEnrollCmd())`。

- [ ] **Step 8: 写测试脚手架的 agent 部分**

创建 `internal/testsupport/agent.go`：

```go
//go:build testing

package testsupport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
)

// TestAgent 是一台已完成 enroll 的测试机器：密钥已生成、hub 公钥已钉扎、
// agent.yml 已写好。它不含连接逻辑——那由计划 7 补上。
type TestAgent struct {
	Dir         string
	IdentityDir string
	Fingerprint string
	MachineID   string
}

// NewTestAgent 走真实的公开 enroll 入口接入给定的 TestHub。
func NewTestAgent(t *testing.T, th *TestHub) *TestAgent {
	t.Helper()

	dir := t.TempDir()
	token, _, err := th.Hub.IssueEnrollToken()
	require.NoError(t, err, "签发注册 token")

	res, err := agent.Enroll(context.Background(), agent.EnrollOptions{
		HubURL:     th.HTTPURL,
		Token:      token,
		Dir:        dir,
		RetryDelay: 0,
	})
	require.NoError(t, err, "agent enroll")

	return &TestAgent{
		Dir:         dir,
		IdentityDir: filepath.Join(dir, "identity"),
		Fingerprint: res.Fingerprint,
		MachineID:   res.MachineID,
	}
}
```

- [ ] **Step 9: 写端到端集成测试**

创建 `internal/testsupport/agent_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestEnrollEndToEnd(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	require.Len(t, ta.Fingerprint, 32)

	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	require.Equal(t, ta.Fingerprint, m.GetString("fingerprint"))
	require.Equal(t, "offline", m.GetString("status"))
	require.NotEmpty(t, m.GetString("hostname"))
	require.NotEmpty(t, m.GetString("agent_version"))

	cfg, err := agent.LoadConfig(ta.Dir)
	require.NoError(t, err)
	require.Equal(t, th.HTTPURL, cfg.HubURL)
	require.Equal(t, ta.MachineID, cfg.MachineID)
}

func TestReEnrollSameMachine(t *testing.T) {
	th := testsupport.NewTestHub(t)
	ta := testsupport.NewTestAgent(t, th)

	token, _, err := th.Hub.IssueEnrollToken()
	require.NoError(t, err)
	res, err := agent.Enroll(context.Background(), agent.EnrollOptions{
		HubURL: th.HTTPURL, Token: token, Dir: ta.Dir, RetryDelay: 0,
	})
	require.NoError(t, err)
	require.Equal(t, ta.MachineID, res.MachineID, "同一目录重装应复用同一台机器")

	machines, err := th.App.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 1)
}

func TestThreeAgentsEnrollIndependently(t *testing.T) {
	th := testsupport.NewTestHub(t)

	fps := map[string]bool{}
	for i := 0; i < 3; i++ {
		ta := testsupport.NewTestAgent(t, th)
		require.False(t, fps[ta.Fingerprint], "三台机器的指纹必须互不相同")
		fps[ta.Fingerprint] = true
	}

	machines, err := th.App.FindRecordsByFilter("machines", "1=1", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, machines, 3)
}
```

- [ ] **Step 10: 运行全部测试**

Run: `make test`
Expected: 全绿

- [ ] **Step 11: 手工验证一行接入的形状**

```bash
go run ./cmd/orciny serve --http=127.0.0.1:8090 --dir=/tmp/orciny-enroll &
# 首次启动会提示创建 superuser：
go run ./cmd/orciny superuser create admin@example.com "changeme12345" --dir=/tmp/orciny-enroll
# 用 superuser 登录拿 token，再签发注册 token：
TOKEN=$(curl -s -X POST localhost:8090/api/collections/_superusers/auth-with-password \
  -H 'content-type: application/json' \
  -d '{"identity":"admin@example.com","password":"changeme12345"}' | jq -r .token)
curl -s -X POST localhost:8090/api/orciny/enroll-tokens -H "Authorization: $TOKEN" | jq
```
Expected: 返回含 `token` / `expiresAt` / `installCommand` 的 JSON，`installCommand` 里带 `--hub-key`。
清理：`kill %1 && rm -rf /tmp/orciny-enroll`

- [ ] **Step 12: 提交**

```bash
git add agent internal/testsupport
git commit -m "feat: agent 侧 enroll、TOFU 钉扎与 enroll 子命令"
```

---

## 完成检查

spec §12.2「enroll」七条用例逐条对照：

- [ ] 正常路径：建记录、核销 token、返回 hub 公钥 —— `TestEnrollHappyPath`
- [ ] 过期 token 被拒 —— `TestEnrollRejectsExpiredToken`
- [ ] 已核销 token 被拒 —— `TestEnrollRejectsUsedTokenWithDifferentKey`
- [ ] 同一 token 并发两次，恰好一个成功 —— `TestConcurrentEnrollExactlyOneWins`
- [ ] 指纹已存在时更新而非重复创建，且仍要求有效 token —— `TestReEnrollUpdatesExistingMachine` + `TestReEnrollWithoutTokenIsRejected`
- [ ] 幂等重放：已核销 token + 相同 pubKey → 成功 —— `TestEnrollIdempotentReplay`
- [ ] 幂等重放的边界：已核销 token + 不同 pubKey → 拒绝 —— `TestEnrollRejectsUsedTokenWithDifferentKey`

另外：

- [ ] token 明文不入库、日志只记前 8 位
- [ ] 过期 token 每小时清理一次，保留 24 小时 —— `TestPurgeExpiredTokens*` + `OnServe` 里的 cron
- [ ] `--hub-key` 不符时不钉扎
- [ ] `go list -deps ./agent/... | grep orciny/hub` 无输出
