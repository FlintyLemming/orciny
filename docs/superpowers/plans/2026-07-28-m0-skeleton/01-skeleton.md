# M0 计划 1 · 骨架 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 立起可编译、可测试、可运行的空骨架——模块、根包、时钟抽象、三个 collection、hub 装配、agent CLI、测试脚手架。

**Architecture:** 单 Go module；`hub/` 与 `agent/` 各自把实现藏进 `internal/`；`internal/clock` 是中立的时间抽象；hub 用 `Attach(core.App, Config)` 把子系统绑到任意 PocketBase app 上，生产走 `New()`（包 `pocketbase.New()`），测试走 `tests.TestApp` + `httptest`。

**Tech Stack:** Go 1.26 · PocketBase v0.39.9 · cobra v1.10.2 · blang/semver v4.0.0 · testify v1.11.1 · yaml.v3

**上位文档：** [统括计划](00-overview.md) · [spec §2 §5 §8 §12](../../specs/2026-07-28-m0-skeleton-design.md)

## Global Constraints

- module 路径 `github.com/FlintyLemming/orciny`，`go 1.26.0`；依赖版本锁死（见统括计划）。
- `agent/**` 与 `hub/**` 之间零直接依赖；共享只走 `protocol/`；`hub/internal/*` 的单测写在包内。
- 测试禁用 `time.Sleep`；超时走注入的 `clock.Clock`；等待可观测效果用 `require.Eventually`。
- 二进制名 `orciny` / `orciny-agent`；agent 目录 `~/.orciny/`。
- 测试命令 `go test -tags=testing ./...`；断言用 `testify/require`。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `go.mod` / `go.sum` | 单 module，依赖锁定 |
| `orciny.go` | 根包：`Version` / `AppName` / `MinAgentVersion`，只放常量 |
| `Makefile` | `build` / `build-hub` / `build-agent` / `build-web` / `test` / `lint` / `clean` |
| `.gitignore` | 忽略构建产物，但保留 `site/dist/index.html` 占位 |
| `internal/clock/clock.go` | `Clock` / `Timer` / `Ticker` 接口 + 系统实现 |
| `internal/clock/fake.go` | `Fake`：可推进的假时钟 |
| `hub/internal/migrations/001_initial.go` | 三个 collection 的声明式迁移 |
| `hub/config.go` | `hub.Config` 与默认值 |
| `hub/hub.go` | `New` / `Attach` / `Start`，`OnServe` 装配点 |
| `hub/internal/site/embed.go` | `//go:embed all:dist` + `DistFS()` |
| `hub/internal/site/dist/index.html` | 前端未接入前的占位页（计划 8 覆盖） |
| `cmd/orciny/main.go` | hub 入口 |
| `agent/config.go` | `agent.Config` 与 `~/.orciny/agent.yml` 读写 |
| `internal/atomicfile/atomicfile.go` | 临时文件 + rename 原子替换（hub 写密钥、agent 写配置与密钥都用它，故与 clock 一样放中立的顶层 `internal/`） |
| `agent/cli.go` | cobra root + `version` |
| `agent/agent.go` | `Execute()` 唯一对外入口 |
| `cmd/orciny-agent/main.go` | agent 入口 |
| `internal/testsupport/doc.go` | 无 build tag 的空包声明（保证 `go build ./...` 不报错） |
| `internal/testsupport/hub.go` | `//go:build testing`，`NewTestHub` |

---

## Task 1: 模块与根包

**Files:**
- Create: `go.mod`, `orciny.go`, `orciny_test.go`, `Makefile`, `.gitignore`

**Interfaces:**
- Consumes: 无
- Produces: `orciny.Version string`（var）、`orciny.AppName string`（const）、`orciny.MinAgentVersion semver.Version`

- [ ] **Step 1: 初始化 module 并拉依赖**

```bash
go mod init github.com/FlintyLemming/orciny
go get github.com/blang/semver/v4@v4.0.0
go get github.com/stretchr/testify@v1.11.1
```

确认 `go.mod` 的 go 指令为 `go 1.26.0`，不是则手工改成 `go 1.26.0`。

- [ ] **Step 2: 写失败的测试**

创建 `orciny_test.go`：

```go
package orciny_test

import (
	"testing"

	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
)

func TestVersionIsValidSemver(t *testing.T) {
	v, err := semver.Parse(orciny.Version)
	require.NoError(t, err, "Version 必须是合法 semver")
	require.Equal(t, "0.1.0", v.String())
}

func TestAppName(t *testing.T) {
	require.Equal(t, "orciny", orciny.AppName)
}

func TestMinAgentVersionNotAboveCurrent(t *testing.T) {
	cur := semver.MustParse(orciny.Version)
	require.False(t, orciny.MinAgentVersion.GT(cur),
		"门槛版本不能高于当前版本，否则自带的 agent 会被自己拒绝")
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./...`
Expected: FAIL —— `undefined: orciny.Version`

- [ ] **Step 4: 写实现**

创建 `orciny.go`：

```go
// Package orciny 只承载 hub 与 agent 共同引用的版本常量。
// 这里不允许放任何逻辑（spec §2.2 第 3 条）。
package orciny

import "github.com/blang/semver/v4"

// Version 是 hub 与 agent 的共同版本号。
// 声明为 var 而非 const，供 goreleaser 用 -ldflags -X 注入构建版本。
var Version = "0.1.0"

// AppName 用于二进制名、目录名与镜像名，全仓库唯一来源。
const AppName = "orciny"

// MinAgentVersion 是 hub 接受的最低 agent 版本。
// 低于此版本的 agent 在握手第一步就被 CodeVersionTooOld 拒绝（spec §3.4）。
var MinAgentVersion = semver.MustParse("0.1.0")
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./...`
Expected: PASS（3 个用例）

- [ ] **Step 6: 写 Makefile**

创建 `Makefile`（**注意配方行必须是 Tab 缩进**）：

```make
MODULE  := github.com/FlintyLemming/orciny
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(MODULE).Version=$(VERSION)

.PHONY: all build build-hub build-agent build-web test lint clean

all: build

## build-web: 编译前端（计划 8 接入 site/ 之后才可用）
build-web:
	cd hub/internal/site && npm ci && npm run build

build-hub:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny ./cmd/orciny

build-agent:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/orciny-agent ./cmd/orciny-agent

build: build-hub build-agent

test:
	go test -tags=testing ./...

lint:
	go vet ./...
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt 未格式化: $$out"; exit 1; fi

clean:
	rm -rf dist
```

- [ ] **Step 7: 写 .gitignore**

创建 `.gitignore`：

```gitignore
dist/
pb_data/
node_modules/
*.pem
.DS_Store

# 前端构建产物不入库，但保留占位 index.html，否则 //go:embed all:dist 找不到目录
hub/internal/site/dist/*
!hub/internal/site/dist/index.html
```

- [ ] **Step 8: 提交**

```bash
git add go.mod go.sum orciny.go orciny_test.go Makefile .gitignore
git commit -m "feat: 初始化 Go module 与根包版本常量"
```

---

## Task 2: 时间抽象 `internal/clock`

整个 M0 的超时、宽限、心跳、退避都走这个接口。它必须在任何定时逻辑之前落地，否则后面会长出一堆 `time.After`，测试就只能真睡。

**Files:**
- Create: `internal/clock/clock.go`, `internal/clock/fake.go`, `internal/clock/fake_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  ```go
  type Clock interface {
      Now() time.Time
      NewTimer(d time.Duration) Timer
      NewTicker(d time.Duration) Ticker
  }
  type Timer interface  { C() <-chan time.Time; Stop() bool; Reset(d time.Duration) bool }
  type Ticker interface { C() <-chan time.Time; Stop() }
  func System() Clock
  func NewFake(now time.Time) *Fake
  func (f *Fake) Now() time.Time
  func (f *Fake) Advance(d time.Duration)
  func (f *Fake) TimerCount() int
  ```

- [ ] **Step 1: 写失败的测试**

创建 `internal/clock/fake_test.go`：

```go
package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/clock"
)

var base = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func TestFakeNowAdvances(t *testing.T) {
	f := clock.NewFake(base)
	require.Equal(t, base, f.Now())
	f.Advance(90 * time.Second)
	require.Equal(t, base.Add(90*time.Second), f.Now())
}

func TestFakeTimerFiresOnlyAfterDeadline(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)

	f.Advance(4 * time.Second)
	select {
	case <-tm.C():
		t.Fatal("未到期就触发了")
	default:
	}

	f.Advance(time.Second)
	select {
	case got := <-tm.C():
		require.Equal(t, base.Add(5*time.Second), got, "触发时间应为逻辑到期时刻")
	default:
		t.Fatal("到期后未触发")
	}
}

func TestFakeTimerStopPreventsFire(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)
	require.True(t, tm.Stop())
	require.False(t, tm.Stop(), "重复 Stop 返回 false")

	f.Advance(time.Minute)
	select {
	case <-tm.C():
		t.Fatal("Stop 之后仍然触发")
	default:
	}
	require.Equal(t, 0, f.TimerCount())
}

func TestFakeTimerResetRearms(t *testing.T) {
	f := clock.NewFake(base)
	tm := f.NewTimer(5 * time.Second)
	f.Advance(4 * time.Second)
	require.True(t, tm.Reset(5*time.Second), "未触发的定时器 Reset 返回 true")

	f.Advance(4 * time.Second) // 距新的到期点还差 1s
	select {
	case <-tm.C():
		t.Fatal("Reset 后按旧到期点触发了")
	default:
	}

	f.Advance(time.Second)
	<-tm.C()
}

func TestFakeTickerRepeats(t *testing.T) {
	f := clock.NewFake(base)
	tk := f.NewTicker(30 * time.Second)
	defer tk.Stop()

	f.Advance(90 * time.Second)
	// 通道容量为 1：一次 Advance 跨过多个周期只保留最后一次滴答，
	// 语义与 time.Ticker 一致（消费者慢则丢滴答）。
	select {
	case <-tk.C():
	default:
		t.Fatal("ticker 未触发")
	}
	require.Equal(t, 1, f.TimerCount(), "ticker 停止前一直在册")
}

func TestSystemClockNowIsMonotonic(t *testing.T) {
	c := clock.System()
	a := c.Now()
	b := c.Now()
	require.False(t, b.Before(a))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/clock/... -v`
Expected: FAIL —— `no required module provides package .../internal/clock`

- [ ] **Step 3: 写接口与系统实现**

创建 `internal/clock/clock.go`：

```go
// Package clock 把「现在几点」和「过 N 秒叫我」抽象成接口，
// 使超时、宽限、心跳、退避这些逻辑可以在测试中被瞬间推进（spec §12.3）。
//
// 本包中立于 hub 与 agent：它不属于任何一侧，两侧都可以 import，
// 因此不构成 spec §2.2 所禁止的 hub↔agent 直接依赖。
package clock

import "time"

// Clock 是取时间与创建定时器的唯一入口。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
}

// Timer 语义对齐 time.Timer。
type Timer interface {
	C() <-chan time.Time
	// Stop 返回 true 表示定时器在本次调用前尚未触发。
	Stop() bool
	// Reset 重新计时，返回值语义同 Stop。
	Reset(d time.Duration) bool
}

// Ticker 语义对齐 time.Ticker。
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type systemClock struct{}

// System 返回走真实时间的时钟。生产代码用它，测试代码一律不用。
func System() Clock { return systemClock{} }

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(d time.Duration) Timer {
	return &systemTimer{t: time.NewTimer(d)}
}

func (systemClock) NewTicker(d time.Duration) Ticker {
	return &systemTicker{t: time.NewTicker(d)}
}

type systemTimer struct{ t *time.Timer }

func (s *systemTimer) C() <-chan time.Time         { return s.t.C }
func (s *systemTimer) Stop() bool                  { return s.t.Stop() }
func (s *systemTimer) Reset(d time.Duration) bool  { return s.t.Reset(d) }

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }
```

- [ ] **Step 4: 写假时钟**

创建 `internal/clock/fake.go`：

```go
package clock

import (
	"sort"
	"sync"
	"time"
)

// Fake 是可手工推进的时钟。
//
// 用法：被测代码持有 Fake 并注册定时器，测试调用 Advance 推进逻辑时间，
// 然后用 require.Eventually 等待「推进所引发的可观测效果」——
// 不要用 time.Sleep 等待，那正是本包要消灭的东西。
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func NewFake(now time.Time) *Fake { return &Fake{now: now} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// TimerCount 返回在册（未触发且未停止）的定时器数量。
// 测试用它确认「被测代码已经注册好定时器」再 Advance，避免竞态。
func (f *Fake) TimerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// Advance 把逻辑时间推进 d，并按到期顺序触发所有到期的定时器。
// 触发时 f.now 已被推进到该定时器的到期时刻，因此定时器回调里读到的
// Now() 与到期时间一致。
func (f *Fake) Advance(d time.Duration) {
	target := f.Now().Add(d)

	for {
		f.mu.Lock()
		sort.SliceStable(f.timers, func(i, j int) bool {
			return f.timers[i].deadline.Before(f.timers[j].deadline)
		})
		if len(f.timers) == 0 || f.timers[0].deadline.After(target) {
			f.now = target
			f.mu.Unlock()
			return
		}
		t := f.timers[0]
		f.now = t.deadline
		fireAt := t.deadline
		if t.period > 0 {
			t.deadline = t.deadline.Add(t.period)
		} else {
			f.timers = f.timers[1:]
			t.armed = false
		}
		f.mu.Unlock()

		// 通道容量为 1，非阻塞发送：消费者未取走上一次滴答时直接丢弃，
		// 与 time.Ticker 行为一致，也保证 Advance 永不死锁。
		select {
		case t.ch <- fireAt:
		default:
		}
	}
}

func (f *Fake) NewTimer(d time.Duration) Timer  { return f.add(d, 0) }
func (f *Fake) NewTicker(d time.Duration) Ticker { return f.add(d, d) }

func (f *Fake) add(d, period time.Duration) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{
		f:        f,
		deadline: f.now.Add(d),
		period:   period,
		ch:       make(chan time.Time, 1),
		armed:    true,
	}
	f.timers = append(f.timers, t)
	return t
}

type fakeTimer struct {
	f        *Fake
	deadline time.Time
	period   time.Duration
	ch       chan time.Time
	armed    bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	return t.removeLocked()
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	was := t.removeLocked()
	t.deadline = t.f.now.Add(d)
	t.armed = true
	t.f.timers = append(t.f.timers, t)
	return was
}

func (t *fakeTimer) removeLocked() bool {
	if !t.armed {
		return false
	}
	for i, x := range t.f.timers {
		if x == t {
			t.f.timers = append(t.f.timers[:i], t.f.timers[i+1:]...)
			break
		}
	}
	t.armed = false
	return true
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/clock/... -race -v`
Expected: PASS（6 个用例）

- [ ] **Step 6: 提交**

```bash
git add internal/clock
git commit -m "feat: 可注入的时钟抽象与假时钟"
```

---

## Task 3: 初始迁移（三个 collection）

**Files:**
- Create: `hub/internal/migrations/001_initial.go`, `hub/internal/migrations/migrations_test.go`

**Interfaces:**
- Consumes: 无
- Produces: 通过 `init()` 向 `core.AppMigrations` 注册的迁移；被 import（含空 import）即生效。collection 与字段名见统括计划「数据模型」。

- [ ] **Step 1: 拉 PocketBase 依赖**

```bash
go get github.com/pocketbase/pocketbase@v0.39.9
```

- [ ] **Step 2: 写失败的测试**

创建 `hub/internal/migrations/migrations_test.go`：

```go
package migrations_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

// newApp 起一个跑完全部迁移的临时 PocketBase。
// tests.NewTestApp 会克隆给定目录并执行 RunAllMigrations，
// 我们的迁移在 init() 里已注册进 core.AppMigrations，因此自动被应用。
func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestCollectionsExist(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{"machines", "enroll_tokens", "events"} {
		c, err := app.FindCollectionByNameOrId(name)
		require.NoError(t, err, "collection %s 必须存在", name)
		require.Nil(t, c.ListRule, "%s 的 list rule 必须是 nil（仅 superuser）", name)
		require.Nil(t, c.ViewRule, "%s 的 view rule 必须是 nil", name)
		require.Nil(t, c.CreateRule, "%s 的 create rule 必须是 nil", name)
		require.Nil(t, c.UpdateRule, "%s 的 update rule 必须是 nil", name)
		require.Nil(t, c.DeleteRule, "%s 的 delete rule 必须是 nil", name)
	}
}

func TestMachinesFields(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	for _, f := range []string{
		"name", "fingerprint", "pub_key", "hostname", "os", "arch",
		"agent_version", "tool_versions", "status", "last_seen", "created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "machines.%s 缺失", f)
	}

	status, ok := c.Fields.GetByName("status").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"online", "offline", "paused"}, status.Values)
}

func TestMachinesFingerprintIsUnique(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("fingerprint", "same-fp")
		r.Set("pub_key", "k")
		r.Set("status", "offline")
		return r
	}
	require.NoError(t, app.Save(mk()))
	require.Error(t, app.Save(mk()), "同一 fingerprint 必须被唯一索引挡住")
}

func TestEnrollTokensAndEventsFields(t *testing.T) {
	app := newApp(t)

	tk, err := app.FindCollectionByNameOrId("enroll_tokens")
	require.NoError(t, err)
	for _, f := range []string{"token_hash", "expires_at", "used_at", "machine"} {
		require.NotNil(t, tk.Fields.GetByName(f), "enroll_tokens.%s 缺失", f)
	}

	ev, err := app.FindCollectionByNameOrId("events")
	require.NoError(t, err)
	for _, f := range []string{"kind", "machine", "detail", "created"} {
		require.NotNil(t, ev.Fields.GetByName(f), "events.%s 缺失", f)
	}

	rel, ok := ev.Fields.GetByName("machine").(*core.RelationField)
	require.True(t, ok)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	require.Equal(t, machines.Id, rel.CollectionId)
	require.False(t, rel.Required, "机器被删后事件仍要保留，因此 machine 可空")
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./hub/internal/migrations/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 4: 写迁移**

创建 `hub/internal/migrations/001_initial.go`：

```go
// Package migrations 以声明式方式定义 hub 的 collection。
// 只增不改：后续里程碑新增文件，不动已有文件（spec §8）。
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up001, down001, "001_initial.go")
}

func up001(app core.App) error {
	// --- machines ---------------------------------------------------
	machines := core.NewBaseCollection("machines")
	machines.Fields.Add(
		&core.TextField{Name: "name", Max: 200},
		&core.TextField{Name: "fingerprint", Required: true, Max: 64},
		&core.TextField{Name: "pub_key", Required: true, Max: 128},
		&core.TextField{Name: "hostname", Max: 255},
		&core.TextField{Name: "os", Max: 32},
		&core.TextField{Name: "arch", Max: 32},
		&core.TextField{Name: "agent_version", Max: 32},
		&core.JSONField{Name: "tool_versions", MaxSize: 4096},
		// paused 在 M0 没有切换入口，但字段先就位（spec §6.1 / §16）
		&core.SelectField{
			Name:      "status",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{"online", "offline", "paused"},
		},
		&core.DateField{Name: "last_seen"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	machines.AddIndex("idx_machines_fingerprint", true, "fingerprint", "")
	// API rule 全部保持 nil —— 仅 superuser 可访问（spec §5.3）
	if err := app.Save(machines); err != nil {
		return err
	}

	// --- enroll_tokens ----------------------------------------------
	tokens := core.NewBaseCollection("enroll_tokens")
	tokens.Fields.Add(
		&core.TextField{Name: "token_hash", Required: true, Max: 64},
		&core.DateField{Name: "expires_at", Required: true},
		&core.DateField{Name: "used_at"},
		&core.RelationField{
			Name:          "machine",
			CollectionId:  machines.Id,
			MaxSelect:     1,
			CascadeDelete: false,
		},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	tokens.AddIndex("idx_enroll_tokens_hash", true, "token_hash", "")
	if err := app.Save(tokens); err != nil {
		return err
	}

	// --- events ------------------------------------------------------
	events := core.NewBaseCollection("events")
	events.Fields.Add(
		&core.TextField{Name: "kind", Required: true, Max: 64},
		&core.RelationField{
			Name:         "machine",
			CollectionId: machines.Id,
			MaxSelect:    1,
			// 机器被删除后事件必须留下（machine.removed 正是此时写的），
			// 因此不级联删除，字段留空即可。
			CascadeDelete: false,
		},
		&core.JSONField{Name: "detail", MaxSize: 8192},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	events.AddIndex("idx_events_created", false, "created", "")
	events.AddIndex("idx_events_machine", false, "machine", "")
	return app.Save(events)
}

func down001(app core.App) error {
	for _, name := range []string{"events", "enroll_tokens", "machines"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue // 已不存在，视为已回滚
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./hub/internal/migrations/... -v`
Expected: PASS（4 个用例）

- [ ] **Step 6: 提交**

```bash
git add hub/internal/migrations
git commit -m "feat: machines/enroll_tokens/events 三个 collection 的初始迁移"
```

---

## Task 4: hub 装配骨架与入口

**Files:**
- Create: `hub/config.go`, `hub/hub.go`, `hub/hub_test.go`, `hub/internal/site/embed.go`, `hub/internal/site/dist/index.html`, `cmd/orciny/main.go`

**Interfaces:**
- Consumes: `clock.Clock`（Task 2）、`hub/internal/migrations`（Task 3，空 import）
- Produces:
  ```go
  type Config struct {
      DataDir           string
      Clock             clock.Clock
      OfflineGrace      time.Duration
      HeartbeatInterval time.Duration
      ReadTimeout       time.Duration
      HandshakeTimeout  time.Duration
      EnrollTokenTTL    time.Duration
      MinAgentVersion   semver.Version
  }
  func (c Config) WithDefaults() Config
  func New(cfg Config) (*Hub, error)
  func Attach(app core.App, cfg Config) (*Hub, error)
  func (h *Hub) Start() error
  func site.DistFS() fs.FS
  ```

- [ ] **Step 1: 写失败的测试**

创建 `hub/hub_test.go`：

```go
package hub_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/clock"
)

func TestConfigWithDefaults(t *testing.T) {
	c := hub.Config{}.WithDefaults()
	require.NotNil(t, c.Clock)
	require.Equal(t, 5*time.Second, c.OfflineGrace)
	require.Equal(t, 30*time.Second, c.HeartbeatInterval)
	require.Equal(t, 70*time.Second, c.ReadTimeout)
	require.Equal(t, 10*time.Second, c.HandshakeTimeout)
	require.Equal(t, 15*time.Minute, c.EnrollTokenTTL)
	require.Equal(t, orciny.MinAgentVersion, c.MinAgentVersion)
}

func TestConfigWithDefaultsKeepsExplicitValues(t *testing.T) {
	f := clock.NewFake(time.Now())
	c := hub.Config{Clock: f, OfflineGrace: time.Second}.WithDefaults()
	require.Same(t, f, c.Clock)
	require.Equal(t, time.Second, c.OfflineGrace)
}

func TestAttachSucceedsOnTestApp(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)
	require.NotNil(t, h)
}

func TestStartOnAttachedHubIsRejected(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	h, err := hub.Attach(app, hub.Config{})
	require.NoError(t, err)

	err = h.Start()
	require.Error(t, err, "Attach 出来的 hub 没有 pocketbase 实例，Start 必须明确报错而不是 panic")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./hub/... -v`
Expected: FAIL —— `hub` 包不存在

- [ ] **Step 3: 写 Config**

创建 `hub/config.go`：

```go
package hub

import (
	"time"

	"github.com/blang/semver/v4"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// Config 收拢 hub 的全部可调参数。
//
// 所有时长都可注入，是为了让测试把 70 秒的 deadline 换成 200 毫秒——
// gws 的读写 deadline 走的是真实网络时间，假时钟对它无效（spec §12.3 的边界）。
type Config struct {
	// DataDir 仅对 New 有意义；Attach 的实例用宿主 app 的数据目录。
	DataDir string

	Clock clock.Clock

	OfflineGrace      time.Duration // 连接断开到判定 offline 的宽限，spec §6.3
	HeartbeatInterval time.Duration // hub 主动 ping 间隔，spec §6.2
	ReadTimeout       time.Duration // WS read deadline，spec §6.2
	HandshakeTimeout  time.Duration // 从 WS 升级到收到合法 Auth，spec §4.2
	EnrollTokenTTL    time.Duration // 注册 token 有效期，spec §4.4

	MinAgentVersion semver.Version
}

// WithDefaults 返回补齐默认值后的副本。零值即默认，调用方不必知道默认是多少。
func (c Config) WithDefaults() Config {
	if c.Clock == nil {
		c.Clock = clock.System()
	}
	if c.OfflineGrace == 0 {
		c.OfflineGrace = 5 * time.Second
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 30 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 70 * time.Second
	}
	if c.HandshakeTimeout == 0 {
		c.HandshakeTimeout = 10 * time.Second
	}
	if c.EnrollTokenTTL == 0 {
		c.EnrollTokenTTL = 15 * time.Minute
	}
	if (c.MinAgentVersion == semver.Version{}) {
		c.MinAgentVersion = orciny.MinAgentVersion
	}
	return c
}
```

- [ ] **Step 4: 写 hub.go**

创建 `hub/hub.go`：

```go
// Package hub 是 hub 侧唯一的对外入口。
// 实现细节全部藏在 hub/internal/*，由 Go 的 internal 规则保证 agent 侧碰不到（spec §2.2）。
package hub

import (
	"errors"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"

	// 空 import 触发 init()，把初始迁移注册进 core.AppMigrations。
	// 这也让 internal/testsupport（只能看到公开包）间接获得迁移。
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

// Hub 嵌入 core.App，子系统作字段，业务全部挂在 OnServe 上（spec §5.1）。
type Hub struct {
	core.App

	cfg Config

	// pb 仅在 New 创建时非 nil。Attach 出来的实例由调用方驱动 serve。
	pb *pocketbase.PocketBase
}

// New 创建生产用的 hub：内部是一个完整的 PocketBase 应用。
func New(cfg Config) (*Hub, error) {
	pb := pocketbase.New()
	h, err := Attach(pb, cfg)
	if err != nil {
		return nil, err
	}
	h.pb = pb
	return h, nil
}

// Attach 把 hub 的子系统绑定到任意 core.App 上。
// 生产由 New 调用；测试脚手架用它绑到 tests.TestApp（spec §12.1）。
//
// 约定：这里只做「注册」，不做任何需要数据库或数据目录的事——
// 那些一律推迟到 OnServe，因为此刻 app 还没 Bootstrap。
func Attach(app core.App, cfg Config) (*Hub, error) {
	h := &Hub{App: app, cfg: cfg.WithDefaults()}

	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// 后续计划在此依次插入：hub 密钥加载、幽灵清理、自定义路由注册、
		// 连接管理器启动。顺序即依赖顺序，不要打乱。
		if err := h.registerUI(e); err != nil {
			return err
		}
		return e.Next()
	})

	return h, nil
}

// registerUI 挂内嵌前端。catch-all 模式在 net/http 的 ServeMux 里优先级最低，
// 因此不会盖住 /api/* 与 PocketBase 自带的 /_/*。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	e.Router.GET("/{path...}", apis.Static(site.DistFS(), true))
	return nil
}

// Start 运行生产 hub（cobra 根命令，含 serve 子命令）。
func (h *Hub) Start() error {
	if h.pb == nil {
		return errors.New("hub: Start 只能用于 New 创建的实例；Attach 的实例由调用方驱动 serve")
	}
	return h.pb.Start()
}
```

- [ ] **Step 5: 写内嵌前端占位**

创建 `hub/internal/site/dist/index.html`：

```html
<!doctype html>
<meta charset="utf-8">
<title>Orciny</title>
<p>Orciny hub 正在运行。Web UI 将在计划 8 接入。</p>
```

创建 `hub/internal/site/embed.go`：

```go
// Package site 承载编译后的 Web UI。
// 生产用 //go:embed 内嵌 dist/；dist/index.html 是仓库里的占位文件，
// 前端构建会覆盖它（spec §5.4）。
package site

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distDir embed.FS

// DistFS 返回以 dist/ 为根的只读文件系统。
func DistFS() fs.FS {
	sub, err := fs.Sub(distDir, "dist")
	if err != nil {
		// 只可能在 dist/ 目录缺失时发生，属于构建期错误。
		panic("site: 内嵌 dist 目录缺失: " + err.Error())
	}
	return sub
}
```

- [ ] **Step 6: 写 hub 入口**

创建 `cmd/orciny/main.go`：

```go
// Command orciny 是 Orciny hub 的可执行入口。
package main

import (
	"log"

	"github.com/FlintyLemming/orciny/hub"
)

func main() {
	h, err := hub.New(hub.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := h.Start(); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./hub/... -v && go build ./cmd/orciny`
Expected: PASS（4 个用例）+ 编译通过

- [ ] **Step 8: 手工确认能起服务**

```bash
go run ./cmd/orciny serve --http=127.0.0.1:8090 --dir=/tmp/orciny-smoke
```
Expected: 输出监听地址；另开终端 `curl -s localhost:8090/api/health` 返回 JSON、`curl -s localhost:8090/` 返回占位 HTML。确认后 Ctrl-C，`rm -rf /tmp/orciny-smoke`。

- [ ] **Step 9: 提交**

```bash
git add hub cmd/orciny
git commit -m "feat: hub 装配骨架、内嵌前端占位与可执行入口"
```

---

## Task 5: agent 骨架（配置、原子写、CLI）

**Files:**
- Create: `agent/config.go`, `agent/config_test.go`, `agent/agent.go`, `agent/cli.go`, `agent/cli_test.go`, `internal/atomicfile/atomicfile.go`, `internal/atomicfile/atomicfile_test.go`, `cmd/orciny-agent/main.go`

**Interfaces:**
- Consumes: `orciny.Version`
- Produces:
  ```go
  func atomicfile.Write(path string, data []byte, perm os.FileMode) error
  type agent.Config struct {
      HubURL    string `yaml:"hub_url"`
      MachineID string `yaml:"machine_id"`
      LogFile   bool   `yaml:"log_file"`
  }
  func agent.DefaultDir() string
  func agent.LoadConfig(dir string) (*Config, error)
  func agent.SaveConfig(dir string, c *Config) error
  func agent.Execute() error
  func agent.newRootCmd() *cobra.Command   // 包内，供测试驱动
  ```

- [ ] **Step 1: 拉依赖**

```bash
go get github.com/spf13/cobra@v1.10.2
go get gopkg.in/yaml.v3@v3.0.1
```

- [ ] **Step 2: 写失败的测试（原子写）**

创建 `internal/atomicfile/atomicfile_test.go`：

```go
package atomicfile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

func TestWriteCreatesFileWithPerm(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.key")

	require.NoError(t, atomicfile.Write(p, []byte("secret"), 0o600))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "secret", string(b))

	if runtime.GOOS != "windows" {
		st, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
}

func TestWriteReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.yml")
	require.NoError(t, atomicfile.Write(p, []byte("old-and-long"), 0o644))
	require.NoError(t, atomicfile.Write(p, []byte("new"), 0o644))

	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "new", string(b), "必须整体替换，不能留下旧内容的尾巴")
}

func TestWriteLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, atomicfile.Write(filepath.Join(dir, "a.yml"), []byte("x"), 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "临时文件必须已被 rename 掉")
}

func TestWriteCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "identity", "agent.key")
	require.NoError(t, atomicfile.Write(p, []byte("k"), 0o600))
	require.FileExists(t, p)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./agent/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 4: 写原子写实现**

创建 `internal/atomicfile/atomicfile.go`：

```go
// Package atomicfile 提供「临时文件 + rename」的原子落盘（spec §7.1）。
// agent 的所有写盘都走这里，避免写到一半崩溃留下半个文件。
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write 原子地把 data 写到 path，并设置权限位。父目录不存在时自动创建（0700）。
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建目录 %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这里是 no-op

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("设置权限: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("写入: %w", err)
	}
	// 先 fsync 再 rename：崩溃时要么是旧内容，要么是完整新内容。
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("重命名到 %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/atomicfile/... -v`
Expected: PASS（4 个用例）

- [ ] **Step 6: 写失败的测试（配置与 CLI）**

创建 `agent/config_test.go`：

```go
package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveLoadConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Config{HubURL: "https://hub.example.com", MachineID: "abc123", LogFile: true}
	require.NoError(t, SaveConfig(dir, in))

	out, err := LoadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestConfigFileIsYAMLWithSnakeCaseKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://h", MachineID: "m"}))

	b, err := os.ReadFile(filepath.Join(dir, "agent.yml"))
	require.NoError(t, err)
	require.Contains(t, string(b), "hub_url: https://h")
	require.Contains(t, string(b), "machine_id: m")
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDefaultDirHonoursOrcinyHome(t *testing.T) {
	t.Setenv("ORCINY_HOME", "/custom/place")
	require.Equal(t, "/custom/place", DefaultDir())
}

func TestDefaultDirFallsBackToHome(t *testing.T) {
	t.Setenv("ORCINY_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".orciny"), DefaultDir())
}
```

创建 `agent/cli_test.go`：

```go
package agent

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	require.NoError(t, root.Execute())
	require.Equal(t, orciny.Version+"\n", out.String())
}

func TestRootCommandName(t *testing.T) {
	require.Equal(t, "orciny-agent", newRootCmd().Use)
}
```

- [ ] **Step 7: 运行测试确认失败**

Run: `go test ./agent/ -v`
Expected: FAIL —— `undefined: Config` / `undefined: newRootCmd`

- [ ] **Step 8: 写配置**

创建 `agent/config.go`：

```go
package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// ConfigFileName 是 agent 配置在 agent 目录下的文件名。
const ConfigFileName = "agent.yml"

// Config 是 ~/.orciny/agent.yml 的内容（spec §7.1）。
// 安装时生成，一般不手改。
type Config struct {
	HubURL    string `yaml:"hub_url"`
	MachineID string `yaml:"machine_id"`
	// LogFile 为 true 时额外把日志写到 ~/.orciny/logs/（spec §7.5）。
	LogFile bool `yaml:"log_file"`
}

// DefaultDir 返回 agent 的工作目录：$ORCINY_HOME 或 ~/.orciny。
// 环境变量优先，测试与多实例部署靠它隔离。
func DefaultDir() string {
	if d := os.Getenv("ORCINY_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// 拿不到 home 时退回相对目录，让错误在后续读写时暴露得更具体。
		return ".orciny"
	}
	return filepath.Join(home, ".orciny")
}

func LoadConfig(dir string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist，调用方用 errors.Is 判断「尚未 enroll」
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", ConfigFileName, err)
	}
	return &c, nil
}

func SaveConfig(dir string, c *Config) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("序列化配置: %w", err)
	}
	return atomicfile.Write(filepath.Join(dir, ConfigFileName), b, 0o600)
}
```

- [ ] **Step 9: 写 CLI 与入口**

创建 `agent/cli.go`：

```go
package agent

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/FlintyLemming/orciny"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orciny-agent",
		Short:         "Orciny agent —— 常驻本机，向 hub 汇报状态",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.PersistentFlags().String("dir", "", "agent 数据目录（默认 $ORCINY_HOME 或 ~/.orciny）")
	root.AddCommand(newVersionCmd())
	// enroll / run / status 由计划 4 与计划 7 补上。
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本号",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), orciny.Version)
			return nil
		},
	}
}

// dirFromFlags 解析 --dir，未指定时用默认目录。所有子命令统一用它取目录。
func dirFromFlags(cmd *cobra.Command) string {
	if d, _ := cmd.Flags().GetString("dir"); d != "" {
		return d
	}
	return DefaultDir()
}
```

创建 `agent/agent.go`：

```go
// Package agent 是 agent 侧唯一的对外入口。
// 实现细节藏在 agent/internal/*，hub 侧碰不到（spec §2.2）。
package agent

// Execute 运行 agent 的命令行。cmd/orciny-agent 只调这一个函数。
func Execute() error {
	return newRootCmd().Execute()
}
```

创建 `cmd/orciny-agent/main.go`：

```go
// Command orciny-agent 是 Orciny agent 的可执行入口。
package main

import (
	"os"

	"github.com/FlintyLemming/orciny/agent"
)

func main() {
	if err := agent.Execute(); err != nil {
		os.Exit(1) // cobra 已把错误打到 stderr
	}
}
```

- [ ] **Step 10: 运行测试确认通过**

Run: `go test ./agent/... -v && go build ./cmd/orciny-agent && ./orciny-agent version && rm -f orciny-agent`
Expected: PASS（11 个用例）；`version` 输出 `0.1.0`

- [ ] **Step 11: 提交**

```bash
git add agent cmd/orciny-agent internal/atomicfile
git commit -m "feat: agent 配置、原子写与 CLI 骨架"
```

---

## Task 6: 测试脚手架 `internal/testsupport`

**Files:**
- Create: `internal/testsupport/doc.go`, `internal/testsupport/hub.go`, `internal/testsupport/hub_test.go`

**Interfaces:**
- Consumes: `hub.Attach`、`hub.Config`、`clock.NewFake`
- Produces:
  ```go
  type TestHub struct {
      App     *tests.TestApp
      Hub     *hub.Hub
      Clock   *clock.Fake
      Server  *httptest.Server
      HTTPURL string
      WSURL   string
  }
  func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub
  ```

- [ ] **Step 1: 写失败的测试**

创建 `internal/testsupport/hub_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestNewTestHubServesPocketBaseAPI(t *testing.T) {
	th := testsupport.NewTestHub(t)

	resp, err := http.Get(th.HTTPURL + "/api/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNewTestHubServesEmbeddedUI(t *testing.T) {
	th := testsupport.NewTestHub(t)

	resp, err := http.Get(th.HTTPURL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "Orciny")
}

func TestNewTestHubRunsMigrations(t *testing.T) {
	th := testsupport.NewTestHub(t)
	for _, name := range []string{"machines", "enroll_tokens", "events"} {
		_, err := th.App.FindCollectionByNameOrId(name)
		require.NoError(t, err, "%s 应已由迁移创建", name)
	}
}

func TestNewTestHubURLsAndClock(t *testing.T) {
	th := testsupport.NewTestHub(t)
	require.Contains(t, th.WSURL, "ws://")
	require.Contains(t, th.WSURL, "/api/orciny/ws")
	require.NotNil(t, th.Clock)

	before := th.Clock.Now()
	th.Clock.Advance(time.Minute)
	require.Equal(t, before.Add(time.Minute), th.Clock.Now())
}

func TestNewTestHubAcceptsConfigOverride(t *testing.T) {
	th := testsupport.NewTestHub(t, func(c *hub.Config) {
		c.OfflineGrace = 123 * time.Millisecond
	})
	require.NotNil(t, th.Hub)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -tags=testing ./internal/testsupport/... -v`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 写不带 build tag 的包声明**

创建 `internal/testsupport/doc.go`：

```go
// Package testsupport 提供跨组件集成测试的脚手架（spec §12.1）。
//
// 实现文件全部带 //go:build testing。本文件不带 tag，
// 保证不加 tag 时 `go build ./...` 看到的是一个合法的空包，而不是
// 「build constraints exclude all Go files」。
//
// 边界提示：本包在 hub/ 树之外，因此只能通过 hub 与 agent 的公开入口
// 做集成测试，够不到 hub/internal/*（spec §2.2）。那些包的单元测试必须
// 写在包内部。
package testsupport
```

- [ ] **Step 4: 写脚手架**

创建 `internal/testsupport/hub.go`：

```go
//go:build testing

package testsupport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// TestHub 是一个跑在内存里的完整 hub：真实路由、真实 HTTP 服务、临时数据库。
type TestHub struct {
	App     *tests.TestApp
	Hub     *hub.Hub
	Clock   *clock.Fake
	Server  *httptest.Server
	HTTPURL string // http://127.0.0.1:PORT
	WSURL   string // ws://127.0.0.1:PORT/api/orciny/ws
}

// FakeStart 是假时钟的起点，固定值让失败日志可读。
var FakeStart = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// NewTestHub 起一个测试 hub，并注册好 t.Cleanup。
//
// 默认配置里，走假时钟的时长保持生产值（宽限 5s、心跳 30s），
// 走真实网络 deadline 的时长被压到毫秒级——后者假时钟管不到。
func NewTestHub(t *testing.T, opts ...func(*hub.Config)) *TestHub {
	t.Helper()

	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err, "创建 TestApp")
	t.Cleanup(app.Cleanup)

	fake := clock.NewFake(FakeStart)
	cfg := hub.Config{
		Clock:             fake,
		OfflineGrace:      5 * time.Second,
		HeartbeatInterval: 30 * time.Second,
		ReadTimeout:       2 * time.Second,
		HandshakeTimeout:  500 * time.Millisecond,
		EnrollTokenTTL:    15 * time.Minute,
	}
	for _, o := range opts {
		o(&cfg)
	}

	h, err := hub.Attach(app, cfg)
	require.NoError(t, err, "Attach hub")

	// 下面这段复刻 PocketBase 自身 apis.Serve 的装配顺序：
	// 建 router → 造 ServeEvent → 触发 OnServe（我们的注册就在里面）→ BuildMux。
	router, err := apis.NewRouter(app)
	require.NoError(t, err, "建 router")

	se := new(core.ServeEvent)
	se.App = app
	se.Router = router
	se.Server = &http.Server{}
	require.NoError(t, app.OnServe().Trigger(se, func(e *core.ServeEvent) error {
		mux, err := e.Router.BuildMux()
		if err != nil {
			return err
		}
		e.Server.Handler = mux
		return nil
	}), "触发 OnServe")

	srv := httptest.NewServer(se.Server.Handler)
	t.Cleanup(srv.Close)

	return &TestHub{
		App:     app,
		Hub:     h,
		Clock:   fake,
		Server:  srv,
		HTTPURL: srv.URL,
		WSURL:   "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/orciny/ws",
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test -tags=testing ./internal/testsupport/... -v`
Expected: PASS（5 个用例）

- [ ] **Step 6: 确认不加 tag 也能构建**

Run: `go build ./... && go vet ./...`
Expected: 无输出（`doc.go` 保证空包合法）

- [ ] **Step 7: 全量测试与 lint**

Run: `make test && make lint`
Expected: 全绿

- [ ] **Step 8: 提交**

```bash
git add internal/testsupport
git commit -m "test: 集成测试脚手架 NewTestHub"
```

---

## 完成检查

- [ ] `make test` 全绿，`make lint` 无输出
- [ ] `make build` 产出 `dist/orciny` 与 `dist/orciny-agent`
- [ ] `go build ./...`（不带 testing tag）通过
- [ ] `hub/` 与 `agent/` 之间没有任何 import：`go list -deps ./agent/... | grep -c 'orciny/hub'` 输出 0；反向同理
- [ ] 交付给计划 2 的接口与统括计划「全局接口契约」一致
