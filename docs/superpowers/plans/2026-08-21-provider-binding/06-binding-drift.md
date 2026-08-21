# 子计划 06 · 绑定漂移的识别与禁用收编

**前置**：04（数据面）、05（前端骨架）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`DetectBindingDrift` 纯函数；`drift.upsert` 落 `binding_drift` / `binding_url`；收编在绑定漂移上被禁用（hub 与前端各一道）；卡片文案（反查第三档的兜底行为）。

**这是反查三档里的第三档骨架**（spec §6.3 末段）：都不命中时只给「恢复」「忽略」，卡片写明「无法识别这个 base_url 属于哪个平台」。**第三档是前两档的子集而不是竞品**——先做它，反查作为其上的增量，不返工。

**场景**（spec §6.1）：ssh 上某台机器，把 `ANTHROPIC_BASE_URL` 从智谱改成 Kimi。agent 的 `Restore` 试图把磁盘内容替回占位符，但磁盘上是 Kimi 的 URL、与绑定的 base_url 对不上，替不回去。于是 `DriftItem.Content` 里那一行是字面 URL，而基线里是 `{{provider.base_url}}`。

---

### Task 1: `DetectBindingDrift`

**Files:**
- Create: `hub/internal/drift/binding.go`
- Create: `hub/internal/drift/binding_test.go`

**Interfaces:**
- Consumes: `providers.HostOf`
- Produces: `DetectBindingDrift(base, cur []byte) (url string, ok bool)`

**识别逻辑完全在 hub，agent 不需要知道「绑定」这个概念**（spec §6.1）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/drift/binding_test.go`：

```go
package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
)

const bindingBase = `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`

func TestDetectBindingDriftFindsLiteralURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.moonshot.cn/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	url, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://api.moonshot.cn/anthropic", url)
}

// 基线里没有 provider.base_url → 这条漂移与绑定无关。
func TestDetectBindingDriftIgnoresUnboundBaseline(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`
	_, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.False(t, ok)
}

// 那一行没被改（还是占位符），改的是别处 → 不是绑定漂移。
func TestDetectBindingDriftIgnoresOtherChanges(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}",
    "MY_VAR": "1"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 改成了一段不像 URL 的东西 → 不乱报，走普通漂移。
func TestDetectBindingDriftIgnoresNonURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "待填",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 紧凑写法（无缩进、单行）也要认得。
func TestDetectBindingDriftHandlesCompactJSON(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://zenmux.ai/api/anthropic"}}`
	url, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://zenmux.ai/api/anthropic", url)
}

func TestDetectBindingDriftHandlesDeletedContent(t *testing.T) {
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), nil)
	require.False(t, ok)
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test ./hub/internal/drift/ -run DetectBindingDrift -v
```

Expected: FAIL，未定义。

- [ ] **Step 3: 实现**

Create `hub/internal/drift/binding.go`：

```go
package drift

import (
	"bytes"
	"strings"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// baseURLToken 是基线里 base_url 那一处的字面形态。
const baseURLToken = "{{provider.base_url}}"

// DetectBindingDrift 识别绑定漂移（M1.5 spec §6.1）：
// **基线侧是 {{provider.base_url}} 占位符、现状侧是字面值**。
// 命中时返回现状侧那段字面 URL。
//
// 做法是按行比：基线里每一处占位符都落在某一行上，取它在这一行里的前缀
// 与后缀，去现状内容里找同前缀同后缀的行——中间那段就是机器上实际写的
// URL。按行而不是按字节偏移，是因为用户多半只改了那一行，其余行还对得上。
//
// 识别逻辑完全在 hub，agent 不需要知道「绑定」这个概念。
func DetectBindingDrift(base, cur []byte) (string, bool) {
	if len(cur) == 0 || !bytes.Contains(base, []byte(baseURLToken)) {
		return "", false
	}
	curLines := strings.Split(string(cur), "\n")

	for _, bl := range strings.Split(string(base), "\n") {
		i := strings.Index(bl, baseURLToken)
		if i < 0 {
			continue
		}
		trimmedBase := strings.TrimSpace(bl)
		prefix := strings.TrimLeft(bl[:i], " \t")
		suffix := strings.TrimRight(bl[i+len(baseURLToken):], " \t\r")

		for _, cl := range curLines {
			t := strings.TrimSpace(cl)
			if t == trimmedBase {
				continue // 这一行没被改，还是占位符
			}
			if !strings.HasPrefix(t, prefix) || !strings.HasSuffix(t, suffix) {
				continue
			}
			mid := strings.TrimSuffix(t[len(prefix):], suffix)
			mid = strings.Trim(mid, "\"' \t")
			if mid == "" || strings.Contains(mid, "{{") {
				continue
			}
			// 不像 URL 就别乱报——用户可能只是把值清空了或写了句中文。
			if providers.HostOf(mid) == "" {
				continue
			}
			return mid, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/internal/drift/ -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/ && git commit -m "feat(hub): 识别绑定漂移——基线占位符 vs 现状字面值"
```

---

### Task 2: 落库 `binding_drift` 与 `binding_url`

**Files:**
- Modify: `hub/internal/drift/service.go`（`upsert`）
- Test: `hub/internal/drift/service_test.go`（追加）

**Interfaces:**
- Consumes: Task 1 的 `DetectBindingDrift`
- Produces: `drift_events.binding_drift` / `binding_url` 被填上

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/drift/service_test.go`（用该文件既有的 rig，见 `rig_test.go`）：

```go
func TestHandleReportMarksBindingDrift(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	r.assign(t, machineID, setID)

	require.NoError(t, r.svc.HandleReport(machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{
			Path: ".claude/settings.json", Kind: protocol.DriftModified,
			Content: []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`),
			Mode:    0o600,
		}},
		Final: true,
	}))

	rec := r.openDrift(t, machineID, ".claude/settings.json")
	require.True(t, rec.GetBool("binding_drift"))
	require.Equal(t, "https://api.moonshot.cn/anthropic", rec.GetString("binding_url"))
}

// 普通漂移不该被误标——误标会把「收编」置灰，用户会以为工具坏了。
func TestHandleReportLeavesNormalDriftUnmarked(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{"CLAUDE.md": "原文"})
	r.assign(t, machineID, setID)

	require.NoError(t, r.svc.HandleReport(machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{
			Path: "CLAUDE.md", Kind: protocol.DriftModified,
			Content: []byte("改过的原文"), Mode: 0o644,
		}},
		Final: true,
	}))

	rec := r.openDrift(t, machineID, "CLAUDE.md")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 用户又把那一行改回来了 → 重复上报时标记要被清掉，不能粘住。
func TestHandleReportClearsBindingDriftWhenReverted(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"1"}}`,
	})
	r.assign(t, machineID, setID)

	report := func(content string) {
		require.NoError(t, r.svc.HandleReport(machineID, protocol.DriftReport{
			Items: []protocol.DriftItem{{
				Path: ".claude/settings.json", Kind: protocol.DriftModified,
				Content: []byte(content), Mode: 0o600,
			}},
			Final: true,
		}))
	}
	report(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic","X":"1"}}`)
	require.True(t, r.openDrift(t, machineID, ".claude/settings.json").GetBool("binding_drift"))

	report(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"2"}}`)
	rec := r.openDrift(t, machineID, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 内容没上来（Truncated）时无从判断，一律不标。
func TestHandleReportDoesNotMarkTruncated(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	r.assign(t, machineID, setID)

	require.NoError(t, r.svc.HandleReport(machineID, protocol.DriftReport{
		Items: []protocol.DriftItem{{
			Path: ".claude/settings.json", Kind: protocol.DriftModified,
			Truncated: true, Mode: 0o600, // Content 故意为空
		}},
		Final: true,
	}))

	rec := r.openDrift(t, machineID, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ -run BindingDrift -v
```

Expected: FAIL，字段恒为假。

- [ ] **Step 3: 实现**

`hub/internal/drift/service.go` 的 `upsert`，在算完 `baseContent` / `curContent` 与 diff 之后、`s.d.App.Save(rec)` 之前插入：

```go
	// 绑定漂移的识别与反查素材（spec §6.1 / §6.3）。
	// 每次上报都重算：用户把那一行改回去之后标记必须跟着消失，
	// 否则「收编」会一直被置灰。
	bindingURL, isBindingDrift := "", false
	if !it.Truncated && it.Kind != protocol.DriftDeleted {
		bindingURL, isBindingDrift = DetectBindingDrift(baseContent, curContent)
	}
	rec.Set("binding_drift", isBindingDrift)
	rec.Set("binding_url", bindingURL)
```

> 注意 `curContent` 只在非 truncated、非 deleted 的分支里被赋值，因此上面的守卫与它一致。

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/ && git commit -m "feat(hub): 漂移落库标记绑定漂移与字面 base_url"
```

---

### Task 3: 收编在绑定漂移上被禁用（hub 侧）

**Files:**
- Modify: `hub/internal/drift/adopt.go`
- Modify: `hub/internal/routes/config.go`（`mapErr` 追加映射）
- Test: `hub/internal/drift/adopt_test.go`（追加）

**Interfaces:**
- Consumes: `drift_events.binding_drift`
- Produces: `drift.ErrBindingDrift`

**为什么禁用**（spec §6.2）：默认收编语义是「把机器现状写进配置集」，作用在绑定漂移上会把占位符拍平成硬编码字面值——**绑定当场死掉，而且是静悄悄地死**。下一次改 Provider 时这个配置集不再跟着走，没有任何提示。

「恢复」「忽略」照常可用。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/drift/adopt_test.go`：

```go
// seedBindingDriftEvent 造一条 binding_drift = true 的 open 漂移，返回 event id。
// 除了多 Set 两个字段，其余照该文件既有的造漂移辅助。
func seedBindingDriftEvent(t *testing.T, r *rig, setID, path, url string) string {
	t.Helper()
	id := r.seedDriftEvent(t, setID, path) // 既有辅助
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("binding_drift", true)
	rec.Set("binding_url", url)
	require.NoError(t, r.app.Save(rec))
	return id
}

func TestAdoptRefusesBindingDrift(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	id := seedBindingDriftEvent(t, r, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic")

	_, err := r.svc.Adopt([]string{id})
	require.ErrorIs(t, err, drift.ErrBindingDrift)
	require.Contains(t, err.Error(), ".claude/settings.json")

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"), "拒绝之后什么都不该被改")
}

// 恢复与忽略不受影响——它们不会破坏绑定。
func TestRestoreAndIgnoreAllowBindingDrift(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})
	a := seedBindingDriftEvent(t, r, setID, ".claude/settings.json", "https://a.test/v1")
	b := seedBindingDriftEvent(t, r, setID, ".claude/other.json", "https://b.test/v1")

	require.NoError(t, r.svc.Restore([]string{a}))
	require.NoError(t, r.svc.Ignore([]string{b}, false))
}

// 一批里混进一条绑定漂移 → 整批拒绝。
// 冲突检查已经是「要么整批成，要么什么都不动」，这条沿用同一立场。
func TestAdoptRefusesMixedBatchWithBindingDrift(t *testing.T) {
	r := newRig(t)
	setID, _ := r.seedSet(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
		"CLAUDE.md":             "原文",
	})
	normal := r.seedDriftEvent(t, setID, "CLAUDE.md")
	bound := seedBindingDriftEvent(t, r, setID, ".claude/settings.json",
		"https://api.moonshot.cn/anthropic")

	_, err := r.svc.Adopt([]string{normal, bound})
	require.ErrorIs(t, err, drift.ErrBindingDrift)

	rec, err := r.app.FindRecordById("drift_events", normal)
	require.NoError(t, err)
	require.Equal(t, "open", rec.GetString("state"), "整批拒绝，普通那条也不动")
}
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
go test -tags=testing ./hub/internal/drift/ -run Adopt -v
```

Expected: FAIL，收编成功了。

- [ ] **Step 3: 实现**

`hub/internal/drift/adopt.go` 的错误常量块追加：

```go
	// ErrBindingDrift：绑定漂移不能收编（spec §6.2）。
	//
	// 默认收编语义是「把机器现状写进配置集」，作用在绑定漂移上会把占位符
	// 拍平成硬编码字面值——绑定当场死掉，而且是静悄悄地死：下一次改
	// Provider 时这个配置集不再跟着走，没有任何提示。
	ErrBindingDrift = errors.New(
		"drift: 绑定漂移不能收编——收编会把占位符拍平成硬编码字面值，绑定会当场失效")
```

`AdoptReviewed` 的逐条检查里，紧跟 `truncated` 那条之后：

```go
		if rec.GetBool("binding_drift") {
			return nil, fmt.Errorf("%w：%s。请改用卡片上的「改绑定」，"+
				"或者「恢复」把它拉回基线", ErrBindingDrift, rec.GetString("path"))
		}
```

> 位置要紧：它必须在**任何写入之前**，与既有的冲突检查同一批——要么整批成，要么什么都不动。

`hub/internal/routes/config.go` 的 `mapErr` 追加（照 `ErrConflict` 的既有分支形状）：

```go
	case errors.Is(err, drift.ErrBindingDrift):
		return e.Error(http.StatusConflict, err.Error(), nil)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -tags=testing ./hub/... -v
```

Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/drift/ hub/internal/routes/ && git commit -m "feat(hub): 绑定漂移禁止收编"
```

---

### Task 4: 前端 —— 卡片置灰收编并说明原因

**Files:**
- Modify: `hub/internal/site/src/types/collections.ts`（`DriftEventRecord` 追加两字段）
- Modify: `hub/internal/site/src/lib/inbox.ts`（`DriftEvent` 追加两字段、`adoptBlockers`）
- Modify: `hub/internal/site/src/lib/inbox.test.ts`（追加）
- Modify: `hub/internal/site/src/components/DriftCard.tsx`
- Modify: `hub/internal/site/src/components/DriftCard.test.tsx`（追加）

**Interfaces:**
- Consumes: `drift_events.binding_drift` / `binding_url`
- Produces: 无新导出符号

**卡片要给的东西**（spec §6.2 + §6.3 第三档）：

- 「收编」勾选框置灰，hover 说明原因
- 一条说明条：「这台机器改用了别的 API 地址。收编会让服务绑定失效，因此不可用。」
- 第三档兜底文案：「无法识别 `<binding_url>` 属于哪个平台」——**这一版所有绑定漂移都走第三档文案**，反查在子计划 07 里替换它

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/site/src/lib/inbox.test.ts`：

```ts
it('绑定漂移不能收编', () => {
  const e = mkEvent({ id: 'e1', path: '.claude/settings.json', binding_drift: true })
  expect(adoptBlockers([e]).join(' ')).toContain('绑定')
})

it('普通漂移不受影响', () => {
  expect(adoptBlockers([mkEvent({ id: 'e1' })])).toEqual([])
})
```

追加到 `hub/internal/site/src/components/DriftCard.test.tsx`：

```tsx
it('绑定漂移的收编勾选框置灰并说明原因', () => {
  render(
    <DriftCard
      event={mkEvent({
        path: '.claude/settings.json',
        binding_drift: true,
        binding_url: 'https://api.moonshot.cn/anthropic',
      })}
      selected={false}
      onToggle={vi.fn()}
    />,
  )
  expect(screen.getByLabelText('.claude/settings.json')).toBeDisabled()
  expect(screen.getByText(/绑定/)).toBeInTheDocument()
  expect(screen.getByText(/https:\/\/api\.moonshot\.cn\/anthropic/)).toBeInTheDocument()
  // 第三档兜底文案
  expect(screen.getByText(/无法识别/)).toBeInTheDocument()
})

it('普通漂移的勾选框照常可用', () => {
  render(<DriftCard event={mkEvent({ path: 'CLAUDE.md' })} selected={false} onToggle={vi.fn()} />)
  expect(screen.getByLabelText('CLAUDE.md')).toBeEnabled()
})
```

- [ ] **Step 2: 运行测试确认它失败**

```bash
cd hub/internal/site && npx vitest run src/lib/inbox.test.ts src/components/DriftCard.test.tsx
```

Expected: FAIL。

- [ ] **Step 3: 实现**

`types/collections.ts` 的 `DriftEventRecord` 追加：

```ts
  /** 基线是 {{provider.*}} 占位符、机器上是字面值（M1.5 spec §6.1） */
  binding_drift: boolean
  /** 机器上那段字面 base_url，供反查用 */
  binding_url: string
```

`lib/inbox.ts` 的 `DriftEvent` 追加同样两个字段；`adoptBlockers` 追加：

```ts
  for (const e of selected.filter((x) => x.binding_drift)) {
    blockers.push(
      t`${e.path} 是服务绑定漂移：收编会把占位符拍平成硬编码地址，绑定会当场失效。请改用「改绑定」或「恢复」`,
    )
  }
```

`DriftCard.tsx`：

```tsx
  // truncated 不能收编：内容没上来，勾选也没用。
  // binding_drift 不能收编：会把占位符拍平成硬编码，绑定当场失效（spec §6.2）。
  const locked = event.truncated || event.binding_drift
```

勾选框加 `title`：

```tsx
        <input
          type="checkbox"
          className="mt-1"
          checked={selected}
          disabled={locked}
          title={
            event.binding_drift
              ? t`收编会把 {{provider.base_url}} 拍平成硬编码地址，服务绑定会当场失效`
              : undefined
          }
          onChange={onToggle}
          aria-label={event.path}
        />
```

在 `truncated` 的红条之后追加绑定漂移的说明条：

```tsx
      {event.binding_drift && (
        <div className="border-b border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">
          <p>
            <Trans>
              这台机器改用了别的 API 地址：<span className="font-mono">{event.binding_url}</span>
            </Trans>
          </p>
          <p className="mt-1 text-ink3">
            <Trans>
              无法识别这个 base_url 属于哪个平台。可以「恢复」把它拉回基线，或者「忽略」。
            </Trans>
          </p>
        </div>
      )}
```

> 第二段是**第三档的兜底文案**。子计划 07 会在前两档命中时把它替换成可操作的按钮；这一版所有绑定漂移都显示它，是有意的——第三档是前两档的子集而不是竞品（spec §6.3）。

- [ ] **Step 4: 运行测试确认通过**

```bash
cd hub/internal/site && npm test && npx tsc --noEmit
```

Expected: 全部 PASS。

- [ ] **Step 5: 抽取文案并提交**

```bash
cd hub/internal/site && npm run extract && npm run compile
```

补 `en.po` 的新条目，然后：

```bash
git add hub/internal/site/src/ && git commit -m "feat(web): 绑定漂移置灰收编并说明原因"
```

---

## 本子计划完成后的状态

- 机器上手改 base_url → 收件箱认出它是绑定漂移 → 收编被禁用（hub 与前端各一道），恢复/忽略照常。
- 第三档文案已就位，功能上是完整可用的兜底。
- **反查还没有**——那是子计划 07，纯增量，不返工本子计划的任何代码。
- `go test -tags=testing ./...` 与 `npm test` 全绿。
