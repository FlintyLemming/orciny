# 子计划 04 · 快照组装、重注入与版本门槛

**前置**：01、02、03
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`configsync` 组装并裁剪 provider 值、定向版本门槛、`NotifyProvider` 重注入反查链、`NotifyCredential` 认 Provider；`hub` 公开入口与 HTTP 路由；一条真端到端。

**本子计划走完，数据面就闭环了**——没有任何 UI，全部靠测试验收（spec §12）。

---

### Task 1: 快照组装 provider 六个键

**Files:**
- Create: `hub/internal/configsync/provider.go`
- Modify: `hub/internal/configsync/service.go`（`Deps` 加 `Providers`、`Snapshot` 接线）
- Modify: `hub/hub.go`（装配 `Providers`）
- Test: `hub/internal/configsync/service_test.go`（追加）

**Interfaces:**
- Consumes: `providers.Store`、`credentials.Store.ValueByID`、`revisions.binding`、`configsets.Refs.ProviderKeys`
- Produces: `ErrBindingMissing`、`ErrEmptyModelSlot`；`ConfigSnapshot.Provider` 被填上

**只发用得到的那些**（spec §5.1）：与凭据策略一致——按 `revisions.refs.provider_keys` 裁剪，没引用 `{{provider.model_opus}}` 就不下发该键。**不读 blob 内容。** 少一个键少一处泄露面，`auth_token` 尤其。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsync/service_test.go`（用该文件既有的 rig）：

```go
func TestSnapshotFillsProviderValues(t *testing.T) {
	r := newRig(t) // 该文件既有的构造函数
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")

	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`,
	}, &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	})
	r.assign(t, machineID, setID, "apply")

	snap, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"base_url":   "https://open.bigmodel.cn/api/anthropic",
		"auth_token": "sk-zhipu-abcdefghij",
		"model":      "glm-5.1",
	}, snap.Provider, "只发被引用到的三个键，四槽里没被引用的不发")
}

// 没引用的键一个都不下发——少一个键少一处泄露面（spec §5.1）。
func TestSnapshotOmitsUnreferencedProviderKeys(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "主模型是 {{provider.model}}",
	}, &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-5.1", Opus: "glm-5.1", Sonnet: "glm-5.1", Haiku: "glm-5.1",
		},
	})
	r.assign(t, machineID, setID, "apply")

	snap, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"model": "glm-5.1"}, snap.Provider)
	require.NotContains(t, snap.Provider, "auth_token", "没引用就绝不下发 key")
}

// 纵深防御：这种状态本应被发布校验挡住（spec §5.1 / §7）。
func TestSnapshotRefusesProviderRefsWithoutBinding(t *testing.T) {
	r := newRig(t)
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, nil)
	r.assign(t, machineID, setID, "apply")

	_, err := r.sync.Snapshot(machineID)
	require.ErrorIs(t, err, configsync.ErrBindingMissing)
}

// 透传模式下引用了模型槽 → 拒绝下发并归因，而不是发一个空串下去
// 让 Claude Code 去打一个空模型名。
func TestSnapshotRefusesEmptyModelSlot(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "Anthropic 官方", "https://api.anthropic.com", "sk-ant-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_MODEL":"{{provider.model}}"}}`,
	}, &providers.Binding{Provider: provID}) // 四槽全空 = 透传
	r.assign(t, machineID, setID, "apply")

	_, err := r.sync.Snapshot(machineID)
	require.ErrorIs(t, err, configsync.ErrEmptyModelSlot)
	require.Contains(t, err.Error(), "provider.model")
}

// 绑了但没用：警告级，照常下发，Provider 为空。
func TestSnapshotWithBindingButNoRefs(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		"CLAUDE.md": "没有任何占位符",
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	snap, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
	require.Empty(t, snap.Provider)
}
```

> `seedProvider` / `seedSetWithBinding` 是本测试文件的新辅助函数：前者建一条凭据 + 一条 Provider 记录，后者在既有的建配置集辅助上多一步 `SetDraftBinding` 再发布。照该文件既有辅助函数的写法加。

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsync/ -run Provider -v
```

Expected: FAIL，`snap.Provider` 为 nil。

- [ ] **Step 3: 实现**

`hub/internal/configsync/service.go` 的 `Deps` 追加：

```go
	// Providers 供组装快照时查绑定指向的服务配置。
	Providers *providers.Store
```

`Snapshot` 在算完 `refs` 之后、组装返回值之前插入：

```go
	provider, err := s.providerValues(head, refs)
	if err != nil {
		return snap, err
	}
```

返回值追加 `Provider: provider,`。

Create `hub/internal/configsync/provider.go`：

```go
package configsync

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

var (
	// ErrBindingMissing：Revision 引用了 {{provider.*}} 但没有绑定。
	// 这种状态本应被发布校验挡住（spec §7），此处是纵深防御（spec §5.1）。
	ErrBindingMissing = errors.New("configsync: 引用了 {{provider.*}} 但没有服务绑定")

	// ErrEmptyModelSlot：引用了某个模型槽，但绑定里该槽是空的（透传模式）。
	// 发一个空串下去会让 Claude Code 去打一个空模型名，错误现场离原因很远。
	ErrEmptyModelSlot = errors.New("configsync: 引用了模型槽但绑定里该槽为空")
)

// providerValues 组装快照里的 provider 六个键（spec §5.1）。
//
// 裁剪口径与凭据一致：只发 refs.provider_keys 里出现过的键。这是最小权限，
// 也是一条实际的防线——少一个键少一处泄露面，auth_token 尤其。
// 判定全程**不读 blob 内容**，这正是 M1 立 refs 字段的初衷。
func (s *Service) providerValues(head *core.Record, refs configsets.Refs) (map[string]string, error) {
	var b providers.Binding
	// 空 JSON 字段解不动是正常情形，按未绑定处理。
	_ = head.UnmarshalJSONField("binding", &b)

	if b.Provider == "" {
		if len(refs.ProviderKeys) > 0 {
			return nil, fmt.Errorf("%w：版本 %s 引用了 %v",
				ErrBindingMissing, head.Id, refs.ProviderKeys)
		}
		return nil, nil
	}
	if len(refs.ProviderKeys) == 0 {
		return nil, nil // 绑了但没用，警告级（spec §7 第 2 条），照常下发
	}
	if s.d.Providers == nil {
		return nil, fmt.Errorf("configsync: providers 服务未装配")
	}

	prov, err := s.d.Providers.Get(b.Provider)
	if err != nil {
		return nil, err
	}
	// key 本身在凭据库里，Provider 只存引用（spec §2.1）。
	token, err := s.d.Creds.ValueByID(prov.GetString("credential"))
	if err != nil {
		return nil, fmt.Errorf("configsync: 取服务配置 %s 的凭据: %w", b.Provider, err)
	}

	all := map[string]string{
		"base_url":     prov.GetString("base_url"),
		"auth_token":   token,
		"model":        b.Models.Main,
		"model_opus":   b.Models.Opus,
		"model_sonnet": b.Models.Sonnet,
		"model_haiku":  b.Models.Haiku,
	}
	out := make(map[string]string, len(refs.ProviderKeys))
	for _, k := range refs.ProviderKeys {
		v, ok := all[k]
		if !ok {
			continue // 非内置名，protocol 的词法早就拦过，这里只是稳一手
		}
		if v == "" {
			return nil, fmt.Errorf("%w：{{provider.%s}} 被引用，但绑定里该值是空的", ErrEmptyModelSlot, k)
		}
		out[k] = v
	}
	return out, nil
}
```

`hub/hub.go` 的装配：在 `h.sets` 之后建 store，并传给 configsync。

```go
	h.provs = providers.NewStore(e.App, h.events)
	// …
	h.sync = configsync.NewService(configsync.Deps{
		App: e.App, Blobs: h.blobs, Sets: h.sets, Revs: h.revs,
		Creds: h.creds, Providers: h.provs, Events: h.events, Sender: h.machines,
		Importer: h.importer, Logger: e.App.Logger(),
	})
```

`Hub` 结构体加字段 `provs *providers.Store`。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/ && git commit -m "feat(hub): 快照按 provider_keys 裁剪注入服务值"
```

---

### Task 2: 定向版本门槛

**Files:**
- Modify: `orciny.go`（`Version` 抬到 `0.2.0`、新增 `MinProviderAgentVersion`）
- Modify: `hub/internal/configsync/provider.go`（`ErrAgentTooOld`、`checkAgentVersion`）
- Modify: `hub/internal/configsync/service.go`（`Snapshot` 调用、`Pull` 的失败落库）
- Test: `orciny_test.go`（追加）
- Test: `hub/internal/configsync/service_test.go`（追加）

**Interfaces:**
- Consumes: `machines.agent_version`
- Produces: `orciny.MinProviderAgentVersion`、`configsync.ErrAgentTooOld`

**不动全局 `MinAgentVersion`**（spec §10）：它在握手层拦截，一抬就把所有低版本 agent 挡在门外——包括那些指派的配置集根本没用绑定的机器。惩罚面远大于问题面。

**为什么不是「发下去让它渲染失败再回滚」**：失败回滚是兜底不是主路径，而且回滚的错误信息（「占位符语法错误」）离真实原因（「agent 老了」）很远。

- [ ] **Step 1: 写失败的测试**

追加到 `orciny_test.go`：

```go
func TestMinAgentVersionStaysAtM1(t *testing.T) {
	require.Equal(t, "0.1.0", orciny.MinAgentVersion.String(),
		"M1.5 的版本门槛是定向的，不许抬全局握手门槛（spec §10）")
}

func TestMinProviderAgentVersion(t *testing.T) {
	require.Equal(t, "0.2.0", orciny.MinProviderAgentVersion.String())
	require.False(t, orciny.MinProviderAgentVersion.LT(orciny.MinAgentVersion),
		"定向门槛不该低于全局门槛")
}
```

追加到 `hub/internal/configsync/service_test.go`：

```go
func TestSnapshotRefusesOldAgentWhenBound(t *testing.T) {
	r := newRig(t)
	r.setAgentVersion(t, machineID, "0.1.0") // 新辅助：改 machines.agent_version

	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	_, err := r.sync.Snapshot(machineID)
	require.ErrorIs(t, err, configsync.ErrAgentTooOld)
	require.Contains(t, err.Error(), "0.1.0")
	require.Contains(t, err.Error(), "0.2.0")
}

// 没绑定的配置集不受门槛影响——惩罚面不该扩大到无关机器。
func TestSnapshotAllowsOldAgentWhenUnbound(t *testing.T) {
	r := newRig(t)
	r.setAgentVersion(t, machineID, "0.1.0")
	setID := r.seedSetWithBinding(t, map[string]string{"CLAUDE.md": "无占位符"}, nil)
	r.assign(t, machineID, setID, "apply")

	_, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
}

// Pull 遇到门槛不下发，且把原因写进 assignment——UI 在机器行与配置集页
// 都要显示得出来。
func TestPullMarksAssignmentFailedOnOldAgent(t *testing.T) {
	r := newRig(t)
	r.setAgentVersion(t, machineID, "0.1.0")
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	r.sync.Pull(machineID, protocol.ConfigPull{Have: ""})

	assign, err := r.sets.Assignment(machineID)
	require.NoError(t, err)
	require.Equal(t, "failed", assign.GetString("state"))
	require.Contains(t, assign.GetString("last_error"), "版本过低")
	require.Empty(t, r.sender.sent, "不许下发任何快照") // 用该文件既有的假 Sender
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./ ./hub/internal/configsync/ -run 'Version|OldAgent' -v
```

Expected: FAIL，`MinProviderAgentVersion` 未定义。

- [ ] **Step 3: 实现**

`orciny.go`：

```go
// Version 是 hub 与 agent 的共同版本号。
var Version = "0.2.0"

// MinAgentVersion 是 hub 接受的最低 agent 版本。
// 低于此版本的 agent 在握手第一步就被 CodeVersionTooOld 拒绝。
//
// **M1.5 不动它**（M1.5 spec §10）：它在握手层拦截，一抬就把所有低版本
// agent 挡在门外——包括那些指派的配置集根本没用绑定的机器。
var MinAgentVersion = semver.MustParse("0.1.0")

// MinProviderAgentVersion 是能渲染 {{provider.*}} 的最低 agent 版本。
//
// 定向门槛：只在快照真的带绑定时才检查（M1.5 spec §10）。
var MinProviderAgentVersion = semver.MustParse("0.2.0")
```

`hub/internal/configsync/provider.go` 追加：

```go
// ErrAgentTooOld：目标机器的 agent 太老，渲染不了 {{provider.*}}。
var ErrAgentTooOld = errors.New("configsync: agent 版本过低")

// checkAgentVersion 是 spec §10 的定向门槛。
//
// 不下发 好过 发下去让它渲染失败再回滚：失败回滚是兜底不是主路径，
// 而且回滚的错误信息（「占位符语法错误」）离真实原因（「agent 老了」）很远。
func (s *Service) checkAgentVersion(machineID string) error {
	m, err := s.d.App.FindRecordById("machines", machineID)
	if err != nil {
		return fmt.Errorf("configsync: 机器 %s 不存在: %w", machineID, err)
	}
	raw := strings.TrimPrefix(m.GetString("agent_version"), "v")
	v, err := semver.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w（版本号 %q 解析不出来），请升级到 v%s 或更高后重试",
			ErrAgentTooOld, m.GetString("agent_version"), orciny.MinProviderAgentVersion)
	}
	if v.LT(orciny.MinProviderAgentVersion) {
		return fmt.Errorf("%w（v%s < v%s），请升级 agent 后重试",
			ErrAgentTooOld, v, orciny.MinProviderAgentVersion)
	}
	return nil
}
```

`providerValues` 在确认要发 provider 值之后、查 Provider 记录之前加门槛。为此把签名改成带 `machineID`：

```go
func (s *Service) providerValues(machineID string, head *core.Record, refs configsets.Refs) (map[string]string, error) {
	// …binding 为空 / refs 为空的两个早返回不变…

	if err := s.checkAgentVersion(machineID); err != nil {
		return nil, err
	}
	// …
}
```

`Snapshot` 的调用点跟着改成 `s.providerValues(machineID, head, refs)`。

`Pull` 里把这三类失败落进 assignment：

```go
	snap, err := s.Snapshot(machineID)
	if err != nil {
		if errors.Is(err, configsets.ErrNoAssignment) {
			s.log.Debug("机器未指派配置集，忽略拉取", "machine", machineID)
			return
		}
		// 绑定相关的失败要能归因：写进 assignment，UI 在机器行与配置集页
		// 都显示得出来（spec §10）。其余失败仍然只记日志。
		if isBindingFault(err) {
			s.failAssignment(machineID, err)
			return
		}
		s.log.Warn("组装快照失败", "machine", machineID, "error", err)
		return
	}
```

```go
func isBindingFault(err error) bool {
	return errors.Is(err, ErrAgentTooOld) ||
		errors.Is(err, ErrBindingMissing) ||
		errors.Is(err, ErrEmptyModelSlot)
}

// failAssignment 把指派置 failed 并写明原因。
//
// 不置 degraded：机器上什么都没被改过，不需要人工解除——
// 升级 agent 或修好绑定之后，下一次 apply 成功就会把状态翻回 aligned。
func (s *Service) failAssignment(machineID string, cause error) {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return
	}
	assign.Set("state", configsets.StateFailed)
	assign.Set("last_error", cause.Error())
	if err := s.d.App.Save(assign); err != nil {
		s.log.Warn("保存指派状态失败", "machine", machineID, "error", err)
		return
	}
	s.log.Warn("拒绝下发含服务绑定的快照", "machine", machineID, "error", cause)
	if err := s.d.Events.Write(events.KindApplyFailed, machineID, map[string]any{
		"error": cause.Error(), "reason": "binding",
	}); err != nil {
		s.log.Warn("写 apply.failed 事件失败", "error", err)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... -v
```

Expected: 全部 PASS。注意 `Version` 抬到 `0.2.0` 可能打到断言版本号的既有用例——那些用例应当断言 `orciny.Version` 这个变量而不是字面量；若有字面量，改成引用变量。

- [ ] **Step 5: 提交**

```bash
git add orciny.go orciny_test.go hub/internal/configsync/ && git commit -m "feat(hub): 含服务绑定的快照做定向 agent 版本门槛"
```

---

### Task 3: 重注入的反查链

**Files:**
- Modify: `hub/internal/configsync/notify.go`
- Test: `hub/internal/configsync/service_test.go`（追加）

**Interfaces:**
- Consumes: `config_sets.head_provider`、`credentials.ReferencedBy` 的 `providerIDs`
- Produces: `NotifyProvider(providerID string) error`

**核心不变量**：改 Provider 的 `base_url` / `models` / `defaults` → **不产生新 Revision**，走 `ConfigNotify` 且**不带 `RevisionID`**（= 仅 secrets 变更，M1 已有的语义，spec §5.2）。

反查链靠 `head_provider` 这个冗余字段收敛成两跳：

```
providers.id
  → config_sets WHERE head_provider = id      （索引查询）
  → assignments WHERE config_set IN (...)     （已有索引 idx_assignments_set）
  → SendTo(machine)
```

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/configsync/service_test.go`：

```go
// 本设计的核心不变量：改 Provider → 重注入 → 不产生新 Revision、
// checksum 不变（spec §2.2）。
func TestNotifyProviderDoesNotCreateRevision(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	before, err := r.revs.Head(setID)
	require.NoError(t, err)
	beforeCount := r.revisionCount(t, setID) // 新辅助：数该配置集的 revision 条数

	// 改 base_url
	_, err = r.provs.Update(provID, r.inputFor(t, provID,
		"https://open.bigmodel.cn/api/coding/paas/v4"))
	require.NoError(t, err)
	require.NoError(t, r.sync.NotifyProvider(provID))

	after, err := r.revs.Head(setID)
	require.NoError(t, err)
	require.Equal(t, before.Id, after.Id, "改 Provider 绝不产生新 Revision")
	require.Equal(t, before.GetString("checksum"), after.GetString("checksum"))
	require.Equal(t, beforeCount, r.revisionCount(t, setID))

	require.Len(t, r.sender.sent, 1)
	n := r.sender.sent[0].payload.(protocol.ConfigNotify)
	require.Equal(t, setID, n.ConfigSetID)
	require.Empty(t, n.RevisionID, "仅 secrets 变更，不带 RevisionID")
	require.Equal(t, protocol.ReasonRotated, n.Reason)

	// 新快照里的值确实变了。
	snap, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4", snap.Provider["base_url"])
}

func TestNotifyProviderSkipsPausedSets(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	set, err := r.app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	set.Set("paused", true)
	require.NoError(t, r.app.Save(set))

	require.NoError(t, r.sync.NotifyProvider(provID))
	require.Empty(t, r.sender.sent, "暂停下发的配置集不该收到重注入通知")
}

// 轮换 Provider 用的凭据也要重注入——它落在现有的凭据轮换通道上，
// 但那条通道只认 refs.creds，认不出 relation 引用（spec §5.2 / §5.3）。
func TestNotifyCredentialReachesProviderBoundMachines(t *testing.T) {
	r := newRig(t)
	provID := r.seedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID := r.seedSetWithBinding(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`,
	}, &providers.Binding{Provider: provID})
	r.assign(t, machineID, setID, "apply")

	require.NoError(t, r.creds.Rotate("zhipu_key", "sk-zhipu-newvalue123"))
	require.NoError(t, r.sync.NotifyCredential("zhipu_key"))

	require.Len(t, r.sender.sent, 1, "配置集本身没有 cred.* 引用，但 Provider 引用了它")
	n := r.sender.sent[0].payload.(protocol.ConfigNotify)
	require.Equal(t, setID, n.ConfigSetID)
	require.Empty(t, n.RevisionID)

	snap, err := r.sync.Snapshot(machineID)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-newvalue123", snap.Provider["auth_token"])
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/configsync/ -run Notify -v
```

Expected: FAIL，`NotifyProvider` 未定义 / 轮换通知没送到。

- [ ] **Step 3: 实现**

`hub/internal/configsync/notify.go` 追加：

```go
// NotifyProvider 在改了 Provider 之后重注入全机队（spec §5.2）。
//
// **不产生新 Revision**：走 ConfigNotify 且不带 RevisionID（= 仅 secrets 变更，
// M1 已有的语义）。agent 拉回来发现 revision 相同但 provider 值变了，
// 只重渲染受影响的文件；rendered hash 跟着更新，因此**不产生漂移**
// （spec §4.3——这条是 M1 设计正确性的红利，一行新代码都不需要）。
//
// 反查靠 config_sets.head_provider 这个冗余字段收敛成一次索引查询，
// 而不是 JSON 字段扫描 + 三次 join（spec §2.2）。
func (s *Service) NotifyProvider(providerID string) error {
	sets, err := s.d.App.FindRecordsByFilter("config_sets",
		"head_provider = {:p}", "", 0, 0, map[string]any{"p": providerID})
	if err != nil {
		return fmt.Errorf("configsync: 查询绑定了服务配置 %s 的配置集: %w", providerID, err)
	}
	for _, set := range sets {
		if set.GetBool("paused") {
			s.log.Info("配置集已暂停下发，跳过重注入", "config_set", set.Id)
			continue
		}
		machineIDs, err := s.d.Sets.AssignedMachines(set.Id)
		if err != nil {
			return err
		}
		for _, id := range machineIDs {
			s.send(id, protocol.ConfigNotify{
				ConfigSetID: set.Id, Reason: protocol.ReasonRotated,
			})
		}
	}
	return nil
}
```

`NotifyCredential` 末尾追加：

```go
	setIDs, _, providerIDs, err := s.d.Creds.ReferencedBy(name)
	if err != nil {
		return err
	}
	// …原有的按 setIDs 通知不变…

	// Provider 引用凭据的方式是 relation 字段，不在 refs 里（spec §5.3）。
	// 漏掉这一段，轮换 key 之后全机队还在用旧 key。
	for _, pid := range providerIDs {
		if err := s.NotifyProvider(pid); err != nil {
			return err
		}
	}
	return nil
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsync/ && git commit -m "feat(hub): 改 Provider 与轮换凭据都重注入全机队"
```

---

### Task 4: `hub` 公开入口与 HTTP 路由

**Files:**
- Create: `hub/providers.go`
- Modify: `hub/internal/routes/routes.go`（`Deps` 加 `Providers`、注册端点）
- Create: `hub/internal/routes/providers.go`
- Modify: `hub/internal/routes/config.go`（`Admin` 接口追加方法）
- Modify: `hub/hub.go`（`routes.Deps` 传 `Providers`）
- Test: `hub/internal/routes/providers_test.go`

**Interfaces:**
- Consumes: `providers.Store`、`configsets.Service`、`configsync.Service`
- Produces: 00-overview「HTTP API」表里的六个端点（反查两个在子计划 07）；`Hub.CreateProvider` / `UpdateProvider` / `DeleteProvider` / `SetBinding` / `FixAuthField`

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/routes/providers_test.go`，照 `routes_test.go` / `config_test.go` 起 router 的方式：

```go
// 全部管理端点必须要求 superuser（M0 spec §5.3 的纪律）。
func TestProviderRoutesRequireSuperuser(t *testing.T) {
	srv := newRouterServer(t, routes.Deps{Admin: &fakeAdmin{}})
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/orciny/provider-presets", ""},
		{"POST", "/api/orciny/providers", `{"name":"x","base_url":"https://a.test","auth_field":"ANTHROPIC_AUTH_TOKEN","credential":"c1"}`},
		{"PUT", "/api/orciny/providers/p1", `{"name":"x","base_url":"https://a.test","auth_field":"ANTHROPIC_AUTH_TOKEN","credential":"c1"}`},
		{"DELETE", "/api/orciny/providers/p1", ""},
		{"PUT", "/api/orciny/config-sets/s1/binding", `{"provider":"p1"}`},
		{"POST", "/api/orciny/config-sets/s1/fix-auth-field", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s %s", c.method, c.path)
	}
}

// 预设表是编译期常量，端点只做序列化。
func TestProviderPresetsReturnsSeed(t *testing.T) {
	// 带 superuser 鉴权请求 /api/orciny/provider-presets，
	// 解出 []providers.Preset，断言长度等于 len(providers.Presets())
	// 且第一条的 base_url 非空。
}

// 清空绑定用 PUT + {"provider":""}，与「设置」同一个端点，不另开 DELETE。
func TestSetBindingAcceptsEmptyProvider(t *testing.T) {
	// 断言 fakeAdmin 收到的 SetBinding 参数为 nil。
}

func TestDeleteProviderInUseMaps409(t *testing.T) {
	// fakeAdmin.DeleteProvider 返回 providers.ErrInUse，
	// 断言 HTTP 409（mapErr 已有 ErrInUse → 409 的映射，照它加）。
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/routes/ -run Provider -v
```

Expected: FAIL，404。

- [ ] **Step 3: 实现**

Create `hub/providers.go`：

```go
package hub

import (
	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// CreateProvider 新建 AI 服务配置。
//
// 新建**不需要**重注入：还没有任何配置集绑着它。
func (h *Hub) CreateProvider(in providers.Input) (string, error) {
	rec, err := h.provs.Create(in)
	if err != nil {
		return "", err
	}
	return rec.Id, nil
}

// UpdateProvider 改 base_url / 模型 / 凭据，然后立即重注入全机队。
//
// **不产生新 Revision**（spec §2.2）。UI 上这件事必须对用户可见——
// 保存前提示「这会立即重注入到 N 台机器，不产生新版本」（spec §8.1）。
func (h *Hub) UpdateProvider(id string, in providers.Input) error {
	if _, err := h.provs.Update(id, in); err != nil {
		return err
	}
	return h.sync.NotifyProvider(id)
}

// DeleteProvider 删除服务配置。被任何配置集绑定时拒绝。
func (h *Hub) DeleteProvider(id string) error {
	return h.provs.Delete(id)
}

// SetBinding 设置或清空配置集的草稿绑定。b 为 nil 即解绑。
//
// 只改草稿：换绑定要产生新 Revision，走正常的发布流程（spec §2.2 / §8.2）。
func (h *Hub) SetBinding(setID string, b *providers.Binding) error {
	return h.sets.SetDraftBinding(setID, b)
}

// FixAuthField 把 settings.json 里承载 API key 的 env 键名改成绑定的
// auth_field（spec §7 第 3 条）。改动落在草稿上，用户在 diff 里看得见。
func (h *Hub) FixAuthField(setID string) error {
	return h.sets.FixAuthField(setID)
}

// ProviderPresets 返回内置预设表。编译期常量，只读（spec §2.3）。
func (h *Hub) ProviderPresets() []providers.Preset {
	return providers.Presets()
}
```

`hub/internal/routes/config.go` 的 `Admin` 接口追加：

```go
	CreateProvider(in providers.Input) (string, error)
	UpdateProvider(id string, in providers.Input) error
	DeleteProvider(id string) error
	SetBinding(setID string, b *providers.Binding) error
	FixAuthField(setID string) error
	ProviderPresets() []providers.Preset
```

`mapErr` 追加：

```go
	case errors.Is(err, providers.ErrNotFound):
		return e.NotFoundError(err.Error(), nil)
	case errors.Is(err, providers.ErrInUse):
		return e.Error(http.StatusConflict, err.Error(), nil)
	case errors.Is(err, providers.ErrBadAuthField), errors.Is(err, providers.ErrBadBaseURL):
		return e.BadRequestError(err.Error(), nil)
```

> 具体写法照该函数既有的分支形状；`ErrInUse` 的 409 映射照 `credentials.ErrInUse` 的既有分支抄。

Create `hub/internal/routes/providers.go`：

```go
package routes

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// providerBody 是新建 / 更新服务配置的请求体。
type providerBody struct {
	Name       string                `json:"name"`
	Preset     string                `json:"preset"`
	BaseURL    string                `json:"base_url"`
	AuthField  string                `json:"auth_field"`
	Credential string                `json:"credential"`
	Models     []string              `json:"models"`
	Defaults   providers.ModelSlots  `json:"defaults"`
	Note       string                `json:"note"`
}

func (b providerBody) input() providers.Input {
	return providers.Input{
		Name: b.Name, Preset: b.Preset, BaseURL: b.BaseURL,
		AuthField: b.AuthField, Credential: b.Credential,
		Models: b.Models, Defaults: b.Defaults, Note: b.Note,
	}
}

func (d Deps) providerPresets(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	return e.JSON(http.StatusOK, d.Admin.ProviderPresets())
}

func (d Deps) createProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providerBody
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	id, err := d.Admin.CreateProvider(req.input())
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"id": id})
}

func (d Deps) updateProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providerBody
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	if err := d.Admin.UpdateProvider(e.Request.PathValue("id"), req.input()); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) deleteProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.DeleteProvider(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// setBinding 设置或清空草稿绑定。provider 为空串即解绑——
// 与「设置」同一个端点，不另开 DELETE：前端只有一个下拉框，
// 「选空」与「选别的」是同一个动作。
func (d Deps) setBinding(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providers.Binding
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	var b *providers.Binding
	if req.Provider != "" {
		b = &req
	}
	if err := d.Admin.SetBinding(e.Request.PathValue("id"), b); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) fixAuthField(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.FixAuthField(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}
```

`hub/internal/routes/routes.go` 的 `Register` 追加：

```go
	g.GET("/provider-presets", d.providerPresets).Bind(su)
	g.POST("/providers", d.createProvider).Bind(su)
	g.PUT("/providers/{id}", d.updateProvider).Bind(su)
	g.DELETE("/providers/{id}", d.deleteProvider).Bind(su)
	g.PUT("/config-sets/{id}/binding", d.setBinding).Bind(su)
	g.POST("/config-sets/{id}/fix-auth-field", d.fixAuthField).Bind(su)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。既有的 `fakeAdmin` 要补上六个新方法的空实现。

- [ ] **Step 5: 提交**

```bash
git add hub/ && git commit -m "feat(hub): AI 服务配置与绑定的 HTTP 端点"
```

---

### Task 5: 端到端

**Files:**
- Modify: `hub/seed_testing.go`（`SeedProvider`、`BindConfigSet`）
- Modify: `internal/testsupport/configsets.go`（转发）
- Create: `internal/testsupport/providers_test.go`

**Interfaces:**
- Consumes: `TestHub`、`TestAgent`、`agent.SyncOnce`
- Produces: `(*TestHub).SeedProvider(t, name, baseURL, key) string`、`(*TestHub).BindConfigSet(t, setID, providerID, model string) string`（返回新 revision id）

**这一条是本里程碑的验收支点**（spec §11 末段）：建 Provider → 绑定 → 发布 → 落盘验证 → 改 base_url → 重注入 → **落盘内容变了而 Revision 没变**。

- [ ] **Step 1: 写失败的测试**

Create `internal/testsupport/providers_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/internal/testsupport"
)

// 全链路：建 Provider → 绑定 → 发布 → agent 落盘 → 改 base_url →
// 重注入 → 落盘内容变了而 Revision 没变（spec §11 / §2.2）。
func TestProviderBindingEndToEnd(t *testing.T) {
	th := testsupport.NewTestHub(t)
	home := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, home)

	provID := th.SeedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")

	setID, revID := th.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{` +
			`"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
			`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
			`"ANTHROPIC_MODEL":"{{provider.model}}"}}`,
	})
	// SeedConfigSet 发的 v1 还没有绑定，绑上再发 v2。
	revID = th.BindConfigSet(t, setID, provID, "glm-5.1")
	require.NoError(t, th.Hub.AssignConfigSet(ta.MachineID, setID, "apply"))

	_, err := agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})
	require.NoError(t, err)

	settings := filepath.Join(home, ".claude", "settings.json")
	got, err := os.ReadFile(settings)
	require.NoError(t, err)
	require.Contains(t, string(got), "https://open.bigmodel.cn/api/anthropic")
	require.Contains(t, string(got), "sk-zhipu-abcdefghij")
	require.Contains(t, string(got), "glm-5.1")
	require.NotContains(t, string(got), "{{provider.")

	// blob 里必须仍然只有占位符——Revision 不可变，写进去就洗不掉。
	blob := th.RevisionBlob(t, revID, ".claude/settings.json") // 新辅助，见下
	require.Contains(t, string(blob), "{{provider.auth_token}}")
	require.NotContains(t, string(blob), "sk-zhipu")

	// —— 改 Provider 的 base_url ——
	require.NoError(t, th.Hub.UpdateProvider(provID, th.ProviderInput(t, provID,
		"https://open.bigmodel.cn/api/coding/paas/v4")))

	_, err = agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})
	require.NoError(t, err)

	got, err = os.ReadFile(settings)
	require.NoError(t, err)
	require.Contains(t, string(got), "https://open.bigmodel.cn/api/coding/paas/v4",
		"落盘内容必须跟着变")

	set, err := th.App.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, revID, set.GetString("head"),
		"改 Provider 绝不产生新 Revision（spec §2.2 的核心不变量）")

	// 也不产生漂移（spec §4.3）：state.json 记的是渲染**后**的 hash，
	// 重注入后重渲染会把它一并更新。这条是 M1 设计正确性的红利，
	// 没有一行新代码支撑它——所以必须有测试盯着。
	items, err := agent.LocalDrift(ta.Dir)
	require.NoError(t, err)
	require.Empty(t, items, "重注入之后不该有任何漂移")
}

// 低版本 agent 收不到含绑定的快照，指派置 failed 且错误信息可归因（spec §10）。
func TestOldAgentGetsAttributableFailure(t *testing.T) {
	th := testsupport.NewTestHub(t)
	home := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, home)

	// 把库里的 agent_version 改老。真实握手写的是当前版本，
	// 这里直接改记录，模拟一台还没升级的机器。
	m, err := th.App.FindRecordById("machines", ta.MachineID)
	require.NoError(t, err)
	m.Set("agent_version", "0.1.0")
	require.NoError(t, th.App.Save(m))

	provID := th.SeedProvider(t, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/anthropic", "sk-zhipu-abcdefghij")
	setID, _ := th.SeedConfigSet(t, "主力配置", map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	th.BindConfigSet(t, setID, provID, "glm-5.1")
	require.NoError(t, th.Hub.AssignConfigSet(ta.MachineID, setID, "apply"))

	_, _ = agent.SyncOnce(context.Background(), agent.SyncOptions{Dir: ta.Dir})

	th.RequireAssignmentState(t, ta.MachineID, "failed")
	recs, err := th.App.FindRecordsByFilter("assignments", "machine = {:m}", "", 1, 0,
		map[string]any{"m": ta.MachineID})
	require.NoError(t, err)
	require.Contains(t, recs[0].GetString("last_error"), "版本过低")

	// 机器上什么都不该被写。
	_, err = os.Stat(filepath.Join(home, ".claude", "settings.json"))
	require.True(t, os.IsNotExist(err))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./internal/testsupport/ -run Provider -v
```

Expected: FAIL，`SeedProvider` 未定义。

- [ ] **Step 3: 实现**

`hub/seed_testing.go` 追加（业务落在 hub 公开包，因为 `internal/testsupport` 够不到 `hub/internal/*`）：

```go
// SeedProvider 建一条凭据 + 一条 AI 服务配置，返回 provider id。
// 凭据名由 name 派生，测试里不需要关心。
func (h *Hub) SeedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()

	credName := "seed_" + strconv.Itoa(len(name)) + "_key"
	if _, err := h.creds.Create(credName, key, "由 testsupport 生成"); err != nil {
		require.NoError(t, err, "建凭据")
	}
	cred, err := h.App.FindFirstRecordByData("credentials", "name", credName)
	require.NoError(t, err)

	id, err := h.CreateProvider(providers.Input{
		Name:       name,
		BaseURL:    baseURL,
		AuthField:  providers.AuthToken,
		Credential: cred.Id,
		Models:     []string{"glm-5.1", "glm-4.7"},
	})
	require.NoError(t, err, "建服务配置")
	return id
}

// BindConfigSet 把配置集绑到给定 Provider（四槽同填 model）并发布一条新
// Revision，返回新 revision id。
func (h *Hub) BindConfigSet(t *testing.T, setID, providerID, model string) string {
	t.Helper()
	require.NoError(t, h.SetBinding(setID, &providers.Binding{
		Provider: providerID,
		Models: providers.ModelSlots{
			Main: model, Opus: model, Sonnet: model, Haiku: model,
		},
	}), "设置绑定")
	revID, err := h.PublishConfigSet(setID, "绑定服务配置")
	require.NoError(t, err, "发布")
	return revID
}

// ProviderInput 读回一条 Provider 的当前值，只把 base_url 换成新的。
// 测试里改 base_url 用它，免得每次手工拼一整个 Input。
func (h *Hub) ProviderInput(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	r, err := h.provs.Get(providerID)
	require.NoError(t, err)
	var models []string
	_ = r.UnmarshalJSONField("models", &models)
	var defaults providers.ModelSlots
	_ = r.UnmarshalJSONField("defaults", &defaults)
	return providers.Input{
		Name: r.GetString("name"), Preset: r.GetString("preset"),
		BaseURL: baseURL, AuthField: r.GetString("auth_field"),
		Credential: r.GetString("credential"), Models: models,
		Defaults: defaults, Note: r.GetString("note"),
	}
}

// RevisionBlob 读一条 Revision 里某个路径的 blob 内容。
// 用来断言「库里永远只有占位符，没有明文」。
func (h *Hub) RevisionBlob(t *testing.T, revID, path string) []byte {
	t.Helper()
	files, err := h.revs.Files(revID)
	require.NoError(t, err)
	for _, f := range files {
		if f.Path == path {
			b, err := h.blobs.Get(f.Hash)
			require.NoError(t, err)
			return b
		}
	}
	t.Fatalf("版本 %s 里没有 %s", revID, path)
	return nil
}
```

`internal/testsupport/configsets.go` 追加转发：

```go
func (h *TestHub) SeedProvider(t *testing.T, name, baseURL, key string) string {
	t.Helper()
	return h.Hub.SeedProvider(t, name, baseURL, key)
}

func (h *TestHub) BindConfigSet(t *testing.T, setID, providerID, model string) string {
	t.Helper()
	return h.Hub.BindConfigSet(t, setID, providerID, model)
}

func (h *TestHub) ProviderInput(t *testing.T, providerID, baseURL string) providers.Input {
	t.Helper()
	return h.Hub.ProviderInput(t, providerID, baseURL)
}

func (h *TestHub) RevisionBlob(t *testing.T, revID, path string) []byte {
	t.Helper()
	return h.Hub.RevisionBlob(t, revID, path)
}
```

> `internal/testsupport` 可以 import `hub/internal/providers` 吗？**不可以**——Go 的 internal 规则挡住它。因此 `ProviderInput` 的返回类型必须由 hub 公开包重新导出：在 `hub/providers.go` 里加 `type ProviderInput = providers.Input`，转发函数用 `hub.ProviderInput` 这个别名。别名而不是新结构体，避免两份字段要同步。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/ internal/testsupport/ && git commit -m "test: 服务绑定的端到端——改 Provider 不产生新版本"
```

---

## 本子计划完成后的状态

- 数据面闭环：建 Provider → 绑定 → 发布 → agent 落真实值；改 Provider → 全机队重注入且不产生新版本。
- 低版本 agent 收不到含绑定的快照，失败可归因。
- **还没有任何 UI**——那是子计划 05。
- `go test -tags=testing ./...` 全绿。
