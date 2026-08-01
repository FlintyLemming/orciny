# 子计划 17 · CLI 补全、M0 尾巴与 M1 收尾

**前置**：12、15
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`orciny-agent` 的 `sync` / `drift` / `pause` / `resume` 与 `status` 补充、M0 遗留的两项修正（spec §1.3）、运维文档、M1 验收记录。

---

### Task 1: `sync` 与 `status`

**Files:**
- Modify: `agent/cli.go`
- Create: `agent/sync.go`
- Modify: `agent/status.go`
- Test: `agent/cli_test.go`、`agent/status_test.go`

**Interfaces:**
- Produces:
  ```go
  // agent
  func SyncOnce(ctx context.Context, o SyncOptions) (*SyncReport, error)
  type SyncOptions struct {
      Dir              string
      HandshakeTimeout time.Duration
      ReadTimeout      time.Duration
      Logger           *slog.Logger
  }
  type SyncReport struct {
      Revision string
      Seq      uint32
      Actions  map[string]int // 动作名 → 条数
      OK       bool
      Error    string
  }

  // Status 追加字段
  ConfigSet    string `json:"config_set,omitempty"`
  Revision     string `json:"revision,omitempty"`
  Seq          uint32 `json:"seq,omitempty"`
  Health       string `json:"health,omitempty"`
  Mode         string `json:"mode,omitempty"`
  Paused       bool   `json:"paused,omitempty"`
  DriftCount   int    `json:"drift_count,omitempty"`
  ManagedHome  string `json:"managed_home,omitempty"`
  ```

`orciny-agent sync`（spec §7.9）：触发一次立即 pull + apply，**打印 plan 与结果**。它连一条临时连接、拉取、应用、打印，然后退出——不与常驻进程抢连接（hub 允许同指纹的新连接顶掉旧的，见 M0 spec §6.4a，因此 `sync` 会短暂顶掉常驻 agent 的连接，常驻进程随后自动重连）。

> 这一点要写进 `sync` 的帮助文案：「本命令会短暂中断常驻 agent 的连接，它会在几秒内自动重连。」

- [ ] **Step 1: 写失败的测试**

追加到 `agent/cli_test.go`：

```go
func TestSyncCommandFailsWithoutEnroll(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, dir, "sync")
	require.Error(t, err)
	require.Contains(t, out, "尚未 enroll")
}

func TestStatusShowsConfigFields(t *testing.T) {
	dir := t.TempDir()
	seedEnrolled(t, dir) // 该文件已有的辅助：写 agent.yml + identity
	require.NoError(t, state.Save(dir, &state.State{
		ConfigSet: "set1", Revision: "rev7", Seq: 7,
		Mode: "apply", Health: state.HealthDegraded,
		Files: map[string]state.FileState{".claude/CLAUDE.md": {Rendered: "x"}},
	}))

	out, err := runCLI(t, dir, "status")
	require.NoError(t, err)
	require.Contains(t, out, "rev7")
	require.Contains(t, out, "degraded")
	require.Contains(t, out, "apply")
}

// 没有 state.json 时 status 也要能跑，只是说「尚未应用任何配置」。
func TestStatusWithoutState(t *testing.T) {
	dir := t.TempDir()
	seedEnrolled(t, dir)
	out, err := runCLI(t, dir, "status")
	require.NoError(t, err)
	require.Contains(t, out, "尚未应用")
}
```

- [ ] **Step 2: 实现**

`agent/sync.go` 里 `SyncOnce` 复用 `Connect` + `syncer`：连上、`SyncNow()`、等第一条 `ApplyAck` 或超时（10 秒）、把 plan 的动作统计打出来。`Syncer` 需要一个可选的 `OnApplied func(protocol.ApplyAck, applier.Plan)` 回调让 CLI 拿到结果。

输出形如：

```
已连接 hub（https://hub.example）
拉取到版本 v7（rev7abc…）
计划：
  overwrite  .claude/CLAUDE.md
  skip       .claude/settings.json
  merge      .claude.json
应用完成：3 条，1 处改动，耗时 42ms
```

`status` 追加读 `state.json` 与本地对账结果（漂移数走 `watcher.Scan` 的一次性调用，不起监视）。

- [ ] **Step 3: 跑测试确认通过并提交**

Run: `go test -tags=testing ./agent/...`

```bash
git add agent/
git commit -m "feat: orciny-agent sync 与 status 补充配置信息"
```

---

### Task 2: `drift` / `pause` / `resume`

**Files:**
- Create: `agent/drift.go`、`agent/pause.go`
- Modify: `agent/cli.go`
- Test: `agent/drift_test.go`

**Interfaces:**
- Produces:
  ```go
  func LocalDrift(dir string) ([]protocol.DriftItem, error)
  func SetPaused(dir string, paused bool) error
  ```

**`drift` 子命令自带简易 diff**（spec §7.9）：这是「diff 由 hub 计算」的**唯一例外**——离线排查时人得能在机器上看到发生了什么，而这份 diff 不进任何数据结构，只打到终端。

**`pause` / `resume`**：写 `state.json` 的 `paused` 并上报 hub（`MachineInfo.LocalPaused`）。离线时也要能改——它是本地开关，落盘即生效，上报只是让面板知道。

- [ ] **Step 1: 写失败的测试**

Create `agent/drift_test.go`：

```go
package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestLocalDriftDetectsChange(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	seedManagedBaseline(t, dir, home, map[string]string{
		".claude/CLAUDE.md": "# 基线\n",
	})

	// 干净时无漂移
	items, err := agent.LocalDrift(dir)
	require.NoError(t, err)
	require.Empty(t, items)

	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".claude/CLAUDE.md"), []byte("# 改了\n"), 0o644))

	items, err = agent.LocalDrift(dir)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, protocol.DriftModified, items[0].Kind)
}

// 终端里的 diff 必须脱敏——离线排查也不该把密钥打到屏幕上或日志里。
func TestDriftCommandOutputIsRedacted(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	seedManagedBaselineWithSecret(t, dir, home, "sk-real-secret-9999")

	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".claude/settings.json"),
		[]byte(`{"K":"sk-real-secret-9999","new":1}`), 0o600))

	out, err := runCLI(t, dir, "drift")
	require.NoError(t, err)
	require.NotContains(t, out, "sk-real-secret-9999", "终端输出也必须脱敏")
	require.Contains(t, out, "{{cred.")
	require.Contains(t, out, ".claude/settings.json")
}

func TestPauseAndResume(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, state.Save(dir, &state.State{
		Files: map[string]state.FileState{}, Health: state.HealthOK,
	}))

	require.NoError(t, agent.SetPaused(dir, true))
	st, err := state.Load(dir)
	require.NoError(t, err)
	require.True(t, st.Paused)

	require.NoError(t, agent.SetPaused(dir, false))
	st, err = state.Load(dir)
	require.NoError(t, err)
	require.False(t, st.Paused)
}

// 从没 apply 过就 pause：建一份最小 state，而不是报错。
func TestPauseWithoutState(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, agent.SetPaused(dir, true))
	st, err := state.Load(dir)
	require.NoError(t, err)
	require.True(t, st.Paused)
}
```

- [ ] **Step 2: 实现**

`LocalDrift` 起一个不联网的 `watcher`（`Report` 传 nil）调 `Scan(true)`。

`drift` 子命令的输出：

```
本机漂移（2 项）：

  modified  .claude/CLAUDE.md
    - # 原始规矩
    + # 改过的规矩

  added     .claude/skills/新的/SKILL.md
    + # 新技能

提示：这份差异由本机计算，仅供排查；收编与恢复请在 Web 上操作。
```

diff 用一个 30 行的行级 LCS 自己算（agent 不引 difflib——它是 hub 侧的依赖，而 agent 要瘦）。**打印前的内容一律取 `render.Restore` 的结果**，凭据已替回占位符。

- [ ] **Step 3: 跑测试确认通过并提交**

Run: `go test -tags=testing ./agent/...`

```bash
git add agent/
git commit -m "feat: orciny-agent drift / pause / resume"
```

---

### Task 3: M0 尾巴之一 —— `hub.pub` 解析失败进 `Compromised`

**Files:**
- Modify: `agent/internal/identity/identity.go` 或 `agent/internal/conn/client.go`
- Test: `agent/internal/conn/client_test.go`

**背景（spec §1.3 第 1 条）**：M0 验收时发现，`hub.pub` 内容无法解析时 agent 走的是通用指数退避、无限重试，而非进入 `Compromised` 终态。一个被替换成垃圾的钉扎公钥同样属于「需要人来判断」的情形，按 M0 spec §7.2 的精神应进终态。

M0 的 `ErrHubSignature` 已是终态错误。这里要做的是：把「`hub.pub` 加载失败」也归到同一类。

- [ ] **Step 1: 写失败的测试**

追加到 `agent/internal/conn/client_test.go`：

```go
// hub.pub 被替换成垃圾时必须进 Compromised 终态，而不是无限重试。
//
// M0 验收记录 AB：那串被误当成合法的公钥其实是 SSH 格式，48 字节，
// agent 在**加载**阶段就报错，走不到签名验证，于是落进通用退避
// ——这条路径必须与签名验证失败同等对待（spec §1.3）。
func TestCorruptHubKeyEntersCompromised(t *testing.T) {
	for name, content := range map[string]string{
		"垃圾内容":  "这不是 base64!!!",
		"长度不对":  base64.StdEncoding.EncodeToString(make([]byte, 48)),
		"空文件":   "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			seedIdentity(t, dir)
			require.NoError(t, os.WriteFile(
				filepath.Join(dir, "identity", "hub.pub"), []byte(content), 0o600))

			clk := clock.NewFake(testStart)
			var states []conn.State
			var mu sync.Mutex
			c := conn.NewClient(conn.ClientConfig{
				Dial: func(ctx context.Context) (*conn.Session, error) {
					return agentConnect(ctx, dir)
				},
				Clock: clk,
				OnState: func(s conn.State, _ error) {
					mu.Lock()
					states = append(states, s)
					mu.Unlock()
				},
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- c.Run(ctx) }()

			select {
			case err := <-done:
				require.Error(t, err, "必须以终局错误退出，而不是继续重试")
			case <-time.After(2 * time.Second):
				t.Fatal("Run 未退出——说明它在无限重试")
			}

			mu.Lock()
			defer mu.Unlock()
			require.Contains(t, states, conn.StateCompromised)
		})
	}
}
```

- [ ] **Step 2: 实现**

在 `agent/internal/identity` 里为 hub 公钥的加载错误加一个哨兵：

```go
// ErrHubKeyUnusable 表示钉扎的 hub 公钥无法使用（文件损坏、长度不对、
// 不是合法 base64）。
//
// 它与「签名验证失败」同等对待，都进 Compromised 终态：一个被替换成垃圾
// 的钉扎公钥同样属于「需要人来判断」的情形（M0 spec §7.2 的精神，
// M1 spec §1.3 第 1 条）。自作主张重试只会掩盖问题。
var ErrHubKeyUnusable = errors.New("identity: 钉扎的 hub 公钥无法使用")
```

`LoadHubKey` 的各条错误路径都包装它。`conn.Client.Run` 的错误分流里，把 `errors.Is(err, identity.ErrHubKeyUnusable)` 与 `errors.Is(err, ErrHubSignature)` 并列，都进 `StateCompromised` 并返回终局错误。

日志文案要说清怎么办：「钉扎的 hub 公钥无法使用（…）。这可能意味着密钥文件被损坏或篡改。请确认 hub 身份后重新 enroll：`orciny-agent enroll --hub <url> --token <token>`。」

- [ ] **Step 3: 跑测试确认通过并提交**

Run: `go test -tags=testing ./agent/...`

```bash
git add agent/
git commit -m "fix: hub.pub 无法解析时进入 Compromised 终态"
```

---

### Task 4: M0 尾巴之二 —— DoD 第 5 条改为 75 秒

**Files:**
- Modify: `docs/superpowers/plans/2026-07-28-m0-skeleton/00-overview.md`（验收对照表第 5 行）
- Modify: `docs/superpowers/plans/2026-07-28-m0-skeleton/acceptance.md`
- Modify: 运维文档里提到「60 秒」的地方

**背景（spec §1.3 第 2 条）**：M0 DoD 第 5 条的「断网 60 秒转 offline」与 spec 自身的 70 秒读超时不自洽。改为「75 秒内」。**不动超时参数**——70 秒是为了容得下 30 秒心跳漏两拍，为凑验收数字去压它会让抖动误判增多。

- [ ] **Step 1: 改文档**

M0 计划的验收对照表第 5 行改成：

```
| 5 | 断网 **75 秒内**转 offline，恢复自动重连 | 06、07 |
```

并在 `acceptance.md` 里追加一段说明：

```markdown
### 第 5 条的数字修正（M1 spec §1.3）

原文写「断网 60 秒转 offline」，与 spec §6.2 的 70 秒读超时不自洽：
hub 要等读超时到期才会发现连接已死，60 秒时它必然还是 online。

改为「75 秒内」。**不动超时参数**——70 秒是为了容得下 30 秒心跳漏两拍
（30×2 + 10 秒余量），为凑验收数字去压它会让网络抖动被误判成离线。
75 = 70（读超时）+ 5（离线宽限）。
```

- [ ] **Step 2: 提交**

```bash
git add docs/
git commit -m "docs: M0 DoD 第 5 条的离线判定改为 75 秒"
```

---

### Task 5: 运维文档

**Files:**
- Modify: `docs/operations.md`

补上 M1 引入的运维知识：

- **主密钥**：`pb_data/secret.key` 必须与数据库一起备份。恢复到新机器时忘了带它，hub 会**拒绝启动**并给出明确错误——这是有意的（spec §6.6）。也可以用 `ORCINY_SECRET_KEY` 环境变量注入。
- **agent 的两个 HOME**：`$ORCINY_HOME`（默认 `~/.orciny`，agent 自己的数据）与 `managed_home`（默认 `os.UserHomeDir()`，被管理的那个）。容器里跑 agent 时后者要显式指定。
- **快照与磁盘占用**：`~/.orciny/snapshots/` 保留最近 5 次 apply 前的现场；`~/.orciny/blobs/` 是内容缓存。两者都可以安全删除，代价是下次 apply 要重新从 hub 拉内容、且丢掉回滚现场。
- **`degraded` 怎么办**：说明它的含义（apply 失败且回滚也失败）、排查步骤（看 `~/.orciny/logs/`、看快照目录里的原始内容）、以及解除后会转 survey 模式。
- **`.claude.json` 竞写**（子计划 07 已写，确认在位）。
- **inotify watch 数**（子计划 07 已写，确认在位）。
- **CLI 速查**：`sync` / `drift` / `pause` / `resume` / `status` 各自的用途与副作用。

- [ ] **Step 1: 写文档并提交**

```bash
git add docs/
git commit -m "docs: 补充 M1 的运维知识"
```

---

### Task 6: M1 收尾与真机验收

**Files:**
- Modify: `docs/superpowers/plans/2026-07-31-m1-config-loop/acceptance.md`

- [ ] **Step 1: 全量测试**

```bash
go test -tags=testing ./...
go test -tags=testing -run TestSecurity -v ./agent/...
cd hub/internal/site && npm test && npm run build
cd - && git checkout -- hub/internal/site/dist/index.html
make lint
```

- [ ] **Step 2: 真机验收 DoD 3、4、5、6、11、12、15**

三台真机，按 spec §12 逐条走：

| # | 怎么验 |
|---|---|
| 3 | 第三台机器选 survey 指派 → `ls -la ~/.claude` 确认无改动 → 收件箱里有全部差异 |
| 4 | 任一机器 `mkdir -p ~/.claude/skills/foo && echo x > .../SKILL.md` → 计时到收件箱出现（应 < 60 秒）→ 收编 → 其余机器 `ls` 确认 |
| 5 | 改 CLAUDE.md → 收件箱看 diff → 点恢复 → `diff` 确认回到基线 |
| 6 | 改 `settings.json` 里的密钥值 → 收件箱条目正确 → **进 hub 容器查库**：`sqlite3 pb_data/data.db "select hash from blobs"` 逐个取内容 grep 密钥，必须搜不到 |
| 11 | 两台机器改同一文件 → 收件箱多选两条 → 收编按钮禁用且给出理由 → 打开三方对比 |
| 12 | `rm ~/.orciny/state.json && systemctl restart orciny-agent` → 面板转 survey → `~/.claude` 未被改动 |
| 15 | `python3 -c "import os,base64;print(base64.b64encode(os.urandom(32)).decode())" > ~/.orciny/identity/hub.pub` → agent 日志报 Compromised 且停止重试 |

第 14 条（内存）用 `ps -o rss= -p $(pgrep orciny-agent)` 与容器的 `docker stats` 记录。

- [ ] **Step 3: 记录结果**

`acceptance.md` 补齐 15 条的实测结论。未通过的写清现象、定位与修复提交号——M0 的做法（第 8 条首测未通过 → 修好 → 复测通过）值得照搬。

- [ ] **Step 4: 登记偏离**

把实现过程中产生的、与计划正文不一致的写法追加到 `00-overview.md` 的「与 spec 的偏离记录」与「实现期对计划代码的修正」两张表里。M0 的这两张表在 M1 期间被反复查阅，值得同样认真地维护。

- [ ] **Step 5: 提交**

```bash
git add docs/
git commit -m "docs: M1 验收记录"
```

---

### Task 7: 分支收尾

- [ ] **Step 1: 用 superpowers:finishing-a-development-branch 收尾**

REQUIRED SUB-SKILL: `superpowers:finishing-a-development-branch`
