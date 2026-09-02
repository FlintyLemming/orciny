# Orciny M1.8 · 本机覆盖层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把收件箱那个粒度太粗的「忽略」拆成「本机保留」（差异点级覆盖层）与「不再管这个路径」（原义退管），让一台机器能保留自己的几处改动、其余照旧跟随中台。

**Architecture:** 新集合 `machine_overrides`（一条记录 = 一个差异点）+ 新包 `hub/internal/overrides`（JSON 按 sjson 键路径、文本按行级三方合并）+ 新包 `hub/internal/merge3`（自研行级 diff3）。合并挂在 `configsync.Snapshot` 组装快照的途中，落在**占位符空间**；agent 侧一行不改，靠既有的「同 revision、内容变了 → BuildPlan 判 Overwrite」这条路生效。协议零改动。

**Tech Stack:** Go 1.26（PocketBase v0.39.9、go-difflib、tidwall/gjson+sjson、testify）、TypeScript / React 19 / Nanostores / Lingui / Vitest + Testing Library / Tailwind v4。

**Spec:** `docs/superpowers/specs/2026-09-01-machine-overrides-design.md`

## Global Constraints

- **协议零改动。** `ConfigSnapshot` / `DriftItem` / `DriftCommand` 一个字段都不加。唯一的 protocol 改动是新增字符串常量 `ReasonOverride = "override"`。
- **agent 只改一处，而且是回退。** 恢复 `applier.BuildPlan` 里被删掉的 `matchAny(snap.IgnorePaths, f.Path) → continue`（Task 1）。除此之外 `agent/` 下一行都不动。
- **覆盖层永远赢，但必须说话。** 撞车（`hub_changed` / `merge_conflict` / `path_gone` / `unmergeable`）一律取本机，只写 `attention` 提醒，apply 绝不因此卡住。
- **`attention` 只在值变化时 Save。** `Snapshot()` 每次连接、每次通知都会调，无条件写会造成每次拉取一次 DB 写。
- **不做的四件事：** JSON 数组元素级合并（数组整体当叶子）、覆盖层跨机器复制、覆盖层自动过期、二进制文件的覆盖层。
- **空串 = 不存在。** `base_value` / `mine_value` 为空串表示该键在这一侧没有；合法 JSON 值最短也是一个字符，不会歧义。
- **selector 必须转义。** 每个键段按 `\`→`\\`、`.` `*` `?`→`\X` 转义后用 `.` 拼接。不转义会静默写坏带点的键（hook matcher、MCP server 名）。
- **比较用规范化形式，存储用原样。** 撞车检测走 `canonJSON`（`json.Unmarshal` 到 `any` 再 `json.Marshal`）；`base_value` / `mine_value` 存原始字节。
- **没有 down 迁移。** 与 `006` 同理：删集合等于删用户数据。
- **中文优先。** 代码注释、错误串、UI 文案一律中文（`zh` 是源语言），`en.po` 补翻译。
- 测试命令：Go 用 `go test -tags=testing ./...`；前端 `cd hub/internal/site && npm test`。

---

### Task 1: 回退 `plan.go` 的在制品改动

**Files:**
- Modify: `agent/internal/applier/plan.go:70-75`
- Test: `agent/internal/applier/plan_test.go:131-146`

**Interfaces:**
- Consumes: 无。
- Produces: 无新符号。恢复既有语义——`snap.IgnorePaths` 命中的路径不进 plan。

**背景（spec §8.1）：** 工作区里有一处未提交的改动删掉了 BuildPlan 里的忽略判断，理由是「忽略不该阻断远端发布」。这个方向是错的：`ignore_rules` 在本设计里的语义收窄成「该路径在这台机器上退出受管」，那么「不进 plan」正是对的。保留这处改动会把「文件冻结」这个 bug 换成「本机改动被静默抹掉」这个更坏的 bug。真正的缺口在收件箱只有一个按钮，后面的 Task 补。

- [ ] **Step 1: 把 `plan_test.go` 改回原样，让它先失败**

把 `agent/internal/applier/plan_test.go` 里那个被改名的用例整体替换回：

```go
// 忽略清单里的路径不进 plan（spec §8.4）。
func TestPlanRespectsIgnorePaths(t *testing.T) {
	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":             []byte("a"),
		".claude/skills/scratch/tmp.md": []byte("b"),
	})
	snap.IgnorePaths = []string{".claude/skills/scratch/**"}

	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Len(t, p.Steps, 1)
	require.Equal(t, ".claude/CLAUDE.md", p.Steps[0].Rel)
}
```

- [ ] **Step 2: 跑测试确认它失败**

Run: `go test -tags=testing ./agent/internal/applier/ -run TestPlanRespectsIgnorePaths -v`
Expected: FAIL —— `Should have 1 item(s), but has 2`

- [ ] **Step 3: 回退 `plan.go`**

把 `agent/internal/applier/plan.go` 里那两行注释换回判断：

```go
		if matchAny(snap.IgnorePaths, f.Path) {
			continue // 用户显式忽略的，不进 plan 也不上报
		}
		wanted[f.Path] = true
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/applier/ -v`
Expected: PASS（全包）

- [ ] **Step 5: 提交**

```bash
git add agent/internal/applier/plan.go agent/internal/applier/plan_test.go
git commit -m "revert(applier): 恢复 IgnorePaths 对 plan 的阻断

ignore_rules 的语义是「该路径在这台机器上退出受管」，不进 plan 正是对的。
真正的缺口在收件箱只有一个按钮，由 M1.8 的覆盖层补（spec §8.1）。"
```

---

### Task 2: `hub/internal/merge3` —— 行级三方合并

**Files:**
- Create: `hub/internal/merge3/lines.go`
- Create: `hub/internal/merge3/merge3.go`
- Test: `hub/internal/merge3/lines_test.go`
- Test: `hub/internal/merge3/merge3_test.go`

**Interfaces:**
- Consumes: `github.com/pmezard/go-difflib/difflib`（已在 go.mod）。
- Produces:
  - `func SplitLines(b []byte) []string` —— 换行符留在行尾；`Join(SplitLines(x)) == x` 逐字节成立。
  - `func Join(lines []string) []byte`
  - `type Range struct { Start, End int }` —— **输出行**坐标的半开区间 `[Start, End)`。
  - `func Merge(base, mine, theirs []string) (out []string, conflicts []Range)` —— 冲突一律取 `mine`。

**为什么自研（spec §5）：** 需要的是最朴素的行级 diff3，算法本身一百多行；候选库要么捆着整个 git 实现，要么是多年未动的单人仓库。**不生成 `<<<<<<<` 冲突标记**：这份内容会直接落到用户的 `~/.claude` 下被 Claude Code 读取，写进标记等于交付一个坏文件。冲突的可见性由 `attention = merge_conflict` 承担。

- [ ] **Step 1: 写 `lines_test.go`（先失败）**

```go
package merge3_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

func TestSplitJoinRoundTrip(t *testing.T) {
	for _, in := range []string{
		"",
		"a\n",
		"a\nb\n",
		"a\nb",       // 无尾换行
		"\n",         // 单个空行
		"a\n\nb\n",   // 中间空行
	} {
		got := merge3.Join(merge3.SplitLines([]byte(in)))
		require.Equal(t, in, string(got), "输入 %q 必须逐字节还原", in)
	}
}

func TestSplitLinesKeepsNewline(t *testing.T) {
	require.Equal(t, []string{"a\n", "b"}, merge3.SplitLines([]byte("a\nb")))
	require.Equal(t, []string{"a\n", "b\n"}, merge3.SplitLines([]byte("a\nb\n")))
	require.Nil(t, merge3.SplitLines(nil))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/merge3/ -v`
Expected: FAIL —— `no required module provides package .../merge3`

- [ ] **Step 3: 写 `lines.go`**

```go
// Package merge3 是行级三方合并（M1.8 spec §5）。
//
// 自研而不引依赖：需要的只是最朴素的行级 diff3，算法本身一百多行，
// 而候选库要么捆着整个 git 实现，要么是多年未动的单人仓库。
// 项目已有 go-difflib，SequenceMatcher 正好提供所需的匹配块。
package merge3

import "strings"

// SplitLines 按行切分，**换行符留在行尾**。
//
// 不用 difflib.SplitLines：它会给最后一行强行补上 "\n"，
// 而这里的产物要写回用户的文件，补一个换行就是改了内容。
func SplitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(b), "\n")
	// "a\n" 会切成 ["a\n", ""]，末尾那个空串是切分产物不是内容。
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Join 是 SplitLines 的逆。Join(SplitLines(x)) 与 x 逐字节相同。
func Join(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, ""))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/merge3/ -v`
Expected: PASS

- [ ] **Step 5: 写 `merge3_test.go`（先失败）**

```go
package merge3_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

func lines(s string) []string { return merge3.SplitLines([]byte(s)) }
func text(ls []string) string { return string(merge3.Join(ls)) }

func TestMergeOnlyMineChanged(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nB\nc\n"), lines("a\nb\nc\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nB\nc\n", text(out))
}

func TestMergeOnlyTheirsChanged(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nb\nc\n"), lines("a\nb\nC\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nb\nC\n", text(out))
}

func TestMergeBothChangedSameWay(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nX\nc\n"), lines("a\nX\nc\n"))
	require.Empty(t, conflicts, "改成同样的内容不是冲突")
	require.Equal(t, "a\nX\nc\n", text(out))
}

func TestMergeBothChangedDifferentlyTakesMine(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nMINE\nc\n"), lines("a\nTHEIRS\nc\n"))
	require.Len(t, conflicts, 1)
	require.Equal(t, "a\nMINE\nc\n", text(out), "冲突一律取 mine")
	// 冲突区间用输出行坐标：第 1 行（0 起）那一行。
	require.Equal(t, merge3.Range{Start: 1, End: 2}, conflicts[0])
}

func TestMergeDisjointChangesBothApply(t *testing.T) {
	base := lines("1\n2\n3\n4\n5\n6\n7\n8\n9\n")
	mine := lines("MINE\n2\n3\n4\n5\n6\n7\n8\n9\n")
	theirs := lines("1\n2\n3\n4\n5\n6\n7\n8\nTHEIRS\n")
	out, conflicts := merge3.Merge(base, mine, theirs)
	require.Empty(t, conflicts, "隔得远的两处改动不该判冲突")
	require.Equal(t, "MINE\n2\n3\n4\n5\n6\n7\n8\nTHEIRS\n", text(out))
}

func TestMergeEmptyInputs(t *testing.T) {
	out, conflicts := merge3.Merge(nil, nil, nil)
	require.Empty(t, conflicts)
	require.Empty(t, out)

	out, conflicts = merge3.Merge(nil, lines("mine\n"), nil)
	require.Empty(t, conflicts)
	require.Equal(t, "mine\n", text(out))
}

func TestMergeNoTrailingNewline(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb"), lines("a\nB"), lines("a\nb"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nB", text(out), "不许凭空补尾换行")
}

func TestMergeDeletionOnOneSide(t *testing.T) {
	out, conflicts := merge3.Merge(
		lines("a\nb\nc\n"), lines("a\nc\n"), lines("a\nb\nc\n"))
	require.Empty(t, conflicts)
	require.Equal(t, "a\nc\n", text(out))
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/merge3/ -run TestMerge -v`
Expected: FAIL —— `undefined: merge3.Merge`

- [ ] **Step 7: 写 `merge3.go`**

```go
package merge3

import "github.com/pmezard/go-difflib/difflib"

// Range 是 out 里一段冲突的半开区间 [Start, End)。
type Range struct {
	Start int
	End   int
}

// hunk 是某一侧相对 base 的一段改动：把 base[start:end) 换成 repl。
type hunk struct {
	start, end int
	repl       []string
}

// hunksOf 求 base → other 的全部改动段（跳过相等段）。
func hunksOf(base, other []string) []hunk {
	m := difflib.NewMatcher(base, other)
	var out []hunk
	for _, op := range m.GetOpCodes() {
		if op.Tag == 'e' {
			continue
		}
		out = append(out, hunk{start: op.I1, end: op.I2, repl: other[op.J1:op.J2]})
	}
	return out
}

// applyIn 把一组 hunk 应用到 base[start:end) 上，得到该侧对这一段的说法。
func applyIn(base []string, hs []hunk, start, end int) []string {
	out := make([]string, 0, end-start)
	pos := start
	for _, h := range hs {
		if h.start > pos {
			out = append(out, base[pos:h.start]...)
		}
		out = append(out, h.repl...)
		pos = h.end
	}
	if pos < end {
		out = append(out, base[pos:end]...)
	}
	return out
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Merge 做行级三方合并。冲突一律取 mine 侧，并在 conflicts 里记下区间。
//
// 沿 base 的行号推进，把两侧的改动切成对齐的区段：
//   - 只有一侧改 → 取改的那侧
//   - 两侧改成同样的内容 → 取其一
//   - 两侧改成不同内容 → 冲突，取 mine，记一条 Range
//
// 不生成 <<<<<<< 标记：产物直接落到用户的 ~/.claude 下被 Claude Code
// 读取，写进标记等于交付一个坏文件（spec §5）。
func Merge(base, mine, theirs []string) ([]string, []Range) {
	hm := hunksOf(base, mine)
	ht := hunksOf(base, theirs)

	var out []string
	var conflicts []Range
	i, a, b := 0, 0, 0

	for a < len(hm) || b < len(ht) {
		// 组的起点 = 两侧下一个 hunk 里靠前的那个。
		groupStart := len(base)
		if a < len(hm) && hm[a].start < groupStart {
			groupStart = hm[a].start
		}
		if b < len(ht) && ht[b].start < groupStart {
			groupStart = ht[b].start
		}
		if i < groupStart {
			out = append(out, base[i:groupStart]...)
			i = groupStart
		}

		// 把与本组真正重叠的 hunk 都收进来。
		// 判据是严格重叠（h.start < groupEnd），相邻但不重叠的两处改动
		// 不该被并成一组——否则「两侧在相邻行各改一处」会误判冲突。
		// h.start == groupStart 是纯插入（start == end）的特例，必须收进来。
		groupEnd := groupStart
		ma, mb := a, b
		for {
			grew := false
			for ma < len(hm) && (hm[ma].start < groupEnd || hm[ma].start == groupStart) {
				if hm[ma].end > groupEnd {
					groupEnd = hm[ma].end
				}
				ma++
				grew = true
			}
			for mb < len(ht) && (ht[mb].start < groupEnd || ht[mb].start == groupStart) {
				if ht[mb].end > groupEnd {
					groupEnd = ht[mb].end
				}
				mb++
				grew = true
			}
			if !grew {
				break
			}
		}

		mineVer := applyIn(base, hm[a:ma], groupStart, groupEnd)
		theirsVer := applyIn(base, ht[b:mb], groupStart, groupEnd)

		switch {
		case ma == a: // 只有 theirs 改了这一段
			out = append(out, theirsVer...)
		case mb == b: // 只有 mine 改了这一段
			out = append(out, mineVer...)
		case sameLines(mineVer, theirsVer):
			out = append(out, mineVer...)
		default:
			start := len(out)
			out = append(out, mineVer...)
			conflicts = append(conflicts, Range{Start: start, End: len(out)})
		}

		a, b, i = ma, mb, groupEnd
	}

	if i < len(base) {
		out = append(out, base[i:]...)
	}
	return out, conflicts
}
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/merge3/ -v`
Expected: PASS（全部 9 个用例）

- [ ] **Step 9: 提交**

```bash
git add hub/internal/merge3
git commit -m "feat(merge3): 行级三方合并，冲突取本机侧

自研而不引依赖：算法一百多行，候选库要么捆着整个 git 实现，
要么是多年未动的单人仓库。不生成冲突标记——产物直接落用户磁盘
（M1.8 spec §5）。"
```

---

### Task 3: 迁移 `007_machine_overrides`

**Files:**
- Create: `hub/internal/migrations/007_machine_overrides.go`
- Test: `hub/internal/migrations/migrations_test.go`（追加用例，文件已存在）

**Interfaces:**
- Consumes: PocketBase `core` 的字段类型；已存在的 `machines` / `blobs` / `revisions` / `drift_events` collection。
- Produces:
  - collection `machine_overrides`，字段：`machine` `path` `kind` `selector` `base_value` `mine_value` `base_blob` `mine_blob` `attention` `shadowed_value` `shadowed_blob` `shadowed_rev` `origin_drift` `note` `created` `updated`
  - 唯一索引 `idx_overrides_machine_path_sel` on `(machine, path, selector)`；部分索引 `idx_overrides_attention` on `attention` where `attention != ''`
  - `drift_events.state` 的 `Values` 追加 `"overridden"`
  - `func Down007(app core.App) error`（供测试断言它拒绝回滚，与 `Down006` 同一惯例）

**不迁移存量 `ignore_rules`（spec §3）。** 已有规则的语义没变（路径退管），它们本来就是路径级的，没有信息可以升级成覆盖层——hub 不知道当初那条漂移的差异点是什么，替用户猜是自曝其短。存量规则原样留着，用户在新 UI 上看到的是「不再管这个路径」，与他当初实际得到的效果一致。

- [ ] **Step 1: 写迁移测试（先失败）**

追加到 `hub/internal/migrations/migrations_test.go`：

```go
func TestMachineOverridesCollection(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machine_overrides")
	require.NoError(t, err)

	for _, f := range []string{
		"machine", "path", "kind", "selector", "base_value", "mine_value",
		"base_blob", "mine_blob", "attention", "shadowed_value",
		"shadowed_blob", "shadowed_rev", "origin_drift", "note",
		"created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "machine_overrides.%s 缺失", f)
	}

	kind, ok := c.Fields.GetByName("kind").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"json_key", "text"}, kind.Values)

	att, ok := c.Fields.GetByName("attention").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t,
		[]string{"hub_changed", "merge_conflict", "path_gone", "unmergeable"},
		att.Values)

	// API rule 一律 nil —— 仅 superuser 可访问，与其余 collection 一致。
	require.Nil(t, c.ListRule)
	require.Nil(t, c.ViewRule)
	require.Nil(t, c.CreateRule)
	require.Nil(t, c.UpdateRule)
	require.Nil(t, c.DeleteRule)
}

func TestMachineOverridesUniqueOnMachinePathSelector(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machine_overrides")
	require.NoError(t, err)

	var unique, attention bool
	for _, idx := range c.Indexes {
		if strings.Contains(idx, "idx_overrides_machine_path_sel") {
			require.Contains(t, idx, "UNIQUE")
			unique = true
		}
		if strings.Contains(idx, "idx_overrides_attention") {
			require.Contains(t, idx, "attention != ''")
			attention = true
		}
	}
	require.True(t, unique, "缺唯一索引 idx_overrides_machine_path_sel")
	require.True(t, attention, "缺部分索引 idx_overrides_attention")
}

func TestDriftStateHasOverridden(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	state, ok := c.Fields.GetByName("state").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{
		"open", "adopted", "restored", "ignored", "superseded", "overridden",
	}, state.Values)
}

// 存量忽略规则原样留着，不被 007 动过（spec §3 第 3 条）。
func TestMigration007LeavesIgnoreRulesAlone(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("ignore_rules")
	require.NoError(t, err)
	rec := core.NewRecord(c)
	rec.Set("path", ".claude/settings.json")
	require.NoError(t, app.Save(rec))

	// 再跑一次迁移必须幂等，且不碰这条规则。
	require.NoError(t, migrations.Up007(app))

	got, err := app.FindRecordById("ignore_rules", rec.Id)
	require.NoError(t, err)
	require.Equal(t, ".claude/settings.json", got.GetString("path"))
}

func TestDown007Refuses(t *testing.T) {
	app := newApp(t)
	require.Error(t, migrations.Down007(app), "删集合等于删用户的覆盖层，必须拒绝")
}
```

在该文件的 import 块里补 `"strings"`（若尚未 import）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/migrations/ -run 'TestMachineOverrides|TestDriftStateHasOverridden|TestMigration007|TestDown007' -v`
Expected: FAIL —— `undefined: migrations.Up007` 以及 `machine_overrides` 不存在

- [ ] **Step 3: 写 `007_machine_overrides.go`**

```go
package migrations

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up007, down007, "007_machine_overrides.go")
}

// Up007 供测试直接调用，断言它幂等且不碰存量 ignore_rules。
func Up007(app core.App) error { return up007(app) }

// Down007 供测试断言「它确实拒绝回滚」。
func Down007(app core.App) error { return down007(app) }

// up007 建 machine_overrides 并给 drift_events.state 加 overridden
// （M1.8 spec §3）。**不迁移存量 ignore_rules**：它们的语义没变
// （路径退管），也没有信息可以升级成覆盖层。
func up007(app core.App) error {
	if err := createMachineOverrides(app); err != nil {
		return err
	}
	return addOverriddenState(app)
}

func createMachineOverrides(app core.App) error {
	if _, err := app.FindCollectionByNameOrId("machine_overrides"); err == nil {
		return nil // 已建过，幂等返回
	}

	machines, err := app.FindCollectionByNameOrId("machines")
	if err != nil {
		return fmt.Errorf("007: 找不到 machines: %w", err)
	}
	blobs, err := app.FindCollectionByNameOrId("blobs")
	if err != nil {
		return fmt.Errorf("007: 找不到 blobs: %w", err)
	}
	revs, err := app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return fmt.Errorf("007: 找不到 revisions: %w", err)
	}
	drifts, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return fmt.Errorf("007: 找不到 drift_events: %w", err)
	}

	// 一条记录 = 一个差异点，不是一个文件（spec §2.1）。
	overrides := core.NewBaseCollection("machine_overrides")
	overrides.Fields.Add(
		&core.RelationField{Name: "machine", Required: true,
			CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.SelectField{Name: "kind", MaxSelect: 1, Required: true,
			Values: []string{"json_key", "text"}},

		// json_key：sjson 键路径（已转义）。text 恒为空串。
		&core.TextField{Name: "selector", Max: 512},

		// json_key 的两侧：原始 JSON 片段。空串 = 该键在这一侧不存在。
		&core.TextField{Name: "base_value", Max: 65536},
		&core.TextField{Name: "mine_value", Max: 65536},

		// text 的两侧：全文 blob。
		&core.RelationField{Name: "base_blob", CollectionId: blobs.Id, MaxSelect: 1},
		&core.RelationField{Name: "mine_blob", CollectionId: blobs.Id, MaxSelect: 1},

		// 需要用户看一眼的状态。空 = 一切正常（spec §4.5）。
		&core.SelectField{Name: "attention", MaxSelect: 1, Values: []string{
			"hub_changed", "merge_conflict", "path_gone", "unmergeable",
		}},
		&core.TextField{Name: "shadowed_value", Max: 65536},
		&core.RelationField{Name: "shadowed_blob", CollectionId: blobs.Id, MaxSelect: 1},
		&core.RelationField{Name: "shadowed_rev", CollectionId: revs.Id, MaxSelect: 1},

		&core.RelationField{Name: "origin_drift", CollectionId: drifts.Id, MaxSelect: 1},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// (machine, path, selector) 唯一：同一处差异点只有一条记录。
	// text 的 selector 恒为空串，因此一个路径上只能有一条 text 覆盖层。
	overrides.AddIndex("idx_overrides_machine_path_sel", true, "machine, path, selector", "")
	overrides.AddIndex("idx_overrides_attention", false, "attention", "attention != ''")
	if err := app.Save(overrides); err != nil {
		return fmt.Errorf("007: 建 machine_overrides: %w", err)
	}
	return nil
}

// addOverriddenState 给 drift_events.state 追加 overridden（spec §2.2）。
// ignored 保留原义（路径退管），两者在收件箱的筛选器里是两个不同的去向。
func addOverriddenState(app core.App) error {
	c, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return fmt.Errorf("007: 找不到 drift_events: %w", err)
	}
	state, ok := c.Fields.GetByName("state").(*core.SelectField)
	if !ok {
		return errors.New("007: drift_events.state 不是 select 字段")
	}
	for _, v := range state.Values {
		if v == "overridden" {
			return nil // 幂等
		}
	}
	state.Values = append(state.Values, "overridden")
	// 按同名覆盖回去，确保改动被序列化进 collection。
	c.Fields.Add(state)
	if err := app.Save(c); err != nil {
		return fmt.Errorf("007: 给 drift_events.state 加 overridden: %w", err)
	}
	return nil
}

// 没有 down 迁移，与 006 同理：删掉集合等于删掉用户的覆盖层，
// 而这是**用户数据**（spec §3）。
func down007(_ core.App) error {
	return errors.New("007: 不支持回滚，删集合等于删用户的覆盖层")
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/migrations/ -v`
Expected: PASS（含既有的 001–006 用例）

- [ ] **Step 5: 提交**

```bash
git add hub/internal/migrations/007_machine_overrides.go hub/internal/migrations/migrations_test.go
git commit -m "feat(migrations): 007 建 machine_overrides、drift_events.state 加 overridden

一条记录 = 一个差异点。不迁移存量 ignore_rules——它们的语义没变，
也没有信息可以升级成覆盖层（M1.8 spec §2-§3）。"
```

---

### Task 4: `hub/internal/overrides` 的纯函数层 —— `Split` / `Hunks` / `SynthesizeMine`

**Files:**
- Create: `hub/internal/overrides/split.go`
- Create: `hub/internal/overrides/synth.go`
- Test: `hub/internal/overrides/split_test.go`
- Test: `hub/internal/overrides/synth_test.go`

**Interfaces:**
- Consumes: `hub/internal/merge3`（Task 2 的 `SplitLines` / `Join`）、`go-difflib`、`encoding/json`。
- Produces:
  - `type Point struct { Selector, BaseValue, MineValue string }`
  - `func Split(base, mine []byte) ([]Point, error)` —— 按 selector 字典序稳定排列
  - `func IsJSONObject(b []byte) bool`
  - `func EscapeSeg(s string) string`
  - `func CanonJSON(raw []byte) (string, error)`
  - `type Hunk struct { BaseStart, BaseEnd, CurStart, CurEnd int }`
  - `func Hunks(base, cur []byte) []Hunk`
  - `func SynthesizeMine(base, cur []byte, keep []int) []byte`
  - `var ErrNotJSONObject = errors.New("overrides: 两侧必须都是 JSON 对象才能按键切分")`

**三条取舍（spec §4.1）：**
1. **数组当叶子。** 按下标定位的 selector 在数组增删时会指向错误的元素——静默写坏用户配置的经典路子。
2. **空串 = 不存在。** 合法 JSON 值最短也是一个字符，空串不可能是任何值的序列化结果，因此不需要额外的 `absent` 布尔。
3. **比较用 `CanonJSON`，存储用原样。** 两个 revision 之间同一个值可能因重新缩进而字节不同、语义相同；比原始字节会误报撞车。存储保留原样，因为写回文件时要保留用户的格式。

**`Hunks` 与 `drift.UnifiedDiff` 的一一对应（spec §4.2）：** 两者都用 `go-difflib` 的 3 行上下文分组（`GetGroupedOpCodes(3)` 对应 unified diff 的 `@@` 块），所以第 k 个 `@@` 就是第 k 个 `Hunk`。前端勾的是 `@@` 块下标，后端按同一下标取 hunk——这条对应关系由 Step 5 的测试钉住。

- [ ] **Step 1: 写 `split_test.go`（先失败）**

```go
package overrides_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/overrides"
)

func TestSplitDescendsIntoObjects(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"env":{"A":"1","B":"2"},"n":1}`),
		[]byte(`{"env":{"A":"9","B":"2"},"n":1}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "env.A", BaseValue: `"1"`, MineValue: `"9"`},
	}, pts)
}

func TestSplitTreatsArrayAsLeaf(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"allow":["a","b"]}`),
		[]byte(`{"allow":["a","b","c"]}`))
	require.NoError(t, err)
	require.Len(t, pts, 1)
	require.Equal(t, "allow", pts[0].Selector)
	require.Equal(t, `["a","b","c"]`, pts[0].MineValue)
}

func TestSplitTypeMismatchIsLeaf(t *testing.T) {
	pts, err := overrides.Split([]byte(`{"x":{"a":1}}`), []byte(`{"x":"字符串"}`))
	require.NoError(t, err)
	require.Len(t, pts, 1)
	require.Equal(t, "x", pts[0].Selector)
}

func TestSplitMissingKeyOnEitherSide(t *testing.T) {
	pts, err := overrides.Split([]byte(`{"a":1}`), []byte(`{"a":1,"b":2}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "b", BaseValue: "", MineValue: "2"},
	}, pts, "空串 = 该键在这一侧不存在")

	pts, err = overrides.Split([]byte(`{"a":1,"b":2}`), []byte(`{"a":1}`))
	require.NoError(t, err)
	require.Equal(t, []overrides.Point{
		{Selector: "b", BaseValue: "2", MineValue: ""},
	}, pts)
}

// 带 . * ? \ 的键是真实存在的（hook matcher、MCP server 名）。
// 不转义会让覆盖层写到错误的位置——静默写坏配置（spec §4.1）。
func TestSplitEscapesSelectorSegments(t *testing.T) {
	pts, err := overrides.Split(
		[]byte(`{"hooks":{"a.b":1,"c*d":1,"e?f":1,"g\\h":1}}`),
		[]byte(`{"hooks":{"a.b":2,"c*d":2,"e?f":2,"g\\h":2}}`))
	require.NoError(t, err)
	got := make([]string, 0, len(pts))
	for _, p := range pts {
		got = append(got, p.Selector)
	}
	require.ElementsMatch(t, []string{
		`hooks.a\.b`, `hooks.c\*d`, `hooks.e\?f`, `hooks.g\\h`,
	}, got)
}

func TestSplitIgnoresReindentation(t *testing.T) {
	pts, err := overrides.Split(
		[]byte("{\n  \"a\": {\n    \"b\": 1\n  }\n}"),
		[]byte(`{"a":{"b":1}}`))
	require.NoError(t, err)
	require.Empty(t, pts, "只是重新缩进，不是差异点")
}

func TestSplitStableOrder(t *testing.T) {
	pts, err := overrides.Split([]byte(`{}`), []byte(`{"z":1,"a":1,"m":1}`))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "m", "z"},
		[]string{pts[0].Selector, pts[1].Selector, pts[2].Selector})
}

func TestSplitRejectsNonObject(t *testing.T) {
	_, err := overrides.Split([]byte(`[1,2]`), []byte(`[1,2,3]`))
	require.ErrorIs(t, err, overrides.ErrNotJSONObject)
}

func TestIsJSONObject(t *testing.T) {
	require.True(t, overrides.IsJSONObject([]byte(`{"a":1}`)))
	require.True(t, overrides.IsJSONObject([]byte(" \n{}\t")))
	require.False(t, overrides.IsJSONObject([]byte(`[1]`)))
	require.False(t, overrides.IsJSONObject([]byte(`# 一份 Markdown`)))
	require.False(t, overrides.IsJSONObject(nil))
}

func TestCanonJSON(t *testing.T) {
	a, err := overrides.CanonJSON([]byte("{\n \"b\":1, \"a\":2}"))
	require.NoError(t, err)
	b, err := overrides.CanonJSON([]byte(`{"a":2,"b":1}`))
	require.NoError(t, err)
	require.Equal(t, a, b, "键序与缩进不该造成差异")

	_, err = overrides.CanonJSON([]byte(`{坏的`))
	require.Error(t, err)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/overrides/ -v`
Expected: FAIL —— `no required module provides package .../overrides`

- [ ] **Step 3: 写 `split.go`**

```go
// Package overrides 是本机覆盖层：差异点的切分、合并与撞车检测
// （M1.8 spec §4）。
//
// 一条 machine_overrides 记录 = 一个差异点。JSON 按 sjson 键路径定位，
// 文本按整份 base/mine 全文加行级三方合并。合并挂在
// configsync.Snapshot 组装快照的途中，落在**占位符空间**——
// agent 侧一行不改（spec §4.4）。
package overrides

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// ErrNotJSONObject：只有两侧都是 JSON 对象才走 json_key 一路。
var ErrNotJSONObject = errors.New("overrides: 两侧必须都是 JSON 对象才能按键切分")

// Point 是一个差异点。BaseValue / MineValue 是**原始** JSON 片段，
// 空串表示该键在这一侧不存在（spec §4.1）。
type Point struct {
	Selector  string
	BaseValue string
	MineValue string
}

// IsJSONObject 判断内容是否是一个合法的 JSON 对象。
// kind 由它决定：两侧都是对象 → json_key，否则 → text。
func IsJSONObject(b []byte) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(b, &m) == nil && m != nil
}

// EscapeSeg 转义一个键段，使它能安全地拼进 gjson / sjson 的路径。
//
// 路径语法里 . 是分隔符、* ? 是通配符、\ 是转义符。settings.json 里
// 带点的键是真实存在的（hook matcher、MCP server 名），不转义会让
// 覆盖层写到错误的位置——静默写坏配置（spec §4.1）。
func EscapeSeg(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '.', '*', '?':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// CanonJSON 返回规范化形式，**只用于比较**。
//
// 两个 revision 之间的同一个值可能因为重新缩进而原始字节不同、语义相同；
// 撞车检测若比原始字节会误报。Go 对 map[string]any 的键排序是稳定的，
// 因此 Marshal 的结果可直接用于相等判断（spec §4.1）。
func CanonJSON(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Split 递归下降比较两棵 JSON 树，产出差异点（spec §4.1）。
//
// 规则：两侧都是 object → 逐键下降；其余（数组、标量、类型不同）→ 叶子。
// 数组整体当叶子是刻意的：按下标定位的 selector 在数组增删时会指向
// 错误的元素，那是静默写坏用户配置的经典路子（spec §1.4）。
func Split(base, mine []byte) ([]Point, error) {
	b, okB := asObject(base)
	m, okM := asObject(mine)
	if !okB || !okM {
		return nil, ErrNotJSONObject
	}
	var out []Point
	splitObj("", b, m, &out)
	return out, nil
}

func asObject(raw []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

func splitObj(prefix string, base, mine map[string]json.RawMessage, out *[]Point) {
	seen := map[string]bool{}
	keys := make([]string, 0, len(base)+len(mine))
	for k := range base {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range mine {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys) // 稳定顺序，UI 与测试都靠它

	for _, k := range keys {
		sel := EscapeSeg(k)
		if prefix != "" {
			sel = prefix + "." + sel
		}
		bv, bok := base[k]
		mv, mok := mine[k]

		if bok && mok {
			bo, bIsObj := asObject(bv)
			mo, mIsObj := asObject(mv)
			if bIsObj && mIsObj {
				splitObj(sel, bo, mo, out)
				continue
			}
			if sameJSON(bv, mv) {
				continue
			}
		}

		*out = append(*out, Point{
			Selector:  sel,
			BaseValue: rawOf(bok, bv),
			MineValue: rawOf(mok, mv),
		})
	}
}

func rawOf(ok bool, raw json.RawMessage) string {
	if !ok {
		return "" // 空串 = 该键在这一侧不存在
	}
	return string(raw)
}

func sameJSON(a, b json.RawMessage) bool {
	ca, errA := CanonJSON(a)
	cb, errB := CanonJSON(b)
	if errA != nil || errB != nil {
		return string(a) == string(b)
	}
	return ca == cb
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/overrides/ -v`
Expected: PASS

- [ ] **Step 5: 写 `synth_test.go`（先失败）**

```go
package overrides_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
)

const (
	synthBase = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\n"
	synthCur  = "L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nL16\n"
)

func TestHunksMatchUnifiedDiffHunkCount(t *testing.T) {
	// 前端勾的是 unified diff 的 @@ 块下标，后端按同一下标取 hunk。
	// 这条一一对应关系是「勾选」能落地的前提（spec §4.2）。
	hs := overrides.Hunks([]byte(synthBase), []byte(synthCur))
	d := drift.UnifiedDiff("f", []byte(synthBase), []byte(synthCur))
	require.Equal(t, strings.Count(d, "\n@@"), len(hs))
	require.Len(t, hs, 2, "首尾各一处改动、中间隔得够远，应是两个 hunk")
}

func TestSynthesizeMineKeepAllEqualsCur(t *testing.T) {
	hs := overrides.Hunks([]byte(synthBase), []byte(synthCur))
	all := make([]int, len(hs))
	for i := range hs {
		all[i] = i
	}
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), all)
	require.Equal(t, synthCur, string(got))
}

func TestSynthesizeMineKeepNoneEqualsBase(t *testing.T) {
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), nil)
	require.Equal(t, synthBase, string(got))
}

func TestSynthesizeMineDropsOneHunk(t *testing.T) {
	// 只留第一处改动，第二处退回基线。
	got := overrides.SynthesizeMine([]byte(synthBase), []byte(synthCur), []int{0})
	require.True(t, strings.HasPrefix(string(got), "L1\n"))
	require.True(t, strings.HasSuffix(string(got), "l16\n"))
}

func TestSynthesizeMineNoTrailingNewline(t *testing.T) {
	got := overrides.SynthesizeMine([]byte("a\nb"), []byte("a\nB"), []int{0})
	require.Equal(t, "a\nB", string(got), "不许凭空补尾换行")
}

func TestHunksOnIdenticalContent(t *testing.T) {
	require.Empty(t, overrides.Hunks([]byte("a\n"), []byte("a\n")))
}
```

- [ ] **Step 6: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/overrides/ -run 'TestHunks|TestSynthesize' -v`
Expected: FAIL —— `undefined: overrides.Hunks`

- [ ] **Step 7: 写 `synth.go`**

```go
package overrides

import (
	"github.com/pmezard/go-difflib/difflib"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
)

// Hunk 是 base → cur 的一段改动，含 3 行上下文。
// 下标与 drift.UnifiedDiff 里的 @@ 块一一对应（spec §4.2）——
// 前端勾的是 @@ 块下标，SynthesizeMine 按同一下标取舍。
type Hunk struct {
	BaseStart, BaseEnd int
	CurStart, CurEnd   int
}

// Hunks 用与 drift.UnifiedDiff 同一套分组（3 行上下文）切出 hunk。
//
// 每次新建 Matcher：difflib 的 GetGroupedOpCodes 会就地改写自己缓存的
// opcodes，复用同一个 Matcher 会拿到被改过的数据。
func Hunks(base, cur []byte) []Hunk {
	bl := merge3.SplitLines(base)
	cl := merge3.SplitLines(cur)
	m := difflib.NewMatcher(bl, cl)

	var out []Hunk
	for _, g := range m.GetGroupedOpCodes(3) {
		if len(g) == 0 {
			continue
		}
		out = append(out, Hunk{
			BaseStart: g[0].I1, BaseEnd: g[len(g)-1].I2,
			CurStart: g[0].J1, CurEnd: g[len(g)-1].J2,
		})
	}
	return out
}

// SynthesizeMine 合成「基线 + 被勾选的那些 hunk」（spec §4.2）。
//
// keep 是被勾选的 hunk 下标。keep 为全集时结果 == cur；keep 为空时
// 结果 == base。勾选因此塌缩进数据本身，后续所有逻辑只面对 base / mine
// 两份全文，不需要第二份真相。
func SynthesizeMine(base, cur []byte, keep []int) []byte {
	bl := merge3.SplitLines(base)
	cl := merge3.SplitLines(cur)
	hs := Hunks(base, cur)

	want := make(map[int]bool, len(keep))
	for _, i := range keep {
		want[i] = true
	}

	out := make([]string, 0, len(cl))
	pos := 0 // base 行号
	for i, h := range hs {
		if h.BaseStart > pos {
			out = append(out, bl[pos:h.BaseStart]...)
		}
		if want[i] {
			out = append(out, cl[h.CurStart:h.CurEnd]...)
		} else {
			out = append(out, bl[h.BaseStart:h.BaseEnd]...)
		}
		pos = h.BaseEnd
	}
	if pos < len(bl) {
		out = append(out, bl[pos:]...)
	}
	return merge3.Join(out)
}
```

- [ ] **Step 8: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/overrides/ -v`
Expected: PASS（全部用例）

- [ ] **Step 9: 提交**

```bash
git add hub/internal/overrides
git commit -m "feat(overrides): 差异点切分与勾选合成

Split 递归比较 JSON 树（数组当叶子、selector 转义、比较用规范化形式）；
SynthesizeMine 把「勾选」塌缩进 base/mine 两份全文，hunk 下标与
unified diff 的 @@ 块一一对应（M1.8 spec §4.1-§4.2）。"
```

---

### Task 5: `overrides.Service` —— 记录读写、`Apply` 与撞车检测

**Files:**
- Create: `hub/internal/overrides/service.go`
- Create: `hub/internal/overrides/apply.go`
- Test: `hub/internal/overrides/apply_test.go`

**Interfaces:**
- Consumes: `Point` / `Hunks` / `SynthesizeMine` / `CanonJSON` / `IsJSONObject`（Task 4）、`merge3.Merge`（Task 2）、`hub/internal/blobs`、`hub/internal/events`、`protocol.FileEntry`。
- Produces:
  - `type Service struct{ … }`；`func NewService(app core.App, b *blobs.Store, ev *events.Writer, log *slog.Logger) *Service`
  - `func (s *Service) Apply(machineID, revID string, files []protocol.FileEntry) ([]protocol.FileEntry, error)`
  - `func (s *Service) ForMachine(machineID string) ([]*core.Record, error)`
  - `func (s *Service) ForPath(machineID, path string) ([]*core.Record, error)`
  - `func (s *Service) CreateJSON(machineID, path, driftID string, points []Point) error`
  - `func (s *Service) CreateText(machineID, path, driftID string, base, mine []byte) error`
  - `func (s *Service) DropPath(machineID, path string) (int, error)` —— 删该路径全部覆盖层，返回删掉的条数
  - `func (s *Service) DropOverlapping(machineID, path, selector string) (int, error)` —— 删与 selector 构成前缀关系的记录
  - `func (s *Service) Delete(id string) (machineID string, err error)`
  - `func (s *Service) Keep(id string) (machineID string, err error)` —— 清 `attention` 与 `shadowed_*`
- 事件常量（本任务顺带加进 `hub/internal/events/writer.go`）：
  - `KindOverrideCreated = "override.created"`
  - `KindOverrideDropped = "override.dropped"`
  - `KindOverrideReplaced = "override.replaced"`
  - `KindOverrideKept = "override.kept"`

**合并落在占位符空间（spec §4.3）。** `Apply` 拿到的 `files` 是 head revision 的条目，其 blob 内容里 `{{provider.*}}` / `{{var.*}}` 原样未渲染；agent 上报的漂移内容经 `render.RestoreWithBase` 还原成占位符后存进 `drift_events.current_blob`。两侧同处一个空间才能合并，渲染仍发生在 agent 侧、合并之后——所以覆盖层永远不会把 API key 明文带进 hub 库。

**四种撞车一律取本机（spec §4.5）：**

| 情形 | `attention` | 记什么 |
|---|---|---|
| `CanonJSON(基线在 selector 上的值) != CanonJSON(base_value)` | `hub_changed` | `shadowed_value` = 基线新值，`shadowed_rev` = head |
| 文本三方合并出现冲突块 | `merge_conflict` | `shadowed_blob` = 基线新全文 |
| 该路径不在本次 `files` 里 | `path_gone` | — |
| 基线在该路径上不是合法 JSON 对象（`kind = json_key`） | `unmergeable` | — |

`unmergeable` 与 `path_gone` 无从取本机，等于放弃本轮合并、下发原基线。apply 永远不会因为撞车卡住。

**写入要防放大。** `Snapshot()` 每次连接、每次通知都会调；只在计算结果与库里已存的值**不同**时才 Save。合并本身是纯函数，`blobs.Put` 内容寻址天然幂等，因此不做任何缓存——这些配置文件都是 KB 级，算一遍比维护缓存一致性便宜。

- [ ] **Step 1: 写 `apply_test.go`（先失败）**

```go
package overrides_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
	"github.com/FlintyLemming/orciny/protocol"
)

type rig struct {
	app       *tests.TestApp
	blobs     *blobs.Store
	svc       *overrides.Service
	machineID string
	saves     *int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	saves := 0
	app.OnRecordAfterUpdateSuccess("machine_overrides").BindFunc(func(e *core.RecordEvent) error {
		saves++
		return e.Next()
	})

	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", "fp-ov-1")
	m.Set("pub_key", "pk-ov-1")
	m.Set("status", "online")
	require.NoError(t, app.Save(m))

	return &rig{
		app: app, blobs: b, machineID: m.Id, saves: &saves,
		svc: overrides.NewService(app, b, events.NewWriter(app), nil),
	}
}

// entry 把一份内容落成 blob 并返回对应的 FileEntry。
func (r *rig) entry(t *testing.T, path string, content []byte) protocol.FileEntry {
	t.Helper()
	h, err := r.blobs.Put(content)
	require.NoError(t, err)
	return protocol.FileEntry{
		Path: path, Hash: h, Size: uint32(len(content)), Mode: 0o644,
	}
}

// merged 跑一次 Apply 并取回指定路径的内容。
func (r *rig) merged(t *testing.T, path string, files []protocol.FileEntry) string {
	t.Helper()
	out, err := r.svc.Apply(r.machineID, "rev-x", files)
	require.NoError(t, err)
	for _, f := range out {
		if f.Path == path {
			b, err := r.blobs.Get(f.Hash)
			require.NoError(t, err)
			require.Equal(t, uint32(len(b)), f.Size, "Size 必须跟着 Hash 一起更新")
			require.Equal(t, uint32(0o644), f.Mode, "Mode 不该被动过")
			return string(b)
		}
	}
	t.Fatalf("Apply 的结果里没有 %s", path)
	return ""
}

func (r *rig) only(t *testing.T) *core.Record {
	t.Helper()
	recs, err := r.svc.ForMachine(r.machineID)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	return recs[0]
}

const p = ".claude/settings.json"

func TestApplyNoOverridesReturnsInput(t *testing.T) {
	r := newRig(t)
	in := []protocol.FileEntry{r.entry(t, p, []byte(`{"a":1}`))}
	out, err := r.svc.Apply(r.machineID, "rev-x", in)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestApplySingleJSONPoint(t *testing.T) {
	r := newRig(t)
	base := []byte(`{"env":{"A":"base"},"keep":1}`)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	got := r.merged(t, p, []protocol.FileEntry{r.entry(t, p, base)})
	require.JSONEq(t, `{"env":{"A":"mine"},"keep":1}`, got)
}

func TestApplyDeletesKeyWhenMineIsAbsent(t *testing.T) {
	r := newRig(t)
	base := []byte(`{"a":1,"b":2}`)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "b", BaseValue: "2", MineValue: ""},
	}))
	got := r.merged(t, p, []protocol.FileEntry{r.entry(t, p, base)})
	require.JSONEq(t, `{"a":1}`, got)
}

func TestApplyMultiplePointsAndEscapedSelector(t *testing.T) {
	r := newRig(t)
	base := []byte(`{"env":{"A":"base","B":"base"},"hooks":{"a.b":1}}`)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"x"`},
		{Selector: `hooks.a\.b`, BaseValue: "1", MineValue: "9"},
	}))
	got := r.merged(t, p, []protocol.FileEntry{r.entry(t, p, base)})
	require.JSONEq(t, `{"env":{"A":"x","B":"base"},"hooks":{"a.b":9}}`, got)
}

// 中台改了同一处 → 取本机，但标 hub_changed 并记下被挡下的值。
func TestApplyHubChangedTakesMineAndFlags(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	newBase := []byte(`{"env":{"A":"中台的新值"}}`)
	got := r.merged(t, p, []protocol.FileEntry{r.entry(t, p, newBase)})
	require.JSONEq(t, `{"env":{"A":"mine"}}`, got, "覆盖层永远赢")

	rec := r.only(t)
	require.Equal(t, "hub_changed", rec.GetString("attention"))
	require.JSONEq(t, `"中台的新值"`, rec.GetString("shadowed_value"))
	require.Equal(t, "rev-x", rec.GetString("shadowed_rev"))
}

// 只是重新缩进不算撞车（CanonJSON 的意义所在）。
func TestApplyReindentIsNotHubChanged(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env", BaseValue: `{"A":"1","B":"2"}`, MineValue: `{"A":"9"}`},
	}))
	newBase := []byte("{\n  \"env\": {\n    \"B\": \"2\",\n    \"A\": \"1\"\n  }\n}")
	r.merged(t, p, []protocol.FileEntry{r.entry(t, p, newBase)})
	require.Empty(t, r.only(t).GetString("attention"))
}

// attention 只在变化时写：Snapshot 每次连接都会调，无条件写 = 每次拉取一次 DB 写。
func TestApplyDoesNotRewriteUnchangedAttention(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	files := []protocol.FileEntry{r.entry(t, p, []byte(`{"env":{"A":"新值"}}`))}

	r.merged(t, p, files)
	after := *r.saves
	require.Positive(t, after, "第一次要写 attention")

	r.merged(t, p, files)
	r.merged(t, p, files)
	require.Equal(t, after, *r.saves, "值没变就不该再 Save")
}

func TestApplyPathGone(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	other := r.entry(t, ".claude/CLAUDE.md", []byte("别的文件\n"))
	out, err := r.svc.Apply(r.machineID, "rev-x", []protocol.FileEntry{other})
	require.NoError(t, err)
	require.Equal(t, []protocol.FileEntry{other}, out, "其余照常")
	require.Equal(t, "path_gone", r.only(t).GetString("attention"))
}

func TestApplyUnmergeableBaseline(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	broken := r.entry(t, p, []byte("这不是 JSON"))
	got := r.merged(t, p, []protocol.FileEntry{broken})
	require.Equal(t, "这不是 JSON", got, "放弃本轮合并，下发原基线")
	require.Equal(t, "unmergeable", r.only(t).GetString("attention"))
}

func TestApplyTextThreeWayMerge(t *testing.T) {
	r := newRig(t)
	base := []byte("一\n二\n三\n四\n五\n六\n七\n八\n")
	mine := []byte("一\n二\n本机改的\n四\n五\n六\n七\n八\n")
	require.NoError(t, r.svc.CreateText(r.machineID, ".claude/CLAUDE.md", "", base, mine))

	// 中台在另一处改了。
	theirs := []byte("一\n二\n三\n四\n五\n六\n七\n中台改的\n")
	got := r.merged(t, ".claude/CLAUDE.md",
		[]protocol.FileEntry{r.entry(t, ".claude/CLAUDE.md", theirs)})
	require.Equal(t, "一\n二\n本机改的\n四\n五\n六\n七\n中台改的\n", got)
	require.Empty(t, r.only(t).GetString("attention"))
}

func TestApplyTextConflictTakesMineAndFlags(t *testing.T) {
	r := newRig(t)
	base := []byte("一\n二\n三\n")
	mine := []byte("一\n本机\n三\n")
	require.NoError(t, r.svc.CreateText(r.machineID, ".claude/CLAUDE.md", "", base, mine))

	theirs := []byte("一\n中台\n三\n")
	got := r.merged(t, ".claude/CLAUDE.md",
		[]protocol.FileEntry{r.entry(t, ".claude/CLAUDE.md", theirs)})
	require.Equal(t, "一\n本机\n三\n", got)
	require.NotContains(t, got, "<<<<<<<", "绝不写冲突标记")

	rec := r.only(t)
	require.Equal(t, "merge_conflict", rec.GetString("attention"))
	require.NotEmpty(t, rec.GetString("shadowed_blob"))
}

func TestDropOverlappingReplacesPrefixRelated(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env", BaseValue: `{"A":1}`, MineValue: `{"A":2}`},
	}))
	n, err := r.svc.DropOverlapping(r.machineID, p, "env.ANTHROPIC_MODEL")
	require.NoError(t, err)
	require.Equal(t, 1, n, "前缀关系的既有记录要被新的替换")

	// 反向也算重叠。
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A.B", BaseValue: "1", MineValue: "2"},
	}))
	n, err = r.svc.DropOverlapping(r.machineID, p, "env.A")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// 同级别的兄弟不算重叠。
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.AB", BaseValue: "1", MineValue: "2"},
	}))
	n, err = r.svc.DropOverlapping(r.machineID, p, "env.AC")
	require.NoError(t, err)
	require.Zero(t, n, "env.AB 与 env.AC 不构成前缀关系")
}

func TestKeepClearsAttention(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	r.merged(t, p, []protocol.FileEntry{r.entry(t, p, []byte(`{"env":{"A":"新"}}`))})
	rec := r.only(t)
	require.Equal(t, "hub_changed", rec.GetString("attention"))

	machineID, err := r.svc.Keep(rec.Id)
	require.NoError(t, err)
	require.Equal(t, r.machineID, machineID)

	rec = r.only(t)
	require.Empty(t, rec.GetString("attention"))
	require.Empty(t, rec.GetString("shadowed_value"))
	require.Empty(t, rec.GetString("shadowed_rev"))
}

func TestDeleteReturnsMachine(t *testing.T) {
	r := newRig(t)
	require.NoError(t, r.svc.CreateJSON(r.machineID, p, "", []overrides.Point{
		{Selector: "env.A", BaseValue: `"base"`, MineValue: `"mine"`},
	}))
	machineID, err := r.svc.Delete(r.only(t).Id)
	require.NoError(t, err)
	require.Equal(t, r.machineID, machineID)

	recs, err := r.svc.ForMachine(r.machineID)
	require.NoError(t, err)
	require.Empty(t, recs)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/overrides/ -run 'TestApply|TestDrop|TestKeep|TestDelete' -v`
Expected: FAIL —— `undefined: overrides.NewService`

- [ ] **Step 3: 在 `hub/internal/events/writer.go` 加四个事件 kind**

在 M1.5 那组常量之后追加：

```go
	// M1.8 本机覆盖层（spec §2、§8.5）。覆盖层的增删不产生新 Revision，
	// 因此这几条事件是唯一的审计痕迹。
	KindOverrideCreated  = "override.created"
	KindOverrideDropped  = "override.dropped"
	KindOverrideReplaced = "override.replaced"
	KindOverrideKept     = "override.kept"
```

- [ ] **Step 4: 写 `service.go`**

```go
package overrides

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/events"
)

// Service 管 machine_overrides 的读写与合并。
type Service struct {
	app    core.App
	blobs  *blobs.Store
	events *events.Writer
	log    *slog.Logger
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer, log *slog.Logger) *Service {
	if log == nil {
		if app != nil {
			log = app.Logger()
		} else {
			log = slog.Default()
		}
	}
	return &Service{app: app, blobs: b, events: ev, log: log}
}

// ForMachine 返回一台机器的全部覆盖层，按 path + selector 排序。
func (s *Service) ForMachine(machineID string) ([]*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("machine_overrides",
		"machine = {:m}", "path,selector", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("overrides: 查询 %s 的覆盖层: %w", machineID, err)
	}
	return recs, nil
}

// ForPath 返回一台机器某个路径上的全部覆盖层。
func (s *Service) ForPath(machineID, path string) ([]*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("machine_overrides",
		"machine = {:m} && path = {:p}", "selector", 0, 0,
		map[string]any{"m": machineID, "p": path})
	if err != nil {
		return nil, fmt.Errorf("overrides: 查询 %s 上 %s 的覆盖层: %w", machineID, path, err)
	}
	return recs, nil
}

// CreateJSON 为一批差异点各建一条 json_key 记录。
func (s *Service) CreateJSON(machineID, path, driftID string, points []Point) error {
	col, err := s.app.FindCollectionByNameOrId("machine_overrides")
	if err != nil {
		return fmt.Errorf("overrides: 找不到 collection: %w", err)
	}
	for _, pt := range points {
		r := core.NewRecord(col)
		r.Set("machine", machineID)
		r.Set("path", path)
		r.Set("kind", "json_key")
		r.Set("selector", pt.Selector)
		r.Set("base_value", pt.BaseValue)
		r.Set("mine_value", pt.MineValue)
		if driftID != "" {
			r.Set("origin_drift", driftID)
		}
		if err := s.app.Save(r); err != nil {
			return fmt.Errorf("overrides: 建 %s 上 %s 的覆盖层: %w", path, pt.Selector, err)
		}
	}
	return nil
}

// CreateText 建一条 text 记录：整份文件由 base / mine 两份全文表达。
// selector 恒为空串，因此一个路径上只能有一条 text 覆盖层（唯一索引保证）。
func (s *Service) CreateText(machineID, path, driftID string, base, mine []byte) error {
	col, err := s.app.FindCollectionByNameOrId("machine_overrides")
	if err != nil {
		return fmt.Errorf("overrides: 找不到 collection: %w", err)
	}
	baseID, err := s.putBlob(base)
	if err != nil {
		return err
	}
	mineID, err := s.putBlob(mine)
	if err != nil {
		return err
	}
	r := core.NewRecord(col)
	r.Set("machine", machineID)
	r.Set("path", path)
	r.Set("kind", "text")
	r.Set("selector", "")
	r.Set("base_blob", baseID)
	r.Set("mine_blob", mineID)
	if driftID != "" {
		r.Set("origin_drift", driftID)
	}
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("overrides: 建 %s 的文本覆盖层: %w", path, err)
	}
	return nil
}

// DropPath 删掉一台机器某个路径上的全部覆盖层，返回删掉的条数。
//
// 两处用它：建 ignore 规则时（路径退管，覆盖层无处可盖，spec §2.3），
// 以及新旧 kind 不同时（用户把 JSON 文件改成了非 JSON，spec §8.5）。
func (s *Service) DropPath(machineID, path string) (int, error) {
	recs, err := s.ForPath(machineID, path)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		if err := s.app.Delete(r); err != nil {
			return 0, fmt.Errorf("overrides: 删 %s 的覆盖层: %w", path, err)
		}
	}
	return len(recs), nil
}

// DropOverlapping 删掉与 selector 构成前缀关系（任一方向）的既有记录。
//
// 两次分开的排除操作可能产出 env 与 env.ANTHROPIC_MODEL 这样的重叠，
// 而重叠会让合并结果依赖应用顺序——不可接受。规则是**新的替换旧的**：
// 用户最后点的那次就是他的意思（spec §8.5）。
func (s *Service) DropOverlapping(machineID, path, selector string) (int, error) {
	recs, err := s.ForPath(machineID, path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range recs {
		if !selectorsOverlap(r.GetString("selector"), selector) {
			continue
		}
		if err := s.app.Delete(r); err != nil {
			return 0, fmt.Errorf("overrides: 删被替换的覆盖层 %s: %w", r.Id, err)
		}
		n++
	}
	return n, nil
}

// selectorsOverlap 判断两个 selector 是否构成前缀关系。
// 按段比较而不是按字符：env.AB 与 env.AC 不重叠，env 与 env.A 重叠。
func selectorsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".")
}

// Delete 删一条覆盖层，返回它所属的机器 id（调用方据此发 ConfigNotify）。
func (s *Service) Delete(id string) (string, error) {
	r, err := s.app.FindRecordById("machine_overrides", id)
	if err != nil {
		return "", fmt.Errorf("overrides: 覆盖层 %s 不存在: %w", id, err)
	}
	machineID := r.GetString("machine")
	path := r.GetString("path")
	if err := s.app.Delete(r); err != nil {
		return "", fmt.Errorf("overrides: 删覆盖层 %s: %w", id, err)
	}
	if s.events != nil {
		if err := s.events.Write(events.KindOverrideDropped, machineID, map[string]any{
			"path": path, "selector": r.GetString("selector"),
		}); err != nil {
			s.log.Warn("写 override.dropped 事件失败", "error", err)
		}
	}
	return machineID, nil
}

// Keep 清掉 attention 与 shadowed_*：用户看过了，决定保持本机的说法。
func (s *Service) Keep(id string) (string, error) {
	r, err := s.app.FindRecordById("machine_overrides", id)
	if err != nil {
		return "", fmt.Errorf("overrides: 覆盖层 %s 不存在: %w", id, err)
	}
	r.Set("attention", "")
	r.Set("shadowed_value", "")
	r.Set("shadowed_blob", "")
	r.Set("shadowed_rev", "")
	if err := s.app.Save(r); err != nil {
		return "", fmt.Errorf("overrides: 清 %s 的提醒: %w", id, err)
	}
	machineID := r.GetString("machine")
	if s.events != nil {
		if err := s.events.Write(events.KindOverrideKept, machineID, map[string]any{
			"path": r.GetString("path"), "selector": r.GetString("selector"),
		}); err != nil {
			s.log.Warn("写 override.kept 事件失败", "error", err)
		}
	}
	return machineID, nil
}

// putBlob 写入内容并返回 blob 记录 id（RelationField 要 id 不要 hash）。
func (s *Service) putBlob(content []byte) (string, error) {
	hash, err := s.blobs.Put(content)
	if err != nil {
		return "", err
	}
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return "", fmt.Errorf("overrides: 写入后找不到 blob %s: %w", hash, err)
	}
	return r.Id, nil
}

// blobBy 按 blob 记录 id 取内容。id 为空时返回空切片。
func (s *Service) blobBy(id string) ([]byte, error) {
	if id == "" {
		return nil, nil
	}
	r, err := s.app.FindRecordById("blobs", id)
	if err != nil {
		return nil, fmt.Errorf("overrides: blob %s 不存在: %w", id, err)
	}
	return s.blobs.Get(r.GetString("hash"))
}
```

- [ ] **Step 5: 写 `apply.go`**

```go
package overrides

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/FlintyLemming/orciny/hub/internal/merge3"
	"github.com/FlintyLemming/orciny/protocol"
)

// Apply 把一台机器的覆盖层盖到 head 的文件清单上（spec §4.3）。
//
// 输入的 files 处在**占位符空间**（{{provider.*}} / {{var.*}} 原样未渲染），
// 覆盖层里的内容也是——渲染发生在 agent 侧、合并之后，因此覆盖层永远
// 不会把 API key 明文带进 hub 库。
//
// 撞车一律取本机（spec §4.5）：unmergeable 与 path_gone 无从取，
// 等于放弃本轮合并、下发原基线。apply 永远不会因为撞车卡住。
func (s *Service) Apply(
	machineID, revID string, files []protocol.FileEntry,
) ([]protocol.FileEntry, error) {
	recs, err := s.ForMachine(machineID)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return files, nil
	}

	byPath := map[string][]*core.Record{}
	for _, r := range recs {
		p := r.GetString("path")
		byPath[p] = append(byPath[p], r)
	}

	out := make([]protocol.FileEntry, len(files))
	copy(out, files)
	seen := map[string]bool{}

	for i, entry := range out {
		group := byPath[entry.Path]
		if len(group) == 0 {
			continue
		}
		seen[entry.Path] = true

		base, err := s.blobs.Get(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("overrides: 取 %s 的基线内容: %w", entry.Path, err)
		}
		merged, err := s.mergeOne(base, group, revID)
		if err != nil {
			return nil, err
		}
		if string(merged) == string(base) {
			continue // 内容没变就不必重新 Put
		}
		h, err := s.blobs.Put(merged)
		if err != nil {
			return nil, fmt.Errorf("overrides: 写合并结果 %s: %w", entry.Path, err)
		}
		// Mode / Keys 不动：覆盖层只管内容。
		out[i].Hash = h
		out[i].Size = uint32(len(merged))
	}

	// 覆盖层指向的路径不在 files 里 → path_gone，其余照常。
	for path, group := range byPath {
		if seen[path] {
			continue
		}
		for _, r := range group {
			if err := s.flag(r, "path_gone", "", "", ""); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// mergeOne 合并一个路径上的一组覆盖层。同一路径上 kind 恒一致
// （kind 由文件内容决定，建覆盖层时已用「新的替换旧的」保证）。
func (s *Service) mergeOne(base []byte, group []*core.Record, revID string) ([]byte, error) {
	if group[0].GetString("kind") == "text" {
		return s.mergeText(base, group[0], revID)
	}
	return s.mergeJSON(base, group, revID)
}

// mergeJSON 逐点覆盖。撞车检测比的是**原始 base**，不是逐步改出来的中间态
// ——selector 互不重叠（spec §8.5），因此这样读是对的。
func (s *Service) mergeJSON(base []byte, group []*core.Record, revID string) ([]byte, error) {
	if !IsJSONObject(base) {
		for _, r := range group {
			if err := s.flag(r, "unmergeable", "", "", ""); err != nil {
				return nil, err
			}
		}
		return base, nil // 放弃本轮合并，下发原基线
	}

	out := base
	for _, r := range group {
		sel := r.GetString("selector")

		// —— 撞车检测：中台是不是也动了同一处？——
		cur := gjson.GetBytes(base, sel)
		curRaw := ""
		if cur.Exists() {
			curRaw = cur.Raw
		}
		if sameRawJSON(curRaw, r.GetString("base_value")) {
			if err := s.flag(r, "", "", "", ""); err != nil {
				return nil, err
			}
		} else if err := s.flag(r, "hub_changed", curRaw, "", revID); err != nil {
			return nil, err
		}

		// —— 取本机 ——
		mine := r.GetString("mine_value")
		var err error
		if mine == "" {
			out, err = sjson.DeleteBytes(out, sel) // 空串 = 本机没有这个键
		} else {
			out, err = sjson.SetRawBytes(out, sel, []byte(mine))
		}
		if err != nil {
			return nil, fmt.Errorf("overrides: 在 %s 上写 %s: %w",
				r.GetString("path"), sel, err)
		}
	}
	return out, nil
}

// mergeText 走行级三方合并。冲突取本机，标 merge_conflict，
// 并把中台的新全文存进 shadowed_blob 供收件箱做三方对比。
func (s *Service) mergeText(theirs []byte, r *core.Record, revID string) ([]byte, error) {
	base, err := s.blobBy(r.GetString("base_blob"))
	if err != nil {
		return nil, err
	}
	mine, err := s.blobBy(r.GetString("mine_blob"))
	if err != nil {
		return nil, err
	}

	lines, conflicts := merge3.Merge(
		merge3.SplitLines(base), merge3.SplitLines(mine), merge3.SplitLines(theirs))
	out := merge3.Join(lines)

	if len(conflicts) == 0 {
		if err := s.flag(r, "", "", "", ""); err != nil {
			return nil, err
		}
		return out, nil
	}
	shadow, err := s.putBlob(theirs)
	if err != nil {
		return nil, err
	}
	if err := s.flag(r, "merge_conflict", "", shadow, revID); err != nil {
		return nil, err
	}
	return out, nil
}

// flag 写 attention 与 shadowed_*，**只在与库里已存的值不同时才 Save**。
//
// Snapshot() 每次连接、每次通知都会调，无条件写会造成每次拉取一次 DB 写
// （spec §4.5）。
func (s *Service) flag(r *core.Record, attention, value, blobID, revID string) error {
	if attention == "" {
		value, blobID, revID = "", "", ""
	}
	if r.GetString("attention") == attention &&
		r.GetString("shadowed_value") == value &&
		r.GetString("shadowed_blob") == blobID &&
		r.GetString("shadowed_rev") == revID {
		return nil
	}
	r.Set("attention", attention)
	r.Set("shadowed_value", value)
	r.Set("shadowed_blob", blobID)
	r.Set("shadowed_rev", revID)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("overrides: 更新 %s 的提醒状态: %w", r.Id, err)
	}
	return nil
}

// sameRawJSON 用规范化形式比较，让重新缩进不误报撞车。
// 任一侧不是合法 JSON 时退回字节比较（空串对空串也走这条）。
func sameRawJSON(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	ca, errA := CanonJSON([]byte(a))
	cb, errB := CanonJSON([]byte(b))
	if errA != nil || errB != nil {
		return false
	}
	return ca == cb
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/overrides/ -v`
Expected: PASS（全部用例）

- [ ] **Step 7: 提交**

```bash
git add hub/internal/overrides hub/internal/events
git commit -m "feat(overrides): 快照组装期的合并与撞车检测

JSON 按 selector 逐点覆盖、文本走 merge3；四种撞车一律取本机并只写
attention 提醒；attention 只在变化时 Save，避免每次拉取一次 DB 写
（M1.8 spec §4.3-§4.5）。"
```

---

### Task 6: 接进 `configsync.Snapshot`、`blobs.GCOrphans` 补引用、`ReasonOverride` 通知

**Files:**
- Modify: `protocol/messages_m1.go:38-43`（ConfigNotify.Reason 常量组）
- Modify: `hub/internal/configsync/service.go`（`Deps` 加字段、`Snapshot` 加一行）
- Modify: `hub/internal/configsync/notify.go`（新增 `NotifyOverride`）
- Modify: `hub/internal/blobs/blobs.go:120-178`（`GCOrphans` 扫 `machine_overrides` 的三个 blob 关系）
- Test: `hub/internal/configsync/service_test.go`
- Test: `hub/internal/blobs/blobs_test.go`

**Interfaces:**
- Consumes: `overrides.Service.Apply`（Task 5）。
- Produces:
  - `protocol.ReasonOverride = "override"`
  - `configsync.Deps.Overrides *overrides.Service`（nil 时跳过合并，既有单测不必装配）
  - `func (s *Service) NotifyOverride(machineID string) error` —— 发一条**不带 RevisionID** 的 `ConfigNotify`

**为什么 agent 一行都不用改（spec §4.4）。** 三条既有事实叠起来，「同一 revision、内容变了」这条路已经通：`configsync.Pull` 不比较 `p.Have`，永远重发快照；`syncer.applyPending` 不按 `RevisionID` 短路；`applier.BuildPlan` 逐文件比 `st.Files[rel].Rendered` 与本次渲染结果，内容变了 → Overwrite，没变 → Skip 零写入。`NotifyProvider` 早就在用这条路，`ReasonOverride` 与它语义完全对齐。

**Checksum 要跟着重算。** `head.checksum` 是 head 文件清单的指纹；合并改了 hash 之后原值就对不上了。合并后统一 `protocol.Checksum(files)`——对没有覆盖层的机器，结果与 `head.checksum` 逐位相同（同一份清单、同一个口径），因此这不是行为变更。

**`GCOrphans` 必须认识覆盖层（spec §8.3）。** 它现在只扫 `revisions.files`、`config_sets.draft`、`drift_events.current_blob` 三处。`base_blob` / `mine_blob` / `shadowed_blob` 不补进去，删掉一个配置集就会把用户的覆盖层内容当孤儿清掉——**静默数据丢失**。合并产物 blob 不需要单独保护：它由 base + 覆盖层纯函数决定，被 GC 掉之后下一次 `Snapshot` 会重新算出来并 `Put` 回去。

- [ ] **Step 1: 写 `blobs` 的回归测试（先失败）**

追加到 `hub/internal/blobs/blobs_test.go`：

```go
// 覆盖层引用的 blob 不能被当孤儿清掉——那是静默数据丢失（spec §8.3）。
func TestGCOrphansKeepsOverrideBlobs(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	baseH, err := s.Put([]byte("覆盖层的基线侧"))
	require.NoError(t, err)
	mineH, err := s.Put([]byte("覆盖层的本机侧"))
	require.NoError(t, err)
	shadowH, err := s.Put([]byte("被挡下的中台新版"))
	require.NoError(t, err)
	orphan, err := s.Put([]byte("真孤儿"))
	require.NoError(t, err)

	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(machines)
	m.Set("fingerprint", "fp-gc")
	m.Set("pub_key", "pk-gc")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	idOf := func(h string) string {
		r, err := app.FindFirstRecordByData("blobs", "hash", h)
		require.NoError(t, err)
		return r.Id
	}

	ovc, err := app.FindCollectionByNameOrId("machine_overrides")
	require.NoError(t, err)
	ov := core.NewRecord(ovc)
	ov.Set("machine", m.Id)
	ov.Set("path", ".claude/CLAUDE.md")
	ov.Set("kind", "text")
	ov.Set("base_blob", idOf(baseH))
	ov.Set("mine_blob", idOf(mineH))
	ov.Set("shadowed_blob", idOf(shadowH))
	require.NoError(t, app.Save(ov))

	n, err := s.GCOrphans("")
	require.NoError(t, err)
	require.Equal(t, 1, n)

	for _, h := range []string{baseH, mineH, shadowH} {
		ok, err := s.Has(h)
		require.NoError(t, err)
		require.True(t, ok, "被覆盖层引用的 blob 不该被删")
	}
	ok, err := s.Has(orphan)
	require.NoError(t, err)
	require.False(t, ok)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/blobs/ -run TestGCOrphansKeepsOverrideBlobs -v`
Expected: FAIL —— `Expected 1, got 4`（三份覆盖层内容被当孤儿删了）

- [ ] **Step 3: 改 `blobs.GCOrphans`**

在 `hub/internal/blobs/blobs.go` 的 `GCOrphans` 里，把 `drift_events` 那段之后补上覆盖层的三个关系。先把「按 blob 记录 id 标记引用」抽成闭包，再复用：

```go
	markByID := func(id string) {
		if id == "" {
			return
		}
		if b, err := s.app.FindRecordById("blobs", id); err == nil && b != nil {
			referenced[b.GetString("hash")] = true
		}
	}

	drifts, err := s.app.FindAllRecords("drift_events")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 drift_events: %w", err)
	}
	for _, d := range drifts {
		markByID(d.GetString("current_blob"))
	}

	// 覆盖层的三份内容是**用户数据**：不补进来，删一个配置集就会把
	// 用户的本机保留内容当孤儿清掉（spec §8.3）。
	// 合并产物 blob 不必单独保护——它由 base + 覆盖层纯函数决定，
	// 被 GC 掉之后下一次 Snapshot 会重新算出来并 Put 回去。
	ovs, err := s.app.FindAllRecords("machine_overrides")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 machine_overrides: %w", err)
	}
	for _, o := range ovs {
		markByID(o.GetString("base_blob"))
		markByID(o.GetString("mine_blob"))
		markByID(o.GetString("shadowed_blob"))
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/blobs/ -v`
Expected: PASS

- [ ] **Step 5: 加 `protocol.ReasonOverride`**

在 `protocol/messages_m1.go` 的 `ConfigNotify.Reason` 常量组末尾追加：

```go
	// ReasonOverride：本机覆盖层增删。与 ReasonRotated 同类——
	// 不带 RevisionID，agent 拉回来发现版本没变但内容变了，
	// BuildPlan 自行判 Overwrite（M1.8 spec §4.4）。
	ReasonOverride = "override"
```

- [ ] **Step 6: 写 `configsync` 的测试（先失败）**

追加到 `hub/internal/configsync/service_test.go`。先在 `rig` 结构体里加一个 `ovs *overrides.Service` 字段，并在 `newRig` 里装配（顺带把它传进 `configsync.Deps`）：

```go
	r.ovs = overrides.NewService(app, b, ev, nil)
	r.svc = configsync.NewService(configsync.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Vars: r.vars,
		Providers: r.provs, Events: ev, Sender: r.sender, Overrides: r.ovs,
	})
```

（`newRig` 现在把 `b` 与 `ev` 存进局部变量即可；`rig` 需新增 `blobs *blobs.Store` 字段供用例取内容。）

用例：

```go
// 有覆盖层的机器拿到合并后的 hash；同配置集的其它机器拿原 hash。
func TestSnapshotAppliesOverridesPerMachine(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{
		".claude/settings.json": []byte(`{"env":{"A":"中台"}}`),
	})
	a := r.assign(t, setID, "fp-ov-a")
	b := r.assign(t, setID, "fp-ov-b")

	require.NoError(t, r.ovs.CreateJSON(a, ".claude/settings.json", "",
		[]overrides.Point{{Selector: "env.A", BaseValue: `"中台"`, MineValue: `"本机"`}}))

	snapA, err := r.svc.Snapshot(a)
	require.NoError(t, err)
	snapB, err := r.svc.Snapshot(b)
	require.NoError(t, err)

	contentA, err := r.blobs.Get(hashOf(t, snapA, ".claude/settings.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"本机"}}`, string(contentA))

	contentB, err := r.blobs.Get(hashOf(t, snapB, ".claude/settings.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"中台"}}`, string(contentB), "别的机器不受影响")

	require.NotEqual(t, snapA.Checksum, snapB.Checksum, "清单变了 checksum 必须跟着变")
}

// 没有覆盖层时 checksum 与 head 逐位相同——重算不是行为变更。
func TestSnapshotChecksumUnchangedWithoutOverrides(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{".claude/CLAUDE.md": []byte("规矩\n")})
	m := r.assign(t, setID, "fp-ov-c")

	snap, err := r.svc.Snapshot(m)
	require.NoError(t, err)
	head, err := r.revs.Head(setID)
	require.NoError(t, err)
	require.Equal(t, head.GetString("checksum"), snap.Checksum)
}

// 覆盖层增删后发一条不带 RevisionID 的 ConfigNotify（spec §4.4）。
func TestNotifyOverrideSendsNotifyWithoutRevision(t *testing.T) {
	r := newRig(t)
	setID := r.publish(t, map[string][]byte{".claude/CLAUDE.md": []byte("规矩\n")})
	m := r.assign(t, setID, "fp-ov-d")

	require.NoError(t, r.svc.NotifyOverride(m))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.NotEmpty(t, sent)
	n := sent[len(sent)-1].payload.(protocol.ConfigNotify)
	require.Equal(t, m, sent[len(sent)-1].machine)
	require.Equal(t, protocol.ReasonOverride, n.Reason)
	require.Empty(t, n.RevisionID, "不带 RevisionID：同版本、内容变了")
}

func hashOf(t *testing.T, snap protocol.ConfigSnapshot, path string) string {
	t.Helper()
	for _, f := range snap.Files {
		if f.Path == path {
			return f.Hash
		}
	}
	t.Fatalf("快照里没有 %s", path)
	return ""
}
```

若 `rig` 还没有 `publish` / `assign` 辅助方法，按既有用例的写法补上：

```go
// publish 建配置集、写文件、发布，返回配置集 id。
func (r *rig) publish(t *testing.T, files map[string][]byte) string {
	t.Helper()
	set, err := r.sets.Create("集-"+t.Name(), "")
	require.NoError(t, err)
	for path, content := range files {
		_, err := r.sets.SetDraftFile(set.Id, path, content, 0o644, nil)
		require.NoError(t, err)
	}
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	return set.Id
}

// assign 建一台机器并指派到给定配置集，返回机器 id。
func (r *rig) assign(t *testing.T, setID, fp string) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", fp)
	m.Set("pub_key", "pk-"+fp)
	m.Set("status", "online")
	require.NoError(t, r.app.Save(m))
	r.sender.online[m.Id] = true
	_, err = r.sets.Assign(m.Id, setID, configsets.ModeApply)
	require.NoError(t, err)
	return m.Id
}
```

- [ ] **Step 7: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsync/ -run 'TestSnapshotApplies|TestSnapshotChecksum|TestNotifyOverride' -v`
Expected: FAIL —— `unknown field Overrides in struct literal` 与 `undefined: NotifyOverride`

- [ ] **Step 8: 改 `configsync/service.go`**

在 `Deps` 里，`Drift DriftHandler` 之后追加：

```go
	// Overrides 是本机覆盖层（M1.8 spec §4.3）。快照组装到一半时挂上去，
	// 把该机器保留的差异点盖回文件清单。nil = 不做合并（单测常见）。
	Overrides *overrides.Service
```

在 `Snapshot` 里，`files, err := s.d.Revs.Files(head.Id)` 之后、组装 `refs` 之前插入：

```go
	// 覆盖层落在**占位符空间**：中台基线未渲染，agent 上报的漂移内容
	// 也经 RestoreWithBase 还原成占位符，两侧同处一个空间才能合并。
	// 渲染仍发生在 agent 侧、合并之后（spec §4.3）。
	checksum := head.GetString("checksum")
	if s.d.Overrides != nil {
		files, err = s.d.Overrides.Apply(machineID, head.Id, files)
		if err != nil {
			return snap, err
		}
		// 清单的 hash 变了，指纹必须跟着重算。没有覆盖层时
		// Checksum(files) 与 head.checksum 逐位相同，因此这不是行为变更。
		checksum = protocol.Checksum(files)
	}
```

并把返回的 `Checksum: head.GetString("checksum")` 改成 `Checksum: checksum`。import 补 `"github.com/FlintyLemming/orciny/hub/internal/overrides"`。

- [ ] **Step 9: 在 `configsync/notify.go` 加 `NotifyOverride`**

放在 `NotifyProvider` 之后：

```go
// NotifyOverride 在本机覆盖层增删之后通知单台机器（M1.8 spec §4.4）。
//
// **不带 RevisionID**：与 NotifyProvider 走同一条路。agent 拉回来发现
// revision 相同但文件内容变了，BuildPlan 逐文件比 rendered hash，
// 变了的判 Overwrite、没变的判 Skip——零新代码，也不产生漂移。
func (s *Service) NotifyOverride(machineID string) error {
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
	s.send(machineID, protocol.ConfigNotify{
		ConfigSetID: setID, Reason: protocol.ReasonOverride,
	})
	return nil
}
```

- [ ] **Step 10: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsync/ ./hub/internal/blobs/ ./protocol/ -v`
Expected: PASS

- [ ] **Step 11: 提交**

```bash
git add protocol/messages_m1.go hub/internal/configsync hub/internal/blobs
git commit -m "feat(configsync): 快照组装期挂上覆盖层合并，GC 认识覆盖层 blob

Snapshot 拿到 head 文件后调 overrides.Apply 并重算 checksum；
覆盖层增删发一条不带 RevisionID 的 ConfigNotify（ReasonOverride）；
GCOrphans 补上 base/mine/shadowed 三处引用——不补就是静默数据丢失
（M1.8 spec §4.3、§4.4、§8.3）。"
```

---

### Task 7: `drift.Override()` —— 五条护栏、`overridden` 状态、与退管互斥、`Remanage`

**Files:**
- Create: `hub/internal/drift/override.go`
- Create: `hub/internal/drift/override_test.go`
- Modify: `hub/internal/drift/service.go`（`Deps` 加 `Overrides`）
- Modify: `hub/internal/drift/resolve.go`（`Ignore` 先删覆盖层；新增 `Remanage`）
- Modify: `hub/internal/drift/rig_test.go`（rig 装配 `overrides.Service`）
- Test: `hub/internal/drift/resolve_test.go`（追加退管互斥与 `Remanage` 用例）

**Interfaces:**
- Consumes: `overrides.Service`（Task 5）、`overrides.Split` / `Hunks` / `SynthesizeMine` / `IsJSONObject`、`configsets.ModeSurvey`。
- Produces:
  - `type Selection struct { Given bool; Selectors []string; Hunks []int }` —— 零值 = 全选
  - `func (s *Service) Override(eventIDs []string, points map[string]Selection, reviewed []string) error`
  - `func (s *Service) Remanage(machineID, path string) error`
  - `var ErrPathUnmanaged = errors.New(...)`
  - `var ErrNotOverridable = errors.New(...)`
  - `var ErrNoPoints = errors.New(...)`
  - `drift.Deps.Overrides *overrides.Service`

**五条护栏（spec §8.2、§8.4、§8.5、§2.3）——全部在任何写入之前检查完，要么整批成、要么什么都不动：**

1. `binding_drift` → `ErrBindingDrift`。覆盖层会把 `{{provider.*}}` 拍平成硬编码字面值，绑定当场失效，且把 API key 明文写进 hub 库。UI 引导到已有的三档（`BindingDriftActions.tsx`）。
2. `restore_partial` 必须出现在 `reviewed` 里，否则 `ErrNeedsReview`（同 `adopt.go:79`）。
3. `truncated`（未能安全脱敏）、二进制、`kind == "deleted"` → `ErrNotOverridable`。删除整份文件没有可保留的差异点，那是「不再管这个路径」的活。
4. keys 模式路径的 selector 必须落在受管键内。`.claude.json` 是 `ModeKeys`（只管 `mcpServers`），受管键之外的东西中台从来不写，在那里建覆盖层毫无作用——用户会得到「设置好了但什么都没发生」。
5. 该路径已有 ignore 规则（机器级或全局）→ `ErrPathUnmanaged`。退管意味着中台根本不下发它，覆盖层无处可盖。

**selector 重叠：新的替换旧的（spec §8.5）。** 建覆盖层时删掉与新 selector 构成前缀关系（任一方向）的既有记录，写一条 `override.replaced`。同一路径上两种 `kind` 也不共存——`kind` 由文件内容决定；既有记录的 kind 与本次不同时，删掉该路径上的全部既有覆盖层再建新的。

**反向互斥（spec §2.3）。** 建 ignore 规则时该路径已有覆盖层 → 先删掉覆盖层，写 `override.dropped`。用户的意图很明确（整个路径都不要了），拦住他没有意义。

- [ ] **Step 1: 让 `rig_test.go` 装配 overrides**

在 `hub/internal/drift/rig_test.go` 的 `rig` 结构体加 `ovs *overrides.Service`，并在 `newRig` 里：

```go
	r.ovs = overrides.NewService(app, b, ev, nil)
	r.svc = drift.NewService(drift.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Events: ev,
		Sync: r.sync, Providers: provs, Overrides: r.ovs,
	})
```

同时把 `syncSvc` 的构造补上 `Overrides: r.ovs`（需要把 `overrides.NewService` 提到 `syncSvc` 之前）。再加一个辅助方法：

```go
// overridesOf 返回一台机器的全部覆盖层。
func (r *rig) overridesOf(t *testing.T, machineID string) []*core.Record {
	t.Helper()
	recs, err := r.ovs.ForMachine(machineID)
	require.NoError(t, err)
	return recs
}
```

- [ ] **Step 2: 写 `override_test.go`（先失败）**

```go
package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/protocol"
)

// jsonDrift 让本机在 settings.json 上产生一条 open 漂移。
func (r *rig) jsonDrift(t *testing.T, base, cur []byte) string {
	t.Helper()
	set, err := r.sets.Create("覆盖层用", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/settings.json", base, 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.setID, r.revID = set.Id, rev.Id

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Mode: 0o644, Content: cur,
	})
	return r.driftsByPath(t)[".claude/settings.json"].Id
}

func TestOverrideCreatesPointsAndMarksDrift(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台","B":"共用"}}`),
		[]byte(`{"env":{"A":"本机","B":"共用"}}`))

	require.NoError(t, r.svc.Override([]string{id}, nil, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1)
	require.Equal(t, "json_key", ovs[0].GetString("kind"))
	require.Equal(t, "env.A", ovs[0].GetString("selector"))
	require.JSONEq(t, `"本机"`, ovs[0].GetString("mine_value"))
	require.Equal(t, id, ovs[0].GetString("origin_drift"))

	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	require.Equal(t, "overridden", rec.GetString("state"))
	r.requireEvent(t, "override.created")
}

func TestOverrideRespectsSelectorSelection(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台","B":"中台"}}`),
		[]byte(`{"env":{"A":"本机","B":"本机"}}`))

	require.NoError(t, r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true, Selectors: []string{"env.A"}}}, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1, "没勾的差异点不建记录")
	require.Equal(t, "env.A", ovs[0].GetString("selector"))
}

func TestOverrideEmptySelectionIsError(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	err := r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true}}, nil)
	require.ErrorIs(t, err, drift.ErrNoPoints)
	require.Empty(t, r.overridesOf(t, r.machineID), "报错就什么都不动")
}

func TestOverrideRejectsBindingDrift(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"}}`),
		[]byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrBindingDrift)
	require.Empty(t, r.overridesOf(t, r.machineID))
}

func TestOverrideNeedsReviewForRestorePartial(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("restore_partial", true)
	require.NoError(t, r.app.Save(rec))

	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNeedsReview)
	require.NoError(t, r.svc.Override([]string{id}, nil, []string{id}))
}

func TestOverrideRejectsTruncated(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("truncated", true)
	require.NoError(t, r.app.Save(rec))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)
}

func TestOverrideRejectsDeletedKind(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	rec, err := r.app.FindRecordById("drift_events", id)
	require.NoError(t, err)
	rec.Set("kind", "deleted")
	require.NoError(t, r.app.Save(rec))
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)
}

// keys 模式：受管键之外建覆盖层毫无作用，会得到「设置好了但什么都没发生」。
func TestOverrideRejectsSelectorOutsideManagedKeys(t *testing.T) {
	r := newRig(t)
	set, err := r.sets.Create("keys 模式", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude.json",
		[]byte(`{"mcpServers":{"a":1},"projects":{"x":1}}`), 0o644, []string{"mcpServers"})
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.report(t, protocol.DriftItem{
		Path: ".claude.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"mcpServers":{"a":1},"projects":{"x":2}}`),
	})
	id := r.driftsByPath(t)[".claude.json"].Id

	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrNotOverridable)

	// 受管键内的差异点则允许。
	r.resolve(t, ".claude.json", "superseded")
	r.report(t, protocol.DriftItem{
		Path: ".claude.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"mcpServers":{"a":9},"projects":{"x":1}}`),
	})
	id2 := r.driftsByPath(t)[".claude.json"].Id
	require.NoError(t, r.svc.Override([]string{id2}, nil, nil))
}

// 已退管的路径不能建覆盖层：中台根本不下发它，覆盖层无处可盖。
func TestOverrideRejectsIgnoredPath(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	r.addIgnoreRule(t, r.machineID, ".claude/settings.json")
	require.ErrorIs(t, r.svc.Override([]string{id}, nil, nil), drift.ErrPathUnmanaged)

	// 全局规则同样拦住。
	r2 := newRig(t)
	id2 := r2.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	r2.addIgnoreRule(t, "", ".claude/settings.json")
	require.ErrorIs(t, r2.svc.Override([]string{id2}, nil, nil), drift.ErrPathUnmanaged)
}

// 新的替换旧的：前缀关系的既有记录被删掉，并记一条 override.replaced。
func TestOverrideReplacesOverlappingSelector(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台"}}`), []byte(`{"env":{"A":"本机"}}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil)) // env.A

	r.resolve(t, ".claude/settings.json", "superseded")
	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified, Mode: 0o644,
		Content: []byte(`{"env":{"A":"本机","B":"新增"}}`),
	})
	id2 := r.driftsByPath(t)[".claude/settings.json"].Id
	require.NoError(t, r.svc.Override([]string{id2},
		map[string]drift.Selection{id2: {Given: true, Selectors: []string{"env"}}}, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1, "env 覆盖了 env.A")
	require.Equal(t, "env", ovs[0].GetString("selector"))
	r.requireEvent(t, "override.replaced")
}

// 文本文件：一条记录承载整个文件，勾选塌缩进 mine_blob。
func TestOverrideTextKeepsSelectedHunks(t *testing.T) {
	r := newRig(t)
	base := []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nl16\n")
	cur := []byte("L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\nl11\nl12\nl13\nl14\nl15\nL16\n")

	set, err := r.sets.Create("文本", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/CLAUDE.md", base, 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, "apply")
	require.NoError(t, err)
	r.report(t, protocol.DriftItem{
		Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, Mode: 0o644, Content: cur,
	})
	id := r.driftsByPath(t)[".claude/CLAUDE.md"].Id

	// 只留第一处 hunk。
	require.NoError(t, r.svc.Override([]string{id},
		map[string]drift.Selection{id: {Given: true, Hunks: []int{0}}}, nil))

	ovs := r.overridesOf(t, r.machineID)
	require.Len(t, ovs, 1)
	require.Equal(t, "text", ovs[0].GetString("kind"))
	require.Empty(t, ovs[0].GetString("selector"))

	blobRec, err := r.app.FindRecordById("blobs", ovs[0].GetString("mine_blob"))
	require.NoError(t, err)
	mine, err := r.blobs.Get(blobRec.GetString("hash"))
	require.NoError(t, err)
	require.Contains(t, string(mine), "L1\n")
	require.Contains(t, string(mine), "l16\n", "没勾的 hunk 退回基线")
}

// 建覆盖层后要发一条 ConfigNotify，不必等下一次拉取。
func TestOverrideNotifiesMachine(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil))

	sent := r.sender.of(protocol.KindConfigNotify)
	require.NotEmpty(t, sent)
	n := sent[len(sent)-1].payload.(protocol.ConfigNotify)
	require.Equal(t, protocol.ReasonOverride, n.Reason)
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/drift/ -run TestOverride -v`
Expected: FAIL —— `undefined: drift.Selection` / `r.svc.Override undefined`

- [ ] **Step 4: 在 `drift/service.go` 的 `Deps` 里加 `Overrides`**

放在 `Providers` 之后：

```go
	// Overrides 是本机覆盖层（M1.8 spec §4）。Override() 建记录、
	// Ignore() 删记录都走它。
	Overrides *overrides.Service
```

import 补 `"github.com/FlintyLemming/orciny/hub/internal/overrides"`。

- [ ] **Step 5: 写 `drift/override.go`**

```go
package drift

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/overrides"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

var (
	// ErrPathUnmanaged：该路径在这台机器上已经退管，中台根本不下发它，
	// 覆盖层无处可盖（spec §2.3）。
	ErrPathUnmanaged = errors.New(
		"drift: 该路径已「不再受管」，请先在机器详情页恢复受管再做本机保留")

	// ErrNotOverridable：这条漂移压根没有可保留的差异点
	// ——二进制、未脱敏、整份删除，或 keys 模式的受管键之外（spec §8.2、§8.4）。
	ErrNotOverridable = errors.New("drift: 这条漂移不能做本机保留")

	// ErrNoPoints：前端显式给了空的勾选。多半是漏传，
	// 而不是用户想做一次空操作（spec §7）。
	ErrNoPoints = errors.New("drift: 没有勾选任何差异点")
)

// Selection 是一个 event 的勾选结果。零值（Given = false）= 全选，
// 这样 UI 的默认路径不必先算一遍差异点（spec §7）。
type Selection struct {
	Given     bool
	Selectors []string // json_key：被勾选的 selector
	Hunks     []int    // text：被勾选的 hunk 下标
}

// plan 是一条漂移过完全部护栏之后、准备落库的形态。
type overridePlan struct {
	rec     *core.Record
	machine string
	path    string
	json    bool
	points  []overrides.Point // json_key
	base    []byte            // text
	mine    []byte            // text
}

// Override 把选中的漂移转成本机覆盖层（spec §4、§8）。
//
// 五条护栏全部在**任何写入之前**检查完：要么整批成，要么什么都不动
// ——与 AdoptReviewed 同一个规矩。
func (s *Service) Override(eventIDs []string, points map[string]Selection, reviewed []string) error {
	if len(eventIDs) == 0 {
		return fmt.Errorf("drift: 没有选中任何漂移")
	}
	if s.d.Overrides == nil {
		return fmt.Errorf("drift: 覆盖层服务未装配")
	}
	reviewedSet := map[string]bool{}
	for _, id := range reviewed {
		reviewedSet[id] = true
	}

	plans := make([]overridePlan, 0, len(eventIDs))
	for _, id := range eventIDs {
		p, err := s.planOverride(id, points[id], reviewedSet[id])
		if err != nil {
			return err
		}
		plans = append(plans, p)
	}

	now := types.NowDateTime()
	for _, p := range plans {
		if err := s.commitOverride(p, now); err != nil {
			return err
		}
	}

	// 立刻通知，不必等下一次拉取。不带 RevisionID：同版本、内容变了。
	if s.d.Sync == nil {
		return nil
	}
	notified := map[string]bool{}
	for _, p := range plans {
		if notified[p.machine] {
			continue
		}
		notified[p.machine] = true
		if err := s.d.Sync.NotifyOverride(p.machine); err != nil {
			s.log.Warn("建覆盖层后通知失败", "machine", p.machine, "error", err)
		}
	}
	return nil
}

// planOverride 过五条护栏并算出要落库的差异点。不写任何东西。
func (s *Service) planOverride(id string, sel Selection, reviewed bool) (overridePlan, error) {
	var p overridePlan

	rec, err := s.d.App.FindRecordById("drift_events", id)
	if err != nil {
		return p, fmt.Errorf("drift: 漂移 %s 不存在: %w", id, err)
	}
	if rec.GetString("state") != "open" {
		return p, fmt.Errorf("drift: 漂移 %s 已被处理过", id)
	}
	path := rec.GetString("path")
	machineID := rec.GetString("machine")

	// 护栏 1：绑定漂移（spec §8.2）。覆盖层会把 {{provider.*}} 拍平成
	// 硬编码字面值，绑定当场失效，且把 API key 明文写进 hub 库。
	if rec.GetBool("binding_drift") {
		return p, fmt.Errorf("%w：%s。请改用卡片上的「改绑定」，"+
			"或者「恢复」把它拉回基线", ErrBindingDrift, path)
	}
	// 护栏 2：未脱敏 / 整份删除（spec §8.2）。
	if rec.GetBool("truncated") {
		return p, fmt.Errorf("%w：%s 含未能安全脱敏的凭据", ErrNotOverridable, path)
	}
	if rec.GetString("kind") == "deleted" {
		return p, fmt.Errorf("%w：%s 在本机整份被删除，没有可保留的差异点；"+
			"想让这台机器不再收到它，请用「不再管这个路径」", ErrNotOverridable, path)
	}
	// 护栏 3：restore_partial 必须逐条看过（spec §8.2，同 adopt.go）。
	if rec.GetBool("restore_partial") && !reviewed {
		return p, fmt.Errorf("%w: %s", ErrNeedsReview, path)
	}
	// 护栏 4：与退管互斥（spec §2.3）。
	unmanaged, err := s.pathIsUnmanaged(machineID, path)
	if err != nil {
		return p, err
	}
	if unmanaged {
		return p, fmt.Errorf("%w：%s", ErrPathUnmanaged, path)
	}

	base, cur, err := s.overrideSides(rec)
	if err != nil {
		return p, err
	}
	if !isText(base) || !isText(cur) {
		return p, fmt.Errorf("%w：%s 是二进制内容", ErrNotOverridable, path)
	}

	p = overridePlan{rec: rec, machine: machineID, path: path}

	// kind 由文件内容决定：两侧都是 JSON 对象才走 json_key。
	if overrides.IsJSONObject(base) && overrides.IsJSONObject(cur) {
		p.json = true
		all, err := overrides.Split(base, cur)
		if err != nil {
			return p, fmt.Errorf("%w：%s 切分差异点失败: %v", ErrNotOverridable, path, err)
		}
		p.points, err = pickPoints(all, sel)
		if err != nil {
			return p, err
		}
		// 护栏 5：keys 模式的 selector 必须落在受管键内（spec §8.4）。
		if err := s.checkManagedKeys(machineID, path, p.points); err != nil {
			return p, err
		}
		return p, nil
	}

	keep, err := pickHunks(overrides.Hunks(base, cur), sel)
	if err != nil {
		return p, err
	}
	p.base = base
	p.mine = overrides.SynthesizeMine(base, cur, keep)
	return p, nil
}

// pickPoints 按勾选筛差异点。Given = false 时全选。
func pickPoints(all []overrides.Point, sel Selection) ([]overrides.Point, error) {
	if !sel.Given {
		if len(all) == 0 {
			return nil, fmt.Errorf("%w：这条漂移没有可切分的差异点", ErrNoPoints)
		}
		return all, nil
	}
	if len(sel.Selectors) == 0 {
		return nil, ErrNoPoints
	}
	want := map[string]bool{}
	for _, s := range sel.Selectors {
		want[s] = true
	}
	out := make([]overrides.Point, 0, len(sel.Selectors))
	for _, p := range all {
		if want[p.Selector] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w：勾选的 selector 都不在差异点里", ErrNoPoints)
	}
	return out, nil
}

// pickHunks 按勾选筛 hunk 下标。Given = false 时全选。
func pickHunks(hs []overrides.Hunk, sel Selection) ([]int, error) {
	if !sel.Given {
		if len(hs) == 0 {
			return nil, fmt.Errorf("%w：这条漂移没有可保留的改动块", ErrNoPoints)
		}
		out := make([]int, len(hs))
		for i := range hs {
			out[i] = i
		}
		return out, nil
	}
	if len(sel.Hunks) == 0 {
		return nil, ErrNoPoints
	}
	for _, i := range sel.Hunks {
		if i < 0 || i >= len(hs) {
			return nil, fmt.Errorf("%w：hunk 下标 %d 越界（共 %d 个）", ErrNoPoints, i, len(hs))
		}
	}
	return sel.Hunks, nil
}

// overrideSides 取这条漂移的基线侧与本机侧内容，两侧都在占位符空间。
func (s *Service) overrideSides(rec *core.Record) (base, cur []byte, err error) {
	if h := rec.GetString("base_hash"); h != "" {
		base, err = s.d.Blobs.Get(h)
		if err != nil {
			return nil, nil, fmt.Errorf("drift: 取 %s 的基线内容: %w",
				rec.GetString("path"), err)
		}
	}
	blobID := rec.GetString("current_blob")
	if blobID == "" {
		return nil, nil, fmt.Errorf("%w：%s 的本机内容不在库里",
			ErrNotOverridable, rec.GetString("path"))
	}
	b, err := s.d.App.FindRecordById("blobs", blobID)
	if err != nil {
		return nil, nil, fmt.Errorf("drift: %s 的内容不在库里: %w",
			rec.GetString("path"), err)
	}
	cur, err = s.d.Blobs.Get(b.GetString("hash"))
	if err != nil {
		return nil, nil, err
	}
	return base, cur, nil
}

// pathIsUnmanaged 判断该路径在这台机器上是否已退管（机器级或全局规则）。
func (s *Service) pathIsUnmanaged(machineID, path string) (bool, error) {
	rules, err := s.IgnorePaths(machineID)
	if err != nil {
		return false, err
	}
	for _, p := range rules {
		if manifest.MatchGlob(p, path) || p == path {
			return true, nil
		}
	}
	return false, nil
}

// checkManagedKeys 拦下 keys 模式受管键之外的 selector（spec §8.4）。
//
// agent 的 applier.merge 只把 f.Keys 列出的键合进磁盘文件，受管键之外
// 中台从来不写。在那里建覆盖层毫无作用——用户会得到一个
// 「设置好了但什么都没发生」的状态。
func (s *Service) checkManagedKeys(machineID, path string, points []overrides.Point) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return err
	}
	head, err := s.d.Revs.Head(assign.GetString("config_set"))
	if err != nil {
		return err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return err
	}
	var keys []string
	for _, f := range files {
		if f.Path == path {
			keys = f.Keys
			break
		}
	}
	if len(keys) == 0 {
		return nil // 不是 keys 模式，整份文件都受管
	}
	for _, p := range points {
		ok := false
		for _, k := range keys {
			esc := overrides.EscapeSeg(k)
			if p.Selector == esc || len(p.Selector) > len(esc) &&
				p.Selector[:len(esc)+1] == esc+"." {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%w：%s 是「只管指定键」模式（受管键 %v），"+
				"而 %s 在受管键之外——中台从来不写那里，建了也不会生效",
				ErrNotOverridable, path, keys, p.Selector)
		}
	}
	return nil
}

// commitOverride 落库：先按「新的替换旧的」清场，再建记录、置 overridden。
func (s *Service) commitOverride(p overridePlan, now types.DateTime) error {
	// kind 变了（用户把 JSON 文件改成了非 JSON，或反之）→ 整条路径重来。
	existing, err := s.d.Overrides.ForPath(p.machine, p.path)
	if err != nil {
		return err
	}
	wantKind := "text"
	if p.json {
		wantKind = "json_key"
	}
	kindChanged := len(existing) > 0 && existing[0].GetString("kind") != wantKind
	if kindChanged || !p.json {
		// 文本一个路径只有一条记录，同样是整条路径替换。
		n, err := s.d.Overrides.DropPath(p.machine, p.path)
		if err != nil {
			return err
		}
		s.writeReplaced(p, n)
	} else {
		total := 0
		for _, pt := range p.points {
			n, err := s.d.Overrides.DropOverlapping(p.machine, p.path, pt.Selector)
			if err != nil {
				return err
			}
			total += n
		}
		s.writeReplaced(p, total)
	}

	if p.json {
		if err := s.d.Overrides.CreateJSON(p.machine, p.path, p.rec.Id, p.points); err != nil {
			return err
		}
	} else if err := s.d.Overrides.CreateText(p.machine, p.path, p.rec.Id, p.base, p.mine); err != nil {
		return err
	}

	p.rec.Set("state", "overridden")
	p.rec.Set("resolved_at", now)
	if err := s.d.App.Save(p.rec); err != nil {
		return fmt.Errorf("drift: 标记 %s 已转覆盖层: %w", p.path, err)
	}
	detail := map[string]any{"path": p.path, "kind": kindOf(p.json)}
	if p.json {
		detail["points"] = len(p.points)
	}
	if err := s.d.Events.Write(events.KindOverrideCreated, p.machine, detail); err != nil {
		s.log.Warn("写 override.created 事件失败", "error", err)
	}
	return nil
}

func (s *Service) writeReplaced(p overridePlan, n int) {
	if n == 0 {
		return
	}
	// 重叠会让合并结果依赖应用顺序——不可接受。用户最后点的那次
	// 就是他的意思（spec §8.5）。
	if err := s.d.Events.Write(events.KindOverrideReplaced, p.machine, map[string]any{
		"path": p.path, "replaced": n,
	}); err != nil {
		s.log.Warn("写 override.replaced 事件失败", "error", err)
	}
}

func kindOf(isJSON bool) string {
	if isJSON {
		return "json_key"
	}
	return "text"
}
```

`override.go` 的 import 块最终是：`errors`、`fmt`、`github.com/pocketbase/pocketbase/core`、`github.com/pocketbase/pocketbase/tools/types`、以及本仓库的 `events` / `overrides` / `internal/manifest` 四个包。**不要 import `protocol`**——本文件用不到它。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/drift/ -run TestOverride -v`
Expected: PASS（12 个用例）

- [ ] **Step 7: 写退管互斥与 `Remanage` 的测试（先失败）**

追加到 `hub/internal/drift/resolve_test.go`：

```go
// 建 ignore 规则时该路径已有覆盖层 → 先删掉覆盖层（spec §2.3）。
// 用户的意图很明确（整个路径都不要了），拦住他没有意义。
func TestIgnoreDropsExistingOverrides(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t, []byte(`{"a":1}`), []byte(`{"a":2}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil))
	require.Len(t, r.overridesOf(t, r.machineID), 1)

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Mode: 0o644, Content: []byte(`{"a":3}`),
	})
	id2 := r.driftsByPath(t)[".claude/settings.json"].Id
	require.NoError(t, r.svc.Ignore([]string{id2}, false))

	require.Empty(t, r.overridesOf(t, r.machineID), "退管必须清掉覆盖层")
	r.requireEvent(t, "override.dropped")
}

// 恢复受管必须**原子地**做两件事，且顺序不能反：先置 survey，再删规则。
// 直接恢复受管的话，下一次 apply 会当场用中台版本盖掉他本机的改动
// ——而他打开这个页面的目的就是把那份改动捞回来（spec §3.1）。
func TestRemanageSetsSurveyAndDropsRule(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.addIgnoreRule(t, r.machineID, ".claude/settings.json")

	require.NoError(t, r.svc.Remanage(r.machineID, ".claude/settings.json"))

	assign, err := r.sets.Assignment(r.machineID)
	require.NoError(t, err)
	require.Equal(t, configsets.ModeSurvey, assign.GetString("mode"))

	paths, err := r.svc.IgnorePaths(r.machineID)
	require.NoError(t, err)
	require.NotContains(t, paths, ".claude/settings.json")
}

// 全局规则不由单机的「恢复受管」删除——那会悄悄改掉全机队的行为。
func TestRemanageLeavesGlobalRule(t *testing.T) {
	r := newRig(t)
	r.assign(t)
	r.addIgnoreRule(t, "", ".claude/settings.json")

	require.ErrorIs(t, r.svc.Remanage(r.machineID, ".claude/settings.json"),
		drift.ErrGlobalRule)

	paths, err := r.svc.IgnorePaths(r.machineID)
	require.NoError(t, err)
	require.Contains(t, paths, ".claude/settings.json")
}
```

在该文件的 import 里补 `"github.com/FlintyLemming/orciny/hub/internal/configsets"` 与 `"github.com/FlintyLemming/orciny/hub/internal/drift"`（若尚未 import）。

- [ ] **Step 8: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/drift/ -run 'TestIgnoreDrops|TestRemanage' -v`
Expected: FAIL —— `undefined: drift.ErrGlobalRule` / `r.svc.Remanage undefined`

- [ ] **Step 9: 改 `drift/resolve.go`**

（a）在 `Ignore` 的循环里，写 ignore_rules **之前**先清掉该路径的覆盖层：

```go
		// 退管与覆盖层互斥：退管意味着中台根本不下发它，覆盖层无处可盖。
		// 用户的意图很明确（整个路径都不要了），拦住他没有意义（spec §2.3）。
		if s.d.Overrides != nil {
			n, err := s.d.Overrides.DropPath(machineID, path)
			if err != nil {
				return err
			}
			if n > 0 {
				if err := s.d.Events.Write(events.KindOverrideDropped, machineID,
					map[string]any{"path": path, "reason": "path_unmanaged", "count": n}); err != nil {
					s.log.Warn("写 override.dropped 事件失败", "error", err)
				}
			}
		}
```

（b）在文件末尾加 `Remanage` 与它的错误：

```go
// ErrGlobalRule：全局忽略规则不由单机的「恢复受管」删除，
// 那会悄悄改掉全机队的行为。
var ErrGlobalRule = errors.New(
	"drift: 这是一条全局规则，请到设置里解除，或改为只对这台机器建例外")

// Remanage 恢复受管（spec §3.1）：**原子地**置 survey 并删掉该机器
// 在这个路径上的 ignore 规则。
//
// 顺序不能反，survey 也不是可选项。若直接恢复受管，下一次 apply 会当场
// 用中台版本盖掉他本机的改动——那份改动此后只剩在 drift_events.current_blob
// 里，而他刚打开这个页面的**目的**就是把它捞回来。
func (s *Service) Remanage(machineID, path string) error {
	rules, err := s.d.App.FindRecordsByFilter("ignore_rules",
		"path = {:p} && (machine = '' || machine = {:m})", "", 0, 0,
		map[string]any{"p": path, "m": machineID})
	if err != nil {
		return fmt.Errorf("drift: 查询忽略规则: %w", err)
	}
	own := make([]*core.Record, 0, len(rules))
	for _, r := range rules {
		if r.GetString("machine") == "" {
			return fmt.Errorf("%w：%s", ErrGlobalRule, path)
		}
		own = append(own, r)
	}
	if len(own) == 0 {
		return nil // 本来就受管，幂等返回
	}

	err = s.d.App.RunInTransaction(func(tx core.App) error {
		assign, err := s.d.Sets.Assignment(machineID)
		if err != nil {
			return err
		}
		// 先置 survey：让差异先进收件箱，别让下一次 apply 抹掉本机改动。
		assign.Set("mode", configsets.ModeSurvey)
		assign.Set("state", configsets.StatePending)
		if err := tx.Save(assign); err != nil {
			return fmt.Errorf("drift: 打回 survey: %w", err)
		}
		// 再删规则。
		for _, r := range own {
			if err := tx.Delete(r); err != nil {
				return fmt.Errorf("drift: 删忽略规则 %s: %w", path, err)
			}
		}
		return s.d.Events.WriteTx(tx, events.KindAssignChanged, machineID, map[string]any{
			"reason": "remanage", "path": path, "mode": configsets.ModeSurvey,
		})
	})
	if err != nil {
		return err
	}

	if s.d.Sync == nil {
		return nil
	}
	return s.d.Sync.NotifyMachine(machineID, protocol.ReasonAssigned)
}
```

import 补 `"errors"` 与 `"github.com/FlintyLemming/orciny/hub/internal/configsets"`。

- [ ] **Step 10: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/drift/ -v`
Expected: PASS（全包，含既有的 adopt / binding / resolve 用例）

- [ ] **Step 11: 提交**

```bash
git add hub/internal/drift
git commit -m "feat(drift): Override() 与五条护栏、Remanage 恢复受管

绑定漂移 / restore_partial / 未脱敏 / 整份删除 / keys 模式越界 / 退管互斥
全部在任何写入之前检查完；selector 重叠按「新的替换旧的」；
退管时先清掉覆盖层；Remanage 原子地置 survey 再删规则
（M1.8 spec §2.3、§3.1、§8.2、§8.4、§8.5）。"
```

---

### Task 8: 四条 API、`mapErr` 映射、Hub 装配与端到端

**Files:**
- Modify: `hub/internal/routes/routes.go`（注册四条路由）
- Modify: `hub/internal/routes/config.go`（`Admin` 接口 + 四个 handler + `mapErr` 两条映射）
- Modify: `hub/api.go`（Hub 实现四个方法）
- Modify: `hub/hub.go`（装配 `overrides.Service` 并注入 configsync / drift）
- Test: `hub/internal/routes/config_test.go`（`fakeAdmin` 补四个方法 + 端点用例）
- Test: `hub/internal/drift/rig_test.go` + 新用例（端到端）

**Interfaces:**
- Consumes: `drift.Service.Override` / `Remanage`（Task 7）、`overrides.Service.Delete` / `Keep`（Task 5）、`configsync.NotifyOverride`（Task 6）。
- Produces（`Admin` 接口新增，`*hub.Hub` 实现）：
  - `OverrideDrift(events []string, points map[string]drift.Selection, reviewed []string) error`
  - `DropOverride(id string) error`
  - `KeepOverride(id string) error`
  - `Remanage(machineID, path string) error`
- 路由（全部 `Bind(su)`）：

| 方法 | 路径 | 入参 |
|---|---|---|
| POST | `/api/orciny/drift/override` | `{events, points, reviewed}` |
| DELETE | `/api/orciny/overrides/{id}` | — |
| POST | `/api/orciny/overrides/{id}/keep` | — |
| POST | `/api/orciny/machines/{id}/remanage` | `{path}` |

- `mapErr` 新增：`ErrPathUnmanaged` → 409 `{"reason":"path_unmanaged"}`，`ErrNotOverridable` → 409 `{"reason":"not_overridable"}`，`ErrNoPoints` → 400，`ErrGlobalRule` → 409 `{"reason":"global_rule"}`。

**退管（原「忽略」）继续走已有的 `POST /drift/ignore`**，只是前端文案改名。列表读取走 PocketBase 的集合 API（与 `drift_events` 现有做法一致），不额外开 GET 路由。

`points` 里缺席的 event 视为「全选」；一个 event 显式给出空数组则是错误（`ErrNoPoints`）——它多半是前端漏传，而不是用户想做一次空操作。

- [ ] **Step 1: 写路由测试（先失败）**

在 `hub/internal/routes/config_test.go` 的 `fakeAdmin` 上加字段与方法：

```go
	// —— M1.8 本机覆盖层 ——
	overrideEvents   []string
	overridePoints   map[string]drift.Selection
	overrideReviewed []string
	overrideErr      error
	droppedOverride  string
	keptOverride     string
	remanageMachine  string
	remanagePath     string
```

```go
func (f *fakeAdmin) OverrideDrift(events []string, points map[string]drift.Selection, reviewed []string) error {
	f.overrideEvents, f.overridePoints, f.overrideReviewed = events, points, reviewed
	return f.overrideErr
}
func (f *fakeAdmin) DropOverride(id string) error { f.droppedOverride = id; return nil }
func (f *fakeAdmin) KeepOverride(id string) error { f.keptOverride = id; return nil }
func (f *fakeAdmin) Remanage(machineID, path string) error {
	f.remanageMachine, f.remanagePath = machineID, path
	return nil
}
```

用例：

```go
func TestOverrideDriftEndpoint(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})

	res := doJSON(t, srv, http.MethodPost, "/api/orciny/drift/override", `{
		"events": ["e1", "e2"],
		"points": {
			"e1": {"selectors": ["env.ANTHROPIC_MODEL"]},
			"e2": {"hunks": [0, 2]}
		},
		"reviewed": ["e2"]
	}`)
	require.Equal(t, http.StatusNoContent, res.StatusCode)

	require.Equal(t, []string{"e1", "e2"}, admin.overrideEvents)
	require.Equal(t, []string{"e2"}, admin.overrideReviewed)
	require.True(t, admin.overridePoints["e1"].Given)
	require.Equal(t, []string{"env.ANTHROPIC_MODEL"}, admin.overridePoints["e1"].Selectors)
	require.Equal(t, []int{0, 2}, admin.overridePoints["e2"].Hunks)
}

// points 里缺席的 event 视为全选：Given 必须是 false。
func TestOverrideDriftAbsentPointsMeansAll(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	res := doJSON(t, srv, http.MethodPost, "/api/orciny/drift/override",
		`{"events":["e1"]}`)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.False(t, admin.overridePoints["e1"].Given)
}

func TestOverrideDriftMapsPathUnmanagedTo409(t *testing.T) {
	admin := &fakeAdmin{overrideErr: drift.ErrPathUnmanaged}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	res := doJSON(t, srv, http.MethodPost, "/api/orciny/drift/override", `{"events":["e1"]}`)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Contains(t, bodyOf(t, res), "path_unmanaged")
}

func TestOverrideDriftMapsNotOverridableTo409(t *testing.T) {
	admin := &fakeAdmin{overrideErr: drift.ErrNotOverridable}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	res := doJSON(t, srv, http.MethodPost, "/api/orciny/drift/override", `{"events":["e1"]}`)
	require.Equal(t, http.StatusConflict, res.StatusCode)
	require.Contains(t, bodyOf(t, res), "not_overridable")
}

func TestOverrideDriftMapsNoPointsTo400(t *testing.T) {
	admin := &fakeAdmin{overrideErr: drift.ErrNoPoints}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	res := doJSON(t, srv, http.MethodPost, "/api/orciny/drift/override", `{"events":["e1"]}`)
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestDropAndKeepOverrideEndpoints(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})

	res := doJSON(t, srv, http.MethodDelete, "/api/orciny/overrides/ov1", "")
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, "ov1", admin.droppedOverride)

	res = doJSON(t, srv, http.MethodPost, "/api/orciny/overrides/ov2/keep", "")
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, "ov2", admin.keptOverride)
}

func TestRemanageEndpoint(t *testing.T) {
	admin := &fakeAdmin{}
	srv := newRouterServer(t, routes.Deps{Admin: admin})
	res := doJSON(t, srv, http.MethodPost, "/api/orciny/machines/m1/remanage",
		`{"path":".claude/settings.json"}`)
	require.Equal(t, http.StatusNoContent, res.StatusCode)
	require.Equal(t, "m1", admin.remanageMachine)
	require.Equal(t, ".claude/settings.json", admin.remanagePath)
}
```

若 `config_test.go` 里还没有 `doJSON` / `bodyOf` 辅助，按既有用例的请求写法补上：

```go
// doJSON 发一条带 superuser 鉴权的请求。body 为空串时不带 body。
func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func bodyOf(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return string(b)
}
```

> 既有用例已经在用 superuser 鉴权的构造方式（`newRouterServer` 内部装了 `apis.RequireSuperuserAuth()` 的旁路）。照抄同文件里现有请求的做法，不要另起一套。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/routes/ -run 'TestOverrideDrift|TestDropAndKeep|TestRemanage' -v`
Expected: FAIL —— `fakeAdmin does not implement routes.Admin` 与 404

- [ ] **Step 3: 在 `routes/config.go` 的 `Admin` 接口加四个方法**

在 M1.5 那组之后追加：

```go
	// —— M1.8 本机覆盖层 ——
	OverrideDrift(events []string, points map[string]drift.Selection, reviewed []string) error
	DropOverride(id string) error
	KeepOverride(id string) error
	Remanage(machineID, path string) error
```

- [ ] **Step 4: 在 `routes/config.go` 加四个 handler**

放在 `ignoreDrift` 之后：

```go
// overrideDrift 把选中的漂移转成本机覆盖层（M1.8 spec §7）。
//
// points 里缺席的 event 视为「全选」，这样 UI 的默认路径不必先算一遍
// 差异点；一个 event 显式给出空数组则是错误——它多半是前端漏传，
// 而不是用户想做一次空操作。
func (d Deps) overrideDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Events []string `json:"events"`
		Points map[string]struct {
			Selectors []string `json:"selectors"`
			Hunks     []int    `json:"hunks"`
		} `json:"points"`
		Reviewed []string `json:"reviewed"`
	}
	if err := e.BindBody(&req); err != nil || len(req.Events) == 0 {
		return e.BadRequestError("需要 events", nil)
	}
	points := make(map[string]drift.Selection, len(req.Points))
	for id, p := range req.Points {
		points[id] = drift.Selection{
			Given: true, Selectors: p.Selectors, Hunks: p.Hunks,
		}
	}
	if err := d.Admin.OverrideDrift(req.Events, points, req.Reviewed); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// dropOverride 撤掉一处排除：下次快照这台机器就拿到中台的值。
func (d Deps) dropOverride(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.DropOverride(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// keepOverride 清掉提醒：用户看过了，决定保持本机的说法。
func (d Deps) keepOverride(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.KeepOverride(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// remanage 恢复受管：原子地置 survey 并删掉该路径的忽略规则
// （M1.8 spec §3.1）。survey 不是可选项——直接恢复受管的话，
// 下一次 apply 会当场用中台版本盖掉用户想捞回来的那份改动。
func (d Deps) remanage(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := e.BindBody(&req); err != nil || req.Path == "" {
		return e.BadRequestError("需要 path", nil)
	}
	if err := d.Admin.Remanage(e.Request.PathValue("id"), req.Path); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}
```

- [ ] **Step 5: 在 `mapErr` 加四条映射**

放在 `drift.ErrNeedsReview` 那条之后：

```go
	case errors.Is(err, drift.ErrPathUnmanaged):
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{"reason": "path_unmanaged"},
		})
	case errors.Is(err, drift.ErrNotOverridable):
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{"reason": "not_overridable"},
		})
	case errors.Is(err, drift.ErrGlobalRule):
		return e.JSON(http.StatusConflict, map[string]any{
			"message": err.Error(),
			"data":    map[string]any{"reason": "global_rule"},
		})
	case errors.Is(err, drift.ErrNoPoints):
		return e.BadRequestError(err.Error(), nil)
```

- [ ] **Step 6: 在 `routes/routes.go` 注册四条路由**

在 `g.POST("/drift/ignore", d.ignoreDrift).Bind(su)` 之后：

```go
	g.POST("/drift/override", d.overrideDrift).Bind(su)
	g.DELETE("/overrides/{id}", d.dropOverride).Bind(su)
	g.POST("/overrides/{id}/keep", d.keepOverride).Bind(su)
	g.POST("/machines/{id}/remanage", d.remanage).Bind(su)
```

- [ ] **Step 7: 跑路由测试确认通过**

Run: `go test -tags=testing ./hub/internal/routes/ -v`
Expected: PASS

- [ ] **Step 8: 在 `hub/api.go` 实现四个方法**

在 `ClearDegraded` 之后追加：

```go
// —— M1.8 本机覆盖层 ——

// OverrideDrift 把选中的漂移转成本机覆盖层，并通知相关机器。
func (h *Hub) OverrideDrift(events []string, points map[string]drift.Selection, reviewed []string) error {
	return h.drift.Override(events, points, reviewed)
}

// DropOverride 撤掉一处排除，并通知该机器重新拉取。
func (h *Hub) DropOverride(id string) error {
	machineID, err := h.overrides.Delete(id)
	if err != nil {
		return err
	}
	return h.sync.NotifyOverride(machineID)
}

// KeepOverride 清掉提醒。不必通知——合并结果一个字节都没变。
func (h *Hub) KeepOverride(id string) error {
	_, err := h.overrides.Keep(id)
	return err
}

// Remanage 恢复受管：原子地置 survey 并删掉该路径的忽略规则。
func (h *Hub) Remanage(machineID, path string) error {
	return h.drift.Remanage(machineID, path)
}
```

import 补 `"github.com/FlintyLemming/orciny/hub/internal/drift"`。

- [ ] **Step 9: 在 `hub/hub.go` 装配 `overrides.Service`**

在 `Hub` 结构体加字段 `overrides *overrides.Service`（放在 `drift` 旁边）；在 `h.blobs = blobs.New(e.App)` 之后建它，并注入两处：

```go
		h.overrides = overrides.NewService(e.App, h.blobs, h.events, e.App.Logger())
```

`configsync.NewService(configsync.Deps{...})` 的字面量里加 `Overrides: h.overrides,`；`drift.NewService(drift.Deps{...})` 的字面量里加 `Overrides: h.overrides,`。import 补 `"github.com/FlintyLemming/orciny/hub/internal/overrides"`。

- [ ] **Step 10: 写端到端用例**

追加到 `hub/internal/drift/override_test.go`：

```go
// 端到端（spec §9）：建覆盖层 → drift 置 overridden → 发布新版本
// （改同一文件的**其它**键）→ 该机器拿到的快照里，被排除的键是本机值、
// 其余键是中台新值。
func TestOverrideSurvivesNewRevision(t *testing.T) {
	r := newRig(t)
	id := r.jsonDrift(t,
		[]byte(`{"env":{"A":"中台","B":"旧"}}`),
		[]byte(`{"env":{"A":"本机","B":"旧"}}`))
	require.NoError(t, r.svc.Override([]string{id}, nil, nil))

	// 中台改了别的键并发布。
	_, err := r.sets.SetDraftFile(r.setID, ".claude/settings.json",
		[]byte(`{"env":{"A":"中台","B":"新"}}`), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(r.setID, "v2", "publish")
	require.NoError(t, err)

	snap, err := r.sync.Snapshot(r.machineID)
	require.NoError(t, err)
	var hash string
	for _, f := range snap.Files {
		if f.Path == ".claude/settings.json" {
			hash = f.Hash
		}
	}
	content, err := r.blobs.Get(hash)
	require.NoError(t, err)
	require.JSONEq(t, `{"env":{"A":"本机","B":"新"}}`, string(content),
		"被排除的键是本机值，其余键跟着中台走")

	// 中台没碰 env.A，因此不该有撞车提醒。
	require.Empty(t, r.overridesOf(t, r.machineID)[0].GetString("attention"))
}
```

- [ ] **Step 11: 跑全量 Go 测试**

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 12: 提交**

```bash
git add hub/internal/routes hub/api.go hub/hub.go hub/internal/drift
git commit -m "feat(routes): 四条覆盖层端点与 mapErr 映射，装配 overrides 服务

POST /drift/override、DELETE /overrides/{id}、POST /overrides/{id}/keep、
POST /machines/{id}/remanage；points 缺席 = 全选，显式空数组 = ErrNoPoints
（M1.8 spec §7）。"
```

---

### Task 9: 前端基础设施 —— 类型、store、api 封装、差异点切分

**Files:**
- Modify: `hub/internal/site/src/types/collections.ts`
- Create: `hub/internal/site/src/stores/overrides.ts`
- Modify: `hub/internal/site/src/lib/api.ts`
- Create: `hub/internal/site/src/lib/overridePoints.ts`
- Test: `hub/internal/site/src/lib/overridePoints.test.ts`

**Interfaces:**
- Consumes: Task 8 的四条端点；`machine_overrides` collection（PB SDK 直读）。
- Produces（`types/collections.ts`）：
  - `type DriftState` 增加 `'overridden'`
  - `type EventKind` 增加 `'override.created' | 'override.dropped' | 'override.replaced' | 'override.kept'`
  - `type OverrideKind = 'json_key' | 'text'`
  - `type OverrideAttention = '' | 'hub_changed' | 'merge_conflict' | 'path_gone' | 'unmergeable'`
  - `interface MachineOverrideRecord { id, machine, path, kind, selector, base_value, mine_value, base_blob, mine_blob, attention, shadowed_value, shadowed_blob, shadowed_rev, origin_drift, note, created, updated, expand? }`
  - `const COLLECTION_MACHINE_OVERRIDES = 'machine_overrides'`
- Produces（`stores/overrides.ts`）：
  - `$overrides: atom<MachineOverrideRecord[]>`、`$overridesLoading`、`$overridesError`
  - `$attentionCount: atom<number>` —— `attention !== ''` 的条数，收件箱横幅用
  - `subscribeOverrides(): () => void`（引用计数 + realtime，形状照抄 `stores/drift.ts`）
  - `overridesByPath(list, machineId?): { path: string; items: MachineOverrideRecord[] }[]`
- Produces（`lib/api.ts`）：
  - `overrideDrift(events, points, reviewed?)`
  - `dropOverride(id)` / `keepOverride(id)` / `remanage(machineId, path)`
- Produces（`lib/overridePoints.ts`）：
  - `escapeSeg(s: string): string`
  - `splitPoints(base: string, mine: string): OverridePoint[] | null`（`null` = 两侧不都是 JSON 对象 → 走文本一路）
  - `diffHunkCount(diff: string): number` —— 数 unified diff 里的 `@@` 块
  - `interface OverridePoint { selector: string; base: string; mine: string }`

**为什么前端也要切一遍差异点。** `OverrideDialog` 要在**建覆盖层之前**把差异点列出来给用户勾。四条端点里没有「预览差异点」这条（spec §7 明确只开四条），所以前端从 `base_hash` 与 `current_blob` 两份内容自己算。两侧实现必须给出**同一套 selector**——这靠 `escapeSeg` 与「数组当叶子、键序排序、空串 = 不存在」三条规则对齐，由本任务的测试钉住。文本一路不需要前端算：hunk 下标就是 `drift.diff` 里 `@@` 块的序号，与 Go 的 `overrides.Hunks` 一一对应（Task 4 已钉）。

- [ ] **Step 1: 写 `overridePoints.test.ts`（先失败）**

```ts
import { describe, expect, it } from 'vitest'
import { diffHunkCount, escapeSeg, splitPoints } from '@/lib/overridePoints'

describe('escapeSeg', () => {
  it('转义 gjson 路径里的四个元字符', () => {
    // 与 Go 侧 overrides.EscapeSeg 逐字符一致，否则覆盖层会写到错误的位置
    expect(escapeSeg('a.b')).toBe('a\\.b')
    expect(escapeSeg('c*d')).toBe('c\\*d')
    expect(escapeSeg('e?f')).toBe('e\\?f')
    expect(escapeSeg('g\\h')).toBe('g\\\\h')
    expect(escapeSeg('普通键')).toBe('普通键')
  })
})

describe('splitPoints', () => {
  it('逐键下降进对象', () => {
    expect(splitPoints('{"env":{"A":"1","B":"2"}}', '{"env":{"A":"9","B":"2"}}')).toEqual([
      { selector: 'env.A', base: '"1"', mine: '"9"' },
    ])
  })

  it('数组整体当叶子', () => {
    const pts = splitPoints('{"allow":["a"]}', '{"allow":["a","b"]}')
    expect(pts).toHaveLength(1)
    expect(pts![0].selector).toBe('allow')
  })

  it('一侧缺键时该侧为空串', () => {
    expect(splitPoints('{"a":1}', '{"a":1,"b":2}')).toEqual([
      { selector: 'b', base: '', mine: '2' },
    ])
    expect(splitPoints('{"a":1,"b":2}', '{"a":1}')).toEqual([
      { selector: 'b', base: '2', mine: '' },
    ])
  })

  it('重新缩进不算差异点', () => {
    expect(splitPoints('{\n  "a": {\n    "b": 1\n  }\n}', '{"a":{"b":1}}')).toEqual([])
  })

  it('selector 按字典序稳定排列', () => {
    const pts = splitPoints('{}', '{"z":1,"a":1,"m":1}')
    expect(pts!.map((p) => p.selector)).toEqual(['a', 'm', 'z'])
  })

  it('转义带元字符的键', () => {
    const pts = splitPoints('{"hooks":{"a.b":1}}', '{"hooks":{"a.b":2}}')
    expect(pts![0].selector).toBe('hooks.a\\.b')
  })

  it('两侧不都是 JSON 对象时返回 null —— 走文本一路', () => {
    expect(splitPoints('# 标题', '# 新标题')).toBeNull()
    expect(splitPoints('[1]', '[1,2]')).toBeNull()
    expect(splitPoints('{"a":1}', '不是 JSON')).toBeNull()
  })
})

describe('diffHunkCount', () => {
  it('数 @@ 块 —— 下标与后端的 hunk 一一对应', () => {
    const diff = [
      '--- f（基线）',
      '+++ f（本机）',
      '@@ -1,4 +1,4 @@',
      '-a',
      '+A',
      '@@ -13,4 +13,4 @@',
      '-z',
      '+Z',
      '',
    ].join('\n')
    expect(diffHunkCount(diff)).toBe(2)
  })

  it('空 diff 是 0 块', () => {
    expect(diffHunkCount('')).toBe(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd hub/internal/site && npx vitest run src/lib/overridePoints.test.ts`
Expected: FAIL —— `Failed to resolve import "@/lib/overridePoints"`

- [ ] **Step 3: 写 `lib/overridePoints.ts`**

```ts
/**
 * 差异点的前端切分。
 *
 * 与 Go 侧 hub/internal/overrides/split.go 是**同一套规则的两份实现**：
 * 数组整体当叶子、键按字典序、空串 = 该键在这一侧不存在、
 * selector 逐段转义。OverrideDialog 要在建覆盖层之前把差异点列出来给
 * 用户勾，而 API 里没有「预览差异点」这条（M1.8 spec §7），只能自己算。
 *
 * 两份实现给出的 selector 必须逐字符一致——不一致就会写到错误的位置。
 */

export interface OverridePoint {
  selector: string
  /** 原始 JSON 片段；空串 = 该键在基线侧不存在 */
  base: string
  /** 原始 JSON 片段；空串 = 该键在本机侧不存在 */
  mine: string
}

/**
 * 转义一个键段，使它能安全地拼进 gjson / sjson 的路径。
 * 路径语法里 . 是分隔符、* ? 是通配符、\ 是转义符。
 */
export function escapeSeg(s: string): string {
  let out = ''
  for (const ch of s) {
    if (ch === '\\' || ch === '.' || ch === '*' || ch === '?') out += '\\'
    out += ch
  }
  return out
}

type JSONObject = Record<string, unknown>

function asObject(text: string): JSONObject | null {
  try {
    const v: unknown = JSON.parse(text)
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return null
    return v as JSONObject
  } catch {
    return null
  }
}

function isPlainObject(v: unknown): v is JSONObject {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

/** 规范化形式，只用于比较：重新缩进 / 键序不同不该算差异。 */
function canon(v: unknown): string {
  if (isPlainObject(v)) {
    const keys = Object.keys(v).sort()
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canon(v[k])}`).join(',')}}`
  }
  if (Array.isArray(v)) return `[${v.map(canon).join(',')}]`
  return JSON.stringify(v) ?? 'null'
}

/**
 * 切出差异点。两侧不都是 JSON 对象时返回 null —— 那种文件走文本一路，
 * 勾选的是 unified diff 的 @@ 块而不是 selector。
 */
export function splitPoints(base: string, mine: string): OverridePoint[] | null {
  const b = asObject(base)
  const m = asObject(mine)
  if (!b || !m) return null
  const out: OverridePoint[] = []
  walk('', b, m, out)
  return out
}

function walk(prefix: string, base: JSONObject, mine: JSONObject, out: OverridePoint[]) {
  const keys = [...new Set([...Object.keys(base), ...Object.keys(mine)])].sort()
  for (const k of keys) {
    const sel = prefix ? `${prefix}.${escapeSeg(k)}` : escapeSeg(k)
    const hasB = Object.hasOwn(base, k)
    const hasM = Object.hasOwn(mine, k)
    const bv = base[k]
    const mv = mine[k]

    if (hasB && hasM) {
      if (isPlainObject(bv) && isPlainObject(mv)) {
        walk(sel, bv, mv, out)
        continue
      }
      if (canon(bv) === canon(mv)) continue
    }
    out.push({
      selector: sel,
      base: hasB ? JSON.stringify(bv) : '',
      mine: hasM ? JSON.stringify(mv) : '',
    })
  }
}

/**
 * 数 unified diff 里的 @@ 块。块的序号就是后端 overrides.Hunks 的下标
 * ——两侧都用 go-difflib 的 3 行上下文分组（spec §4.2）。
 */
export function diffHunkCount(diff: string): number {
  if (!diff) return 0
  return diff.split('\n').filter((l) => l.startsWith('@@')).length
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd hub/internal/site && npx vitest run src/lib/overridePoints.test.ts`
Expected: PASS

- [ ] **Step 5: 扩 `types/collections.ts`**

（a）`DriftState` 加 `'overridden'`：

```ts
export type DriftState =
  | 'open' | 'adopted' | 'restored' | 'ignored' | 'superseded' | 'overridden'
```

（b）`EventKind` 联合类型里，`'drift.superseded'` 之后追加：

```ts
  | 'override.created'
  | 'override.dropped'
  | 'override.replaced'
  | 'override.kept'
```

（c）在 `IgnoreRuleRecord` 之后加覆盖层的类型：

```ts
export type OverrideKind = 'json_key' | 'text'

/** 空串 = 一切正常；非空 = 需要用户看一眼（M1.8 spec §4.5） */
export type OverrideAttention =
  | ''
  | 'hub_changed'
  | 'merge_conflict'
  | 'path_gone'
  | 'unmergeable'

/** 一条记录 = 一个差异点，不是一个文件（M1.8 spec §2.1） */
export interface MachineOverrideRecord {
  id: string
  machine: string
  path: string
  kind: OverrideKind
  /** json_key：已转义的 sjson 键路径。text 恒为空串 */
  selector: string
  /** 原始 JSON 片段；空串 = 该键在这一侧不存在 */
  base_value: string
  mine_value: string
  base_blob: string
  mine_blob: string
  attention: OverrideAttention
  shadowed_value: string
  shadowed_blob: string
  shadowed_rev: string
  origin_drift: string
  note: string
  created: string
  updated: string
  expand?: {
    machine?: MachineRecord
    base_blob?: BlobRecord
    mine_blob?: BlobRecord
    shadowed_blob?: BlobRecord
  }
}
```

（d）常量表里追加：

```ts
export const COLLECTION_MACHINE_OVERRIDES = 'machine_overrides'
```

- [ ] **Step 6: 写 `stores/overrides.ts`**

形状照抄 `stores/drift.ts`（引用计数 + realtime 订阅），不要另发明一套：

```ts
/**
 * 本机覆盖层 + realtime 订阅。
 *
 * 列表读 PB SDK；建 / 撤 / 保持走 /api/orciny/*（见 lib/api）。
 */

import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import {
  COLLECTION_MACHINE_OVERRIDES,
  type MachineOverrideRecord,
} from '@/types/collections'

export const $overrides = atom<MachineOverrideRecord[]>([])
export const $overridesLoading = atom(true)
export const $overridesError = atom('')
/** 收件箱横幅：需要用户看一眼的条数 */
export const $attentionCount = atom(0)

let subscription: Promise<() => void> | null = null
let refCount = 0

function byPathThenSelector(a: MachineOverrideRecord, b: MachineOverrideRecord) {
  return a.path.localeCompare(b.path) || a.selector.localeCompare(b.selector)
}

function recount(list: MachineOverrideRecord[]) {
  $attentionCount.set(list.filter((o) => o.attention !== '').length)
}

function commit(list: MachineOverrideRecord[]) {
  const sorted = list.slice().sort(byPathThenSelector)
  $overrides.set(sorted)
  recount(sorted)
}

async function load() {
  try {
    const list = await pb
      .collection(COLLECTION_MACHINE_OVERRIDES)
      .getFullList<MachineOverrideRecord>({
        sort: 'path,selector',
        // base_blob / mine_blob 是文本一路的两侧全文，收件箱的三方对比要它们；
        // json_key 一路两侧在 base_value / mine_value 字段里，不必 expand。
        expand: 'machine,base_blob,mine_blob,shadowed_blob',
      })
    commit(list)
    $overridesError.set('')
  } catch (e) {
    $overridesError.set(String(e))
  } finally {
    $overridesLoading.set(false)
  }
}

/** 订阅覆盖层。多个组件共用一份订阅。 */
export function subscribeOverrides() {
  refCount += 1
  if (refCount === 1 && !subscription) {
    void load()
    subscription = pb
      .collection(COLLECTION_MACHINE_OVERRIDES)
      .subscribe<MachineOverrideRecord>('*', (e) => {
        const cur = $overrides.get()
        if (e.action === 'delete') {
          commit(cur.filter((o) => o.id !== e.record.id))
          return
        }
        const idx = cur.findIndex((o) => o.id === e.record.id)
        if (idx === -1) commit([e.record, ...cur])
        else {
          const next = [...cur]
          next[idx] = e.record
          commit(next)
        }
      })
  }
  return () => {
    refCount -= 1
    if (refCount === 0 && subscription) {
      const pending = subscription
      subscription = null
      void pending.then((fn) => fn()).catch(() => {})
    }
  }
}

/** 按路径分组，路径升序；可选按机器过滤。 */
export function overridesByPath(
  list: MachineOverrideRecord[],
  machineId?: string,
): { path: string; items: MachineOverrideRecord[] }[] {
  const map = new Map<string, MachineOverrideRecord[]>()
  for (const o of list) {
    if (machineId && o.machine !== machineId) continue
    const items = map.get(o.path) ?? []
    items.push(o)
    map.set(o.path, items)
  }
  return [...map.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([path, items]) => ({ path, items }))
}
```

- [ ] **Step 7: 扩 `lib/api.ts`**

在 `ignoreDrift` 之后追加：

```ts
/** 一个 event 的勾选结果。缺席 = 全选（M1.8 spec §7） */
export interface OverrideSelection {
  selectors?: string[]
  hunks?: number[]
}

/** 本机保留：把选中的漂移转成覆盖层。 */
export function overrideDrift(
  events: string[],
  points: Record<string, OverrideSelection> = {},
  reviewed: string[] = [],
) {
  return postJSON('/api/orciny/drift/override', { events, points, reviewed })
}

/** 撤掉排除：下次快照这台机器就拿到中台的值。 */
export function dropOverride(id: string) {
  return deleteJSON(`/api/orciny/overrides/${id}`)
}

/** 保持：清掉提醒，横幅上消失。 */
export function keepOverride(id: string) {
  return postJSON(`/api/orciny/overrides/${id}/keep`)
}

/** 恢复受管：原子地置 survey 并删掉该路径的忽略规则。 */
export function remanage(machineId: string, path: string) {
  return postJSON(`/api/orciny/machines/${machineId}/remanage`, { path })
}
```

- [ ] **Step 8: 类型检查与全量前端测试**

Run: `cd hub/internal/site && npx tsc --noEmit && npm test`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add hub/internal/site/src/types hub/internal/site/src/stores/overrides.ts \
        hub/internal/site/src/lib/api.ts hub/internal/site/src/lib/overridePoints.ts \
        hub/internal/site/src/lib/overridePoints.test.ts
git commit -m "feat(site): 覆盖层的类型、store、api 封装与差异点切分

splitPoints 与 Go 侧 overrides.Split 是同一套规则的两份实现，
selector 必须逐字符一致；文本一路的 hunk 下标就是 @@ 块序号
（M1.8 spec §4.1-§4.2、§7）。"
```

---

### Task 10: `OverrideDialog` 组件

**Files:**
- Create: `hub/internal/site/src/components/OverrideDialog.tsx`
- Test: `hub/internal/site/src/components/OverrideDialog.test.tsx`

**Interfaces:**
- Consumes: `splitPoints` / `diffHunkCount` / `OverridePoint`（Task 9）、`OverrideSelection`（Task 9 的 api 类型）、既有的 `parseUnifiedDiff`（`lib/inbox.ts`）。
- Produces:
  - `interface OverrideTarget { id: string; path: string; diff: string; points: OverridePoint[] | null; hunkCount: number }`
  - `function OverrideDialog(props: { targets: OverrideTarget[]; checked: Record<string, Set<string>>; busy: boolean; onToggle(targetId: string, key: string): void; onCancel(): void; onConfirm(): void }): JSX.Element`
  - `function selectionsOf(targets: OverrideTarget[], checked: Record<string, Set<string>>): Record<string, OverrideSelection>`
  - `function defaultChecked(targets: OverrideTarget[]): Record<string, Set<string>>` —— **默认全选**
  - `function checkedCount(checked: Record<string, Set<string>>): number`

**形状参考已有的 `ReviewDialog`**（同一个 `fixed inset-0 … role="dialog"` 骨架，同一套按钮样式），并且**单独成文件、具名导出**——整页要连 store 与 api 一起 mock，组件单独导出才测得动（与 `ReviewDialog` 同一个理由）。

**勾选键的口径：** JSON 一路用 `selector` 当键，文本一路用 `h:<下标>` 当键。`selectionsOf` 把它翻译成 API 的 `{selectors}` / `{hunks}`。一个差异点都没勾 → 确认按钮禁用（等价于什么都不做，不该产生一次空操作，spec §6.2）。

- [ ] **Step 1: 写 `OverrideDialog.test.tsx`（先失败）**

```tsx
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import {
  OverrideDialog,
  defaultChecked,
  selectionsOf,
  type OverrideTarget,
} from '@/components/OverrideDialog'

i18n.load('zh', {})
i18n.activate('zh')

const jsonTarget: OverrideTarget = {
  id: 'e1',
  path: '.claude/settings.json',
  diff: '',
  points: [
    { selector: 'env.A', base: '"中台"', mine: '"本机"' },
    { selector: 'env.B', base: '"旧"', mine: '"新"' },
  ],
  hunkCount: 0,
}

const textTarget: OverrideTarget = {
  id: 'e2',
  path: '.claude/CLAUDE.md',
  diff: ['@@ -1,3 +1,3 @@', '-a', '+A', '@@ -20,3 +20,3 @@', '-z', '+Z'].join('\n'),
  points: null,
  hunkCount: 2,
}

/** 受控组件，测的是真实的勾选联动，不是回调被调了几次。 */
function Harness({
  targets,
  onConfirm = vi.fn(),
}: {
  targets: OverrideTarget[]
  onConfirm?: () => void
}) {
  const [checked, setChecked] = useState(() => defaultChecked(targets))
  return (
    <I18nProvider i18n={i18n}>
      <OverrideDialog
        targets={targets}
        checked={checked}
        busy={false}
        onToggle={(targetId, key) =>
          setChecked((prev) => {
            const next = { ...prev }
            const set = new Set(next[targetId])
            if (set.has(key)) set.delete(key)
            else set.add(key)
            next[targetId] = set
            return next
          })
        }
        onCancel={() => {}}
        onConfirm={onConfirm}
      />
    </I18nProvider>
  )
}

describe('OverrideDialog', () => {
  it('默认全选', () => {
    render(<Harness targets={[jsonTarget, textTarget]} />)
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes).toHaveLength(4) // 2 个 selector + 2 个 hunk
    for (const b of boxes) expect(b).toBeChecked()
  })

  it('按钮上写着会保留几处', () => {
    render(<Harness targets={[jsonTarget]} />)
    expect(screen.getByRole('button', { name: /保留选中的 2 处/ })).toBeEnabled()
  })

  it('取消勾选后提交的 points 只含勾上的', async () => {
    const user = userEvent.setup()
    render(<Harness targets={[jsonTarget, textTarget]} />)

    await user.click(screen.getByLabelText('env.B'))
    await user.click(screen.getByLabelText('.claude/CLAUDE.md #2'))

    expect(screen.getByLabelText('env.B')).not.toBeChecked()
    expect(screen.getByRole('button', { name: /保留选中的 2 处/ })).toBeEnabled()
  })

  it('全不勾时按钮禁用 —— 空操作不该被提交', async () => {
    const user = userEvent.setup()
    render(<Harness targets={[jsonTarget]} />)
    await user.click(screen.getByLabelText('env.A'))
    await user.click(screen.getByLabelText('env.B'))
    expect(screen.getByRole('button', { name: /保留选中的/ })).toBeDisabled()
  })

  it('JSON 一路左右两列显示 base → mine', () => {
    render(<Harness targets={[jsonTarget]} />)
    expect(screen.getByText('"中台"')).toBeInTheDocument()
    expect(screen.getByText('"本机"')).toBeInTheDocument()
  })
})

describe('selectionsOf', () => {
  it('JSON 出 selectors、文本出 hunks', () => {
    const checked = {
      e1: new Set(['env.A']),
      e2: new Set(['h:1']),
    }
    expect(selectionsOf([jsonTarget, textTarget], checked)).toEqual({
      e1: { selectors: ['env.A'] },
      e2: { hunks: [1] },
    })
  })

  it('hunk 下标按数值升序，不是字符串序', () => {
    const t: OverrideTarget = { ...textTarget, hunkCount: 12 }
    const checked = { e2: new Set(['h:10', 'h:2']) }
    expect(selectionsOf([t], checked)).toEqual({ e2: { hunks: [2, 10] } })
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd hub/internal/site && npx vitest run src/components/OverrideDialog.test.tsx`
Expected: FAIL —— `Failed to resolve import "@/components/OverrideDialog"`

- [ ] **Step 3: 写 `components/OverrideDialog.tsx`**

```tsx
import { Trans, useLingui } from '@lingui/react/macro'
import type { OverridePoint } from '@/lib/overridePoints'
import type { OverrideSelection } from '@/lib/api'

/**
 * 一条待转成覆盖层的漂移。
 * points 非空 = JSON 一路（勾 selector）；points 为 null = 文本一路（勾 hunk）。
 */
export interface OverrideTarget {
  id: string
  path: string
  /** hub 算好的 unified diff，文本一路按 @@ 块切开显示 */
  diff: string
  points: OverridePoint[] | null
  hunkCount: number
}

/** 文本一路的勾选键：h:<hunk 下标>。JSON 一路直接用 selector。 */
function hunkKey(i: number): string {
  return `h:${i}`
}

/** 默认全选（M1.8 spec §6.2）：用户来这里是为了保留，不是为了逐个挑。 */
export function defaultChecked(targets: OverrideTarget[]): Record<string, Set<string>> {
  const out: Record<string, Set<string>> = {}
  for (const t of targets) {
    out[t.id] = t.points
      ? new Set(t.points.map((p) => p.selector))
      : new Set(Array.from({ length: t.hunkCount }, (_, i) => hunkKey(i)))
  }
  return out
}

export function checkedCount(checked: Record<string, Set<string>>): number {
  return Object.values(checked).reduce((n, s) => n + s.size, 0)
}

/** 把勾选翻译成 API 的 points。空集合的 target 直接不出现 = 该条不做。 */
export function selectionsOf(
  targets: OverrideTarget[],
  checked: Record<string, Set<string>>,
): Record<string, OverrideSelection> {
  const out: Record<string, OverrideSelection> = {}
  for (const t of targets) {
    const set = checked[t.id]
    if (!set || set.size === 0) continue
    if (t.points) {
      out[t.id] = {
        selectors: t.points.map((p) => p.selector).filter((s) => set.has(s)),
      }
    } else {
      out[t.id] = {
        hunks: Array.from({ length: t.hunkCount }, (_, i) => i).filter((i) =>
          set.has(hunkKey(i)),
        ),
      }
    }
  }
  return out
}

/** 把 unified diff 按 @@ 切成块，块序号与后端的 hunk 下标一一对应。 */
function diffBlocks(diff: string): string[][] {
  const blocks: string[][] = []
  let cur: string[] | null = null
  for (const line of diff.split('\n')) {
    if (line.startsWith('@@')) {
      cur = []
      blocks.push(cur)
      continue
    }
    if (cur && (line.startsWith('+') || line.startsWith('-') || line.startsWith(' '))) {
      cur.push(line)
    }
  }
  return blocks
}

/**
 * 「本机保留」的勾选弹窗（M1.8 spec §6.2）。
 *
 * 单独成文件并具名导出：整页要连 store 与 api 一起 mock，
 * 组件单独导出才测得动（与 ReviewDialog 同一个理由）。
 */
export function OverrideDialog({
  targets,
  checked,
  busy,
  onToggle,
  onCancel,
  onConfirm,
}: {
  targets: OverrideTarget[]
  checked: Record<string, Set<string>>
  busy: boolean
  onToggle: (targetId: string, key: string) => void
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useLingui()
  const total = checkedCount(checked)

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="dialog"
    >
      <div className="max-h-[85vh] w-full max-w-3xl overflow-auto rounded-lg border border-line bg-surface p-5 shadow-xl">
        <h2 className="mb-2 text-base font-semibold">
          <Trans>本机保留</Trans>
        </h2>
        <p className="mb-4 text-sm text-ink3">
          <Trans>
            勾上的差异点归这台机器所有，中台以后改到别处照旧同步过来。
            默认全选，取消勾选的那些会退回中台的值。
          </Trans>
        </p>

        <ul className="mb-4 space-y-4">
          {targets.map((target) => (
            <li key={target.id} className="rounded border border-line">
              <div className="border-b border-line px-3 py-1.5 font-mono text-xs break-all">
                {target.path}
              </div>
              {target.points
                ? renderPoints(target, checked[target.id], onToggle)
                : renderHunks(target, checked[target.id], onToggle)}
            </li>
          ))}
        </ul>

        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded bg-wash px-3 py-1.5 text-sm"
            onClick={onCancel}
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={busy || total === 0}
            className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white disabled:opacity-40"
            onClick={onConfirm}
          >
            {t`保留选中的 ${total} 处`}
          </button>
        </div>
      </div>
    </div>
  )
}

function renderPoints(
  target: OverrideTarget,
  checked: Set<string> | undefined,
  onToggle: (targetId: string, key: string) => void,
) {
  return (
    <table className="w-full border-collapse font-mono text-xs">
      <tbody>
        {target.points?.map((p) => (
          <tr key={p.selector} className="border-b border-line/60 last:border-0">
            <td className="w-8 px-3 py-1.5 align-top">
              <input
                type="checkbox"
                aria-label={p.selector}
                checked={checked?.has(p.selector) ?? false}
                onChange={() => onToggle(target.id, p.selector)}
              />
            </td>
            <td className="px-1 py-1.5 align-top break-all">{p.selector}</td>
            <td className="px-1 py-1.5 align-top break-all text-ink3">
              {p.base === '' ? <Trans>（无此键）</Trans> : p.base}
            </td>
            <td className="w-4 px-1 py-1.5 align-top text-ink3">→</td>
            <td className="px-3 py-1.5 align-top break-all text-accent">
              {p.mine === '' ? <Trans>（删掉这个键）</Trans> : p.mine}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function renderHunks(
  target: OverrideTarget,
  checked: Set<string> | undefined,
  onToggle: (targetId: string, key: string) => void,
) {
  const blocks = diffBlocks(target.diff)
  return (
    <ul className="divide-y divide-line/60">
      {blocks.map((block, i) => (
        <li key={i} className="flex gap-2 px-3 py-2">
          <input
            type="checkbox"
            className="mt-1"
            aria-label={`${target.path} #${i + 1}`}
            checked={checked?.has(hunkKey(i)) ?? false}
            onChange={() => onToggle(target.id, hunkKey(i))}
          />
          <pre className="min-w-0 flex-1 overflow-auto font-mono text-xs leading-5">
            {block.map((line, j) => (
              <div
                key={j}
                className={
                  line.startsWith('+')
                    ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                    : line.startsWith('-')
                      ? 'bg-rose-500/10 text-rose-700 dark:text-rose-300'
                      : 'text-ink3'
                }
              >
                {line}
              </div>
            ))}
          </pre>
        </li>
      ))}
    </ul>
  )
}
```

本组件按 `@@` 分块（勾选的单位是块而不是行），因此**不要 import `parseUnifiedDiff`**——`diffBlocks` 已经承担了那件事。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd hub/internal/site && npx vitest run src/components/OverrideDialog.test.tsx`
Expected: PASS（7 个用例）

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site/src/components/OverrideDialog.tsx \
        hub/internal/site/src/components/OverrideDialog.test.tsx
git commit -m "feat(site): OverrideDialog —— 差异点勾选弹窗

JSON 一行一个 selector 左右两列，文本一个 @@ 块一个复选框；
默认全选，一个都没勾时确认按钮禁用（M1.8 spec §6.2）。"
```

---

### Task 11: 收件箱 —— 操作条改造与撞车横幅

**Files:**
- Create: `hub/internal/site/src/components/AttentionBanner.tsx`
- Test: `hub/internal/site/src/components/AttentionBanner.test.tsx`
- Modify: `hub/internal/site/src/pages/Inbox.tsx`
- Test: `hub/internal/site/src/pages/Inbox.test.tsx`

**Interfaces:**
- Consumes: `OverrideDialog` / `defaultChecked` / `selectionsOf` / `OverrideTarget`（Task 10）、`splitPoints` / `diffHunkCount`（Task 9）、`$overrides` / `$attentionCount` / `subscribeOverrides`（Task 9）、`overrideDrift` / `dropOverride` / `keepOverride` / `getBlob`（Task 9 与既有 api）。
- Produces:
  - `function AttentionBanner(props: { items: MachineOverrideRecord[]; machineName: Map<string, string>; busy: boolean; onDrop(id: string): void; onKeep(id: string): void; onCompare(o: MachineOverrideRecord): void }): JSX.Element | null`
  - `const ATTENTION_TEXT: Record<Exclude<OverrideAttention, ''>, () => string>` —— 四段文案集中在一处常量（spec R3）
  - `Inbox` 的 `FILTERS` 增加 `{ key: 'overridden', label: <Trans>已本机保留</Trans> }`

**操作条的新形状（spec §6.1）：**

```
[收编] [恢复] [本机保留] [不再管这个路径 ▾] [三方对比] [取消选择]
```

- **本机保留** 是新的主推动作，样式与「收编」同级（`bg-emerald-600`，即 `bg-accent` 之外的第二强调）。
- **不再管这个路径** 是原「忽略」改名，`title` 写明后果：「中台不再向这台机器下发该路径，也不再提醒。想只保留几处差异请用「本机保留」」。
- **「全局」勾选框收进它的下拉里**，不再平铺在操作条上——它是个重量级动作，不该和常用动作抢同一层视觉权重。
- 在制品里那句 `title={t\`忽略该路径的本机漂移提醒；远端发布仍会继续同步\`}` **是错的**（见 spec §8.1，Task 1 已回退代码），一并改掉。

**横幅（spec §6.3）：** `attention !== ''` 的条数 > 0 时显示「**N 处本机覆盖挡下了中台更新**——展开」。展开后每条一行：机器 / 路径 / selector（或 hunk 数）/ 情形说明，行内两个动作「撤掉排除」「保持」，外加「三方对比」（复用已有的 `ThreeWayCompare`：基线 theirs / 排除那一刻的基线 base / 本机 mine）。

- [ ] **Step 1: 写 `AttentionBanner.test.tsx`（先失败）**

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { AttentionBanner } from '@/components/AttentionBanner'
import type { MachineOverrideRecord } from '@/types/collections'

i18n.load('zh', {})
i18n.activate('zh')

function ov(over: Partial<MachineOverrideRecord> = {}): MachineOverrideRecord {
  return {
    id: 'o1', machine: 'm1', path: '.claude/settings.json', kind: 'json_key',
    selector: 'env.A', base_value: '"中台"', mine_value: '"本机"',
    base_blob: '', mine_blob: '', attention: 'hub_changed',
    shadowed_value: '"中台的新值"', shadowed_blob: '', shadowed_rev: 'r2',
    origin_drift: 'e1', note: '', created: '2026-09-01T00:00:00Z',
    updated: '2026-09-01T00:00:00Z',
    ...over,
  }
}

function view(items: MachineOverrideRecord[], handlers: Partial<{
  onDrop: (id: string) => void
  onKeep: (id: string) => void
  onCompare: (o: MachineOverrideRecord) => void
}> = {}) {
  return render(
    <I18nProvider i18n={i18n}>
      <AttentionBanner
        items={items}
        machineName={new Map([['m1', '主力机']])}
        busy={false}
        onDrop={handlers.onDrop ?? vi.fn()}
        onKeep={handlers.onKeep ?? vi.fn()}
        onCompare={handlers.onCompare ?? vi.fn()}
      />
    </I18nProvider>,
  )
}

describe('AttentionBanner', () => {
  it('没有需要看的条目时什么都不渲染', () => {
    const { container } = view([])
    expect(container).toBeEmptyDOMElement()
  })

  it('有条目时显示计数', () => {
    view([ov(), ov({ id: 'o2', selector: 'env.B' })])
    expect(screen.getByText(/2 处本机覆盖挡下了中台更新/)).toBeInTheDocument()
  })

  it('展开后每条一行，带机器名与路径', async () => {
    const user = userEvent.setup()
    view([ov()])
    await user.click(screen.getByRole('button', { name: /展开/ }))
    expect(screen.getByText('主力机')).toBeInTheDocument()
    expect(screen.getByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText('env.A')).toBeInTheDocument()
  })

  it('「撤掉排除」与「保持」各自调对回调', async () => {
    const user = userEvent.setup()
    const onDrop = vi.fn()
    const onKeep = vi.fn()
    view([ov()], { onDrop, onKeep })
    await user.click(screen.getByRole('button', { name: /展开/ }))

    await user.click(screen.getByRole('button', { name: /撤掉排除/ }))
    expect(onDrop).toHaveBeenCalledWith('o1')

    await user.click(screen.getByRole('button', { name: /保持/ }))
    expect(onKeep).toHaveBeenCalledWith('o1')
  })

  it('四种情形各说各的话', async () => {
    const user = userEvent.setup()
    view([
      ov({ id: 'a', attention: 'hub_changed' }),
      ov({ id: 'b', attention: 'merge_conflict', selector: '' , kind: 'text' }),
      ov({ id: 'c', attention: 'path_gone', selector: 'x' }),
      ov({ id: 'd', attention: 'unmergeable', selector: 'y' }),
    ])
    await user.click(screen.getByRole('button', { name: /展开/ }))
    expect(screen.getByText(/中台也改了同一处/)).toBeInTheDocument()
    expect(screen.getByText(/两边改法不同/)).toBeInTheDocument()
    expect(screen.getByText(/已不在中台的下发范围里/)).toBeInTheDocument()
    expect(screen.getByText(/不是合法的 JSON 对象/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd hub/internal/site && npx vitest run src/components/AttentionBanner.test.tsx`
Expected: FAIL —— `Failed to resolve import "@/components/AttentionBanner"`

- [ ] **Step 3: 写 `components/AttentionBanner.tsx`**

```tsx
import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import type { MachineOverrideRecord, OverrideAttention } from '@/types/collections'

/**
 * 四段文案集中在一处，便于统一措辞（M1.8 spec R3）。
 * 横幅只显示计数与一句归纳，详情在展开行里。
 */
function attentionText(a: OverrideAttention, t: (s: TemplateStringsArray) => string): string {
  switch (a) {
    case 'hub_changed':
      return t`中台也改了同一处，本机的值挡下了它`
    case 'merge_conflict':
      return t`两边改法不同，已取本机的说法`
    case 'path_gone':
      return t`这个路径已不在中台的下发范围里，本轮没有生效`
    case 'unmergeable':
      return t`中台这份内容不是合法的 JSON 对象，本轮放弃合并`
    default:
      return ''
  }
}

/**
 * 收件箱顶部的撞车横幅（M1.8 spec §6.3）。
 *
 * 中台赢不了，但必须说话：覆盖层一律取本机，这里只负责让用户知道
 * 有哪些中台更新被挡下了，以及给两个出口。
 */
export function AttentionBanner({
  items,
  machineName,
  busy,
  onDrop,
  onKeep,
  onCompare,
}: {
  items: MachineOverrideRecord[]
  machineName: Map<string, string>
  busy: boolean
  onDrop: (id: string) => void
  onKeep: (id: string) => void
  onCompare: (o: MachineOverrideRecord) => void
}) {
  const { t } = useLingui()
  const [open, setOpen] = useState(false)
  if (items.length === 0) return null

  return (
    <section className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <p className="flex-1 text-sm font-medium text-amber-800 dark:text-amber-200">
          <Trans>{items.length} 处本机覆盖挡下了中台更新</Trans>
        </p>
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="rounded bg-wash px-2 py-1 text-xs"
        >
          {open ? <Trans>收起</Trans> : <Trans>展开</Trans>}
        </button>
      </div>

      {open && (
        <ul className="mt-3 space-y-2">
          {items.map((o) => (
            <li
              key={o.id}
              className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded border border-line bg-surface px-3 py-2 text-xs"
            >
              <span className="text-ink2">{machineName.get(o.machine) ?? o.machine}</span>
              <span className="font-mono break-all">{o.path}</span>
              <span className="font-mono text-ink3">
                {o.kind === 'json_key' ? o.selector : t`整份文件`}
              </span>
              <span className="flex-1 text-ink3">{attentionText(o.attention, t)}</span>
              <button
                type="button"
                disabled={busy}
                onClick={() => onCompare(o)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>三方对比</Trans>
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => onDrop(o.id)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>撤掉排除</Trans>
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => onKeep(o.id)}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>保持</Trans>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd hub/internal/site && npx vitest run src/components/AttentionBanner.test.tsx`
Expected: PASS

- [ ] **Step 5: 写 `Inbox.test.tsx` 的新用例（先失败）**

追加到 `hub/internal/site/src/pages/Inbox.test.tsx`（沿用文件顶部已有的 i18n 初始化）：

```tsx
import { buildOverrideTargets } from '@/pages/Inbox'
import type { DriftEventRecord } from '@/types/collections'

describe('buildOverrideTargets', () => {
  const rec = (over: Partial<DriftEventRecord> = {}): DriftEventRecord => ({
    id: 'e1', machine: 'm1', config_set: 's1', path: '.claude/settings.json',
    kind: 'modified', base_hash: 'h-base', current_blob: 'b1', mode: 420,
    diff: '', restore_partial: false, truncated: false, binding_drift: false,
    binding_url: '', state: 'open', resolved_revision: '', resolved_at: '',
    created: '2026-09-01T00:00:00Z', updated: '2026-09-01T00:00:00Z',
    ...over,
  })

  it('两侧都是 JSON 对象 → 切成 selector 列表', () => {
    const [target] = buildOverrideTargets(
      [rec()],
      { 'h-base': '{"env":{"A":"中台"}}' },
      { e1: '{"env":{"A":"本机"}}' },
    )
    expect(target.points?.map((p) => p.selector)).toEqual(['env.A'])
    expect(target.hunkCount).toBe(0)
  })

  it('非 JSON → points 为 null，按 @@ 块数给 hunkCount', () => {
    const diff = ['@@ -1,3 +1,3 @@', '-a', '+A'].join('\n')
    const [target] = buildOverrideTargets(
      [rec({ diff })],
      { 'h-base': '# 标题' },
      { e1: '# 新标题' },
    )
    expect(target.points).toBeNull()
    expect(target.hunkCount).toBe(1)
  })
})
```

- [ ] **Step 6: 跑测试确认失败**

Run: `cd hub/internal/site && npx vitest run src/pages/Inbox.test.tsx`
Expected: FAIL —— `buildOverrideTargets is not exported`

- [ ] **Step 7: 改 `pages/Inbox.tsx`**

（a）import 补：

```tsx
import {
  $overrides,
  subscribeOverrides,
} from '@/stores/overrides'
import { AttentionBanner } from '@/components/AttentionBanner'
import {
  OverrideDialog,
  defaultChecked,
  selectionsOf,
  type OverrideTarget,
} from '@/components/OverrideDialog'
import { diffHunkCount, splitPoints } from '@/lib/overridePoints'
import {
  dropOverride,
  keepOverride,
  overrideDrift,
} from '@/lib/api'
import type { MachineOverrideRecord } from '@/types/collections'
```

（b）`FILTERS` 里，`{ key: 'ignored', … }` 之前插入：

```tsx
  { key: 'overridden', label: <Trans>已本机保留</Trans> },
```

并把 `ignored` 那条的 label 从 `<Trans>已忽略</Trans>` 改成 `<Trans>已退管</Trans>`——两者在筛选器里是两个不同的去向，不合并（spec §2.2）。

（c）导出一个纯函数，供测试直接调（与 `ReviewDialog` 同一个理由）：

```tsx
/**
 * 把选中的漂移变成弹窗要的 target。
 *
 * baseByHash / curById 是已经取回来的内容（base_hash → 基线内容、
 * event id → 本机内容）。两侧都是 JSON 对象就切 selector，否则走文本一路
 * ——hunk 下标就是 diff 里 @@ 块的序号，与后端的 overrides.Hunks 一一对应。
 */
export function buildOverrideTargets(
  events: DriftEventRecord[],
  baseByHash: Record<string, string>,
  curById: Record<string, string>,
): OverrideTarget[] {
  return events.map((e) => {
    const base = baseByHash[e.base_hash] ?? ''
    const cur = curById[e.id] ?? ''
    const points = splitPoints(base, cur)
    return {
      id: e.id,
      path: e.path,
      diff: e.diff ?? '',
      points,
      hunkCount: points ? 0 : diffHunkCount(e.diff ?? ''),
    }
  })
}
```

（d）在 `Inbox` 组件里加状态与三个 handler：

```tsx
  const overrides = useStore($overrides)
  useEffect(() => subscribeOverrides(), [])

  const [overriding, setOverriding] = useState<OverrideTarget[] | null>(null)
  const [overrideChecked, setOverrideChecked] = useState<Record<string, Set<string>>>({})
  const [ignoreMenu, setIgnoreMenu] = useState(false)

  const attention = useMemo(
    () => overrides.filter((o) => o.attention !== ''),
    [overrides],
  )

  /** 本机保留：取回两侧内容 → 切差异点 → 开弹窗（默认全选）。 */
  async function handleOverride() {
    if (selected.size === 0) return
    setBusy(true)
    setActionErr('')
    try {
      const recs = [...selected].map((id) => byId.get(id)).filter(Boolean) as DriftEventRecord[]
      const baseByHash: Record<string, string> = {}
      const curById: Record<string, string> = {}
      for (const r of recs) {
        if (r.base_hash && !(r.base_hash in baseByHash)) {
          baseByHash[r.base_hash] = await getBlob(r.base_hash).catch(() => '')
        }
        curById[r.id] = await loadCurrent(r)
      }
      const targets = buildOverrideTargets(recs, baseByHash, curById)
      setOverriding(targets)
      setOverrideChecked(defaultChecked(targets))
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function doOverride() {
    if (!overriding) return
    setBusy(true)
    setActionErr('')
    try {
      const points = selectionsOf(overriding, overrideChecked)
      // restore_partial 的条目必须列进 reviewed —— 与收编同一条规矩。
      const reviewed = overriding
        .filter((tg) => byId.get(tg.id)?.restore_partial)
        .map((tg) => tg.id)
      await overrideDrift(Object.keys(points), points, reviewed)
      setOverriding(null)
      setOverrideChecked({})
      clearSelection()
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleAttentionAction(fn: (id: string) => Promise<unknown>, id: string) {
    setBusy(true)
    setActionErr('')
    try {
      await fn(id)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /**
   * 三方对比：中台现值（theirs）/ 排除那一刻的基线（base）/ 本机（mine）。
   *
   * 两种 kind 的三份内容存在**不同的地方**：json_key 在 base_value /
   * mine_value / shadowed_value 三个文本字段里，text 在三个 blob 关系里。
   * 读错地方会得到三个空白面板。
   */
  async function openOverrideCompare(o: MachineOverrideRecord) {
    setBusy(true)
    setActionErr('')
    try {
      const blobText = async (hash?: string) =>
        hash ? await getBlob(hash).catch(() => '') : ''
      const isText = o.kind === 'text'
      const theirs = isText
        ? await blobText(o.expand?.shadowed_blob?.hash)
        : o.shadowed_value
      const base = isText ? await blobText(o.expand?.base_blob?.hash) : o.base_value
      const mine = isText ? await blobText(o.expand?.mine_blob?.hash) : o.mine_value

      setCompare({
        path: o.path,
        base: theirs,
        left: { label: t`排除那一刻的基线`, content: base },
        right: { label: machineName.get(o.machine) ?? o.machine, content: mine },
      })
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
```

（e）在页面标题下方、错误提示上方插入横幅：

```tsx
      <AttentionBanner
        items={attention}
        machineName={machineName}
        busy={busy}
        onDrop={(id) => void handleAttentionAction(dropOverride, id)}
        onKeep={(id) => void handleAttentionAction(keepOverride, id)}
        onCompare={(o) => void openOverrideCompare(o)}
      />
```

（f）重做操作条。删掉平铺的「全局忽略」`<label>`，把「忽略路径」按钮换成下面这一组（顺序按 spec §6.1：收编 / 恢复 / 本机保留 / 不再管这个路径 / 三方对比 / 取消选择）：

```tsx
            <button
              type="button"
              disabled={busy || selected.size === 0}
              onClick={() => void handleOverride()}
              title={t`勾出的差异点归这台机器所有，中台以后改到别处照旧同步过来`}
              className="rounded bg-emerald-600 px-3 py-1.5 text-xs text-white disabled:opacity-40"
            >
              <Trans>本机保留</Trans>
            </button>

            <div className="relative">
              <button
                type="button"
                disabled={busy}
                onClick={() => setIgnoreMenu((v) => !v)}
                title={t`中台不再向这台机器下发该路径，也不再提醒。想只保留几处差异请用「本机保留」`}
                className="rounded bg-wash px-3 py-1.5 text-xs"
              >
                <Trans>不再管这个路径 ▾</Trans>
              </button>
              {ignoreMenu && (
                <div className="absolute bottom-full right-0 mb-1 w-64 rounded border border-line bg-surface p-3 text-xs shadow-lg">
                  <p className="mb-2 text-ink3">
                    <Trans>
                      中台不再向这台机器下发该路径，也不再提醒。
                      想只保留几处差异请用「本机保留」。
                    </Trans>
                  </p>
                  <label className="mb-2 flex items-center gap-1 text-ink2">
                    <input
                      type="checkbox"
                      checked={globalIgnore}
                      onChange={(e) => setGlobalIgnore(e.target.checked)}
                    />
                    <Trans>对全机队生效</Trans>
                  </label>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      setIgnoreMenu(false)
                      void handleIgnore()
                    }}
                    className="w-full rounded bg-wash px-2 py-1"
                  >
                    <Trans>确认退管</Trans>
                  </button>
                </div>
              )}
            </div>
```

（g）在 `reviewing` 弹窗之后挂上 `OverrideDialog`：

```tsx
      {overriding && (
        <OverrideDialog
          targets={overriding}
          checked={overrideChecked}
          busy={busy}
          onToggle={(targetId, key) => {
            setOverrideChecked((prev) => {
              const next = { ...prev }
              const set = new Set(next[targetId])
              if (set.has(key)) set.delete(key)
              else set.add(key)
              next[targetId] = set
              return next
            })
          }}
          onCancel={() => {
            setOverriding(null)
            setOverrideChecked({})
          }}
          onConfirm={() => void doOverride()}
        />
      )}
```

- [ ] **Step 8: 跑测试与类型检查**

Run: `cd hub/internal/site && npx tsc --noEmit && npm test`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add hub/internal/site/src/pages/Inbox.tsx hub/internal/site/src/pages/Inbox.test.tsx \
        hub/internal/site/src/components/AttentionBanner.tsx \
        hub/internal/site/src/components/AttentionBanner.test.tsx
git commit -m "feat(site): 收件箱拆出「本机保留」，加撞车横幅

「忽略」改名「不再管这个路径」并把「全局」收进下拉——它是个重量级动作，
不该和常用动作抢同一层视觉权重；在制品里那句「远端发布仍会继续同步」
的错误 title 一并改掉（M1.8 spec §6.1、§6.3、§8.1）。"
```

---

### Task 12: 机器详情页两个区块 + 机器列表角标

**Files:**
- Create: `hub/internal/site/src/components/MachineOverrides.tsx`
- Create: `hub/internal/site/src/components/UnmanagedPaths.tsx`
- Test: `hub/internal/site/src/components/MachineOverrides.test.tsx`
- Test: `hub/internal/site/src/components/UnmanagedPaths.test.tsx`
- Modify: `hub/internal/site/src/pages/MachineDetail.tsx`
- Modify: `hub/internal/site/src/pages/Machines.tsx`

**Interfaces:**
- Consumes: `$overrides` / `subscribeOverrides` / `overridesByPath`（Task 9）、`dropOverride` / `remanage`（Task 9）、`COLLECTION_IGNORE_RULES` 与 `IgnoreRuleRecord`（既有类型）。
- Produces:
  - `function MachineOverrides(props: { machineId: string }): JSX.Element`
  - `function UnmanagedPaths(props: { machineId: string }): JSX.Element`
  - `MachineDetail` 的 `eventLabel` 增加四条 `override.*` 的中文标签

**两个区块（spec §6.4）：**
- **本机覆盖**（`machine_overrides` 按 path 分组）：路径 / 差异点数 / 建立时间 / 逐条删除 / 有 `attention` 的高亮。
- **不再受管的路径**（该机器的 `ignore_rules` + 全局的）：路径 / 来源（机器级 / 全局）/「恢复受管」按钮。这是 spec §3.1 里存量用户的补救入口。全局规则不给「恢复受管」按钮——后端会返回 `ErrGlobalRule`，前端先在 UI 上说清楚，别让用户点了才知道。

**R1 的角标。** spec 说「机器列表的对齐状态列在有覆盖层时加一个角标」，但 `Machines.tsx` 目前**没有**对齐状态列（只有名称 / 状态 / 系统 / agent 版本 / Claude Code / 模型 / 最后心跳）。因此角标挂在**状态列**的 `StatusDot` 旁边——它就是那一列在视觉上承担「这台机器好不好」的位置。同一枚角标也出现在机器详情页「配置对齐状态」卡片里。

- [ ] **Step 1: 写两个区块的测试（先失败）**

`MachineOverrides.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { $overrides } from '@/stores/overrides'
import { MachineOverrides } from '@/components/MachineOverrides'
import type { MachineOverrideRecord } from '@/types/collections'

i18n.load('zh', {})
i18n.activate('zh')

vi.mock('@/stores/overrides', async (orig) => {
  const mod = await orig<typeof import('@/stores/overrides')>()
  return { ...mod, subscribeOverrides: () => () => {} }
})

const dropOverride = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ dropOverride: (id: string) => dropOverride(id) }))

function ov(over: Partial<MachineOverrideRecord> = {}): MachineOverrideRecord {
  return {
    id: 'o1', machine: 'm1', path: '.claude/settings.json', kind: 'json_key',
    selector: 'env.A', base_value: '"中台"', mine_value: '"本机"',
    base_blob: '', mine_blob: '', attention: '', shadowed_value: '',
    shadowed_blob: '', shadowed_rev: '', origin_drift: '', note: '',
    created: '2026-09-01T00:00:00Z', updated: '2026-09-01T00:00:00Z',
    ...over,
  }
}

function view() {
  return render(
    <I18nProvider i18n={i18n}>
      <MachineOverrides machineId="m1" />
    </I18nProvider>,
  )
}

beforeEach(() => {
  dropOverride.mockClear()
  $overrides.set([])
})

describe('MachineOverrides', () => {
  it('没有覆盖层时给一句话', () => {
    view()
    expect(screen.getByText(/没有本机覆盖/)).toBeInTheDocument()
  })

  it('按路径分组并显示差异点数', () => {
    $overrides.set([ov(), ov({ id: 'o2', selector: 'env.B' })])
    view()
    expect(screen.getByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText(/2 处/)).toBeInTheDocument()
  })

  it('别的机器的覆盖层不出现', () => {
    $overrides.set([ov({ machine: 'm2', path: '.claude/别的.json' })])
    view()
    expect(screen.queryByText('.claude/别的.json')).not.toBeInTheDocument()
  })

  it('有 attention 的条目高亮并写明原因', () => {
    $overrides.set([ov({ attention: 'hub_changed' })])
    view()
    expect(screen.getByText(/中台也改了同一处/)).toBeInTheDocument()
  })

  it('逐条删除调 dropOverride', async () => {
    const user = userEvent.setup()
    $overrides.set([ov()])
    view()
    await user.click(screen.getByRole('button', { name: /删除/ }))
    expect(dropOverride).toHaveBeenCalledWith('o1')
  })
})
```

`UnmanagedPaths.test.tsx`：

```tsx
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { UnmanagedPaths } from '@/components/UnmanagedPaths'

i18n.load('zh', {})
i18n.activate('zh')

const rules = [
  { id: 'r1', machine: 'm1', path: '.claude/settings.json', note: '', created: '' },
  { id: 'r2', machine: '', path: '.claude/全局.json', note: '', created: '' },
]

vi.mock('@/lib/pb', () => ({
  pb: {
    collection: () => ({ getFullList: () => Promise.resolve(rules) }),
    filter: (s: string) => s,
  },
}))

const remanage = vi.fn().mockResolvedValue(undefined)
vi.mock('@/lib/api', () => ({ remanage: (m: string, p: string) => remanage(m, p) }))

function view() {
  return render(
    <I18nProvider i18n={i18n}>
      <UnmanagedPaths machineId="m1" />
    </I18nProvider>,
  )
}

beforeEach(() => remanage.mockClear())

describe('UnmanagedPaths', () => {
  it('列出机器级与全局的规则，并标明来源', async () => {
    view()
    expect(await screen.findByText('.claude/settings.json')).toBeInTheDocument()
    expect(screen.getByText('.claude/全局.json')).toBeInTheDocument()
    expect(screen.getByText(/全局/)).toBeInTheDocument()
  })

  it('「恢复受管」调对接口', async () => {
    const user = userEvent.setup()
    view()
    await user.click(await screen.findByRole('button', { name: /恢复受管/ }))
    await waitFor(() =>
      expect(remanage).toHaveBeenCalledWith('m1', '.claude/settings.json'),
    )
  })

  it('全局规则不给「恢复受管」按钮 —— 那会悄悄改掉全机队的行为', async () => {
    view()
    await screen.findByText('.claude/全局.json')
    expect(screen.getAllByRole('button', { name: /恢复受管/ })).toHaveLength(1)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd hub/internal/site && npx vitest run src/components/MachineOverrides.test.tsx src/components/UnmanagedPaths.test.tsx`
Expected: FAIL —— 两个组件都不存在

- [ ] **Step 3: 写 `components/MachineOverrides.tsx`**

```tsx
import { useEffect, useMemo, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Trash2 } from 'lucide-react'
import { $overrides, overridesByPath, subscribeOverrides } from '@/stores/overrides'
import { dropOverride } from '@/lib/api'
import type { OverrideAttention } from '@/types/collections'

/** 与 AttentionBanner 同一套措辞（M1.8 spec R3）。 */
function attentionText(a: OverrideAttention, t: (s: TemplateStringsArray) => string): string {
  switch (a) {
    case 'hub_changed':
      return t`中台也改了同一处，本机的值挡下了它`
    case 'merge_conflict':
      return t`两边改法不同，已取本机的说法`
    case 'path_gone':
      return t`这个路径已不在中台的下发范围里`
    case 'unmergeable':
      return t`中台这份内容不是合法的 JSON 对象`
    default:
      return ''
  }
}

/** 机器详情页的「本机覆盖」区块（M1.8 spec §6.4）。 */
export function MachineOverrides({ machineId }: { machineId: string }) {
  const { t } = useLingui()
  const all = useStore($overrides)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => subscribeOverrides(), [])

  const groups = useMemo(() => overridesByPath(all, machineId), [all, machineId])

  async function remove(id: string) {
    setBusy(true)
    setError('')
    try {
      await dropOverride(id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (groups.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>这台机器没有本机覆盖。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-3">
      {error && <p className="text-sm text-rose-600">{error}</p>}
      {groups.map((g) => (
        <section key={g.path} className="rounded border border-line">
          <div className="flex flex-wrap items-baseline gap-2 border-b border-line px-3 py-1.5">
            <span className="font-mono text-xs break-all">{g.path}</span>
            <span className="text-xs text-ink3">
              <Trans>{g.items.length} 处</Trans>
            </span>
          </div>
          <ul className="divide-y divide-line/60">
            {g.items.map((o) => (
              <li
                key={o.id}
                className={`flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-xs ${
                  o.attention !== '' ? 'bg-amber-500/10' : ''
                }`}
              >
                <span className="font-mono">
                  {o.kind === 'json_key' ? o.selector : t`整份文件`}
                </span>
                {o.attention !== '' && (
                  <span className="text-amber-700 dark:text-amber-300">
                    {attentionText(o.attention, t)}
                  </span>
                )}
                <span className="flex-1 text-ink3">
                  {new Date(o.created).toLocaleString()}
                </span>
                <button
                  type="button"
                  disabled={busy}
                  aria-label={t`删除这处覆盖`}
                  onClick={() => void remove(o.id)}
                  className="rounded p-1 text-crit hover:bg-wash"
                >
                  <Trash2 size={14} aria-hidden />
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: 写 `components/UnmanagedPaths.tsx`**

```tsx
import { useCallback, useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { pb } from '@/lib/pb'
import { remanage } from '@/lib/api'
import { COLLECTION_IGNORE_RULES, type IgnoreRuleRecord } from '@/types/collections'

/**
 * 机器详情页的「不再受管的路径」区块（M1.8 spec §6.4）。
 *
 * 这是存量踩坑用户的补救入口（spec §3.1）：点「恢复受管」会**原子地**
 * 把机器打回 survey 并删掉规则——survey 不是可选项，直接恢复受管的话
 * 下一次 apply 会当场用中台版本盖掉他想捞回来的那份改动。
 */
export function UnmanagedPaths({ machineId }: { machineId: string }) {
  const { t } = useLingui()
  const [rules, setRules] = useState<IgnoreRuleRecord[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    void pb
      .collection(COLLECTION_IGNORE_RULES)
      .getFullList<IgnoreRuleRecord>({
        filter: pb.filter('machine = {:m} || machine = ""', { m: machineId }),
        sort: 'path',
      })
      .then(setRules)
      .catch(() => setRules([]))
  }, [machineId])

  useEffect(load, [load])

  async function restore(path: string) {
    setBusy(true)
    setError('')
    try {
      await remanage(machineId, path)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (rules.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>没有退管的路径。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-2">
      {error && <p className="text-sm text-rose-600">{error}</p>}
      <ul className="divide-y divide-line/60">
        {rules.map((r) => (
          <li key={r.id} className="flex flex-wrap items-center gap-3 py-2 text-xs">
            <span className="font-mono break-all">{r.path}</span>
            <span className="flex-1 text-ink3">
              {r.machine ? <Trans>机器级</Trans> : <Trans>全局</Trans>}
            </span>
            {r.machine ? (
              <button
                type="button"
                disabled={busy}
                onClick={() => void restore(r.path)}
                title={t`会把这台机器打回「先看看」模式，差异先进收件箱，再由你决定保留哪几处`}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>恢复受管</Trans>
              </button>
            ) : (
              <span className="text-ink3">
                <Trans>全局规则请到设置里解除</Trans>
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `cd hub/internal/site && npx vitest run src/components/MachineOverrides.test.tsx src/components/UnmanagedPaths.test.tsx`
Expected: PASS

- [ ] **Step 6: 把两个区块挂进 `MachineDetail.tsx`**

（a）import 补：

```tsx
import { MachineOverrides } from '@/components/MachineOverrides'
import { UnmanagedPaths } from '@/components/UnmanagedPaths'
import { $overrides, subscribeOverrides } from '@/stores/overrides'
```

（b）`eventLabel` 里，`'drift.superseded'` 之后追加：

```tsx
  'override.created': <Trans>已本机保留</Trans>,
  'override.dropped': <Trans>本机保留已撤销</Trans>,
  'override.replaced': <Trans>本机保留已替换</Trans>,
  'override.kept': <Trans>本机保留已确认</Trans>,
```

（c）组件里加订阅与计数：

```tsx
  const overrides = useStore($overrides)
  useEffect(() => subscribeOverrides(), [])
  const overrideCount = useMemo(
    () => overrides.filter((o) => o.machine === id).length,
    [overrides, id],
  )
```

（d）「配置对齐状态」卡片里，`stateLabel[assignment.state]` 那一行之后加角标——**光看「已对齐」会以为这台机器和别人一样**（spec R1）：

```tsx
                  {overrideCount > 0 && (
                    <span className="ml-2 rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
                      <Trans>+{overrideCount} 本机覆盖</Trans>
                    </span>
                  )}
```

（e）在「apply 回执历史」之前插入两个区块：

```tsx
      <div className="grid gap-4 md:grid-cols-2">
        <section className="rounded-lg border border-line bg-surface p-5">
          <h2 className="mb-3 text-sm font-semibold">
            <Trans>本机覆盖</Trans>
          </h2>
          <MachineOverrides machineId={id} />
        </section>

        <section className="rounded-lg border border-line bg-surface p-5">
          <h2 className="mb-3 text-sm font-semibold">
            <Trans>不再受管的路径</Trans>
          </h2>
          <UnmanagedPaths machineId={id} />
        </section>
      </div>
```

- [ ] **Step 7: 机器列表加角标（`Machines.tsx`）**

（a）import 补 `import { $overrides, subscribeOverrides } from '@/stores/overrides'`。

（b）组件里：

```tsx
  const overrides = useStore($overrides)
  useEffect(() => subscribeOverrides(), [])
  const overrideCount = useMemo(() => {
    const m = new Map<string, number>()
    for (const o of overrides) m.set(o.machine, (m.get(o.machine) ?? 0) + 1)
    return m
  }, [overrides])
```

（c）状态列的 `<td>` 改成：

```tsx
                <td className="py-2">
                  <StatusDot status={m.status} />
                  {/*
                    有覆盖层的机器即使「已对齐」也和别人不一样。不加这枚角标，
                    用户建了几十个覆盖层之后面板上全是「已对齐」，
                    而机队实际配置各不相同（M1.8 spec R1）。
                  */}
                  {(overrideCount.get(m.id) ?? 0) > 0 && (
                    <span className="ml-2 rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
                      <Trans>+{overrideCount.get(m.id)} 本机覆盖</Trans>
                    </span>
                  )}
                </td>
```

- [ ] **Step 8: 类型检查与全量前端测试**

Run: `cd hub/internal/site && npx tsc --noEmit && npm test`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add hub/internal/site/src/components/MachineOverrides.tsx \
        hub/internal/site/src/components/MachineOverrides.test.tsx \
        hub/internal/site/src/components/UnmanagedPaths.tsx \
        hub/internal/site/src/components/UnmanagedPaths.test.tsx \
        hub/internal/site/src/pages/MachineDetail.tsx \
        hub/internal/site/src/pages/Machines.tsx
git commit -m "feat(site): 机器详情页两个区块与列表角标

「本机覆盖」按路径分组、逐条删除；「不再受管的路径」带「恢复受管」，
是存量踩坑用户的补救入口；有覆盖层的机器加角标——否则面板上全是
「已对齐」而机队实际各不相同（M1.8 spec §6.4、R1）。"
```

---

### Task 13: i18n 抽取与翻译、重新构建 `dist`

**Files:**
- Modify: `hub/internal/site/src/locales/zh.po`
- Modify: `hub/internal/site/src/locales/en.po`
- Modify: `hub/internal/site/src/locales/zh.js`、`en.js`（`lingui compile` 产物）
- Modify: `hub/internal/site/dist/**`（`vite build` 产物，Go 侧 `//go:embed all:dist` 内嵌）

**Interfaces:**
- Consumes: 前面四个前端任务里的全部 `<Trans>` / `t\`\`` 文案。
- Produces: 补齐的 po/js 与重新构建的 `dist`。

**`zh` 是源语言，`en` 需要人工过一遍**（spec §6.5）。两个新词的英文措辞钉死为：

| 中文 | English |
|---|---|
| 本机保留 | Keep local |
| 不再管这个路径 | Stop managing this path |
| 撤掉排除 | Undo override |
| 保持 | Keep as is |
| 恢复受管 | Resume managing |
| 本机覆盖 | Local overrides |
| 不再受管的路径 | Unmanaged paths |
| N 处本机覆盖挡下了中台更新 | N local override(s) blocked a hub update |

按钮的 `title` 长句要与按钮词一致，别一个说 "Keep local" 另一个说 "keep on this machine"。

- [ ] **Step 1: 抽取**

Run: `cd hub/internal/site && npm run extract`
Expected: `zh.po` / `en.po` 里出现本期新增的 msgid，`en.po` 的新条目 msgstr 为空

- [ ] **Step 2: 补齐 `en.po`**

按上表逐条填 `msgstr`。检查没有遗留空 msgstr：

Run: `cd hub/internal/site && grep -c 'msgstr ""' src/locales/en.po`
Expected: 只剩文件头那一条（值为 1）

- [ ] **Step 3: 编译并跑测试**

Run: `cd hub/internal/site && npm run compile && npx tsc --noEmit && npm test`
Expected: PASS

- [ ] **Step 4: 重新构建 `dist`**

Run: `cd hub/internal/site && npm run build`
Expected: `dist/index.html` 与 `dist/assets/*` 更新

- [ ] **Step 5: 跑全量 Go 测试（`dist` 是被 embed 的）**

Run: `go test -tags=testing ./... && go vet ./... && gofmt -l .`
Expected: 测试全过；`gofmt -l` 无输出

- [ ] **Step 6: 提交**

```bash
git add hub/internal/site/src/locales hub/internal/site/dist
git commit -m "chore(site): 补齐 M1.8 的 i18n 并重新构建 dist

「本机保留」= Keep local，「不再管这个路径」= Stop managing this path；
title 长句与按钮词保持一致（M1.8 spec §6.5）。"
```

---

## 落地顺序与依赖

| Task | 依赖 | 能独立跑的测试 |
|---|---|---|
| 1 回退 `plan.go` | — | `./agent/internal/applier/` |
| 2 `merge3` | — | `./hub/internal/merge3/` |
| 3 迁移 `007` | — | `./hub/internal/migrations/` |
| 4 `Split` / `SynthesizeMine` | 2 | `./hub/internal/overrides/` |
| 5 `overrides.Service` | 3, 4 | `./hub/internal/overrides/` |
| 6 接进 `configsync` / GC / 通知 | 5 | `./hub/internal/configsync/`、`./hub/internal/blobs/` |
| 7 `drift.Override` 与护栏 | 5, 6 | `./hub/internal/drift/` |
| 8 四条 API 与装配 | 7 | `./hub/internal/routes/`、`./...` |
| 9 前端基础设施 | 8 | `npm test` |
| 10 `OverrideDialog` | 9 | `npm test` |
| 11 收件箱 | 10 | `npm test` |
| 12 机器详情页与列表 | 9 | `npm test` |
| 13 i18n 与 `dist` | 11, 12 | `make test` |

Task 1–3 互不依赖，可并行开工；其余按表推进。每一步结束时仓库都处在「测试全绿」的状态，不留半成品。
