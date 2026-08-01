# 子计划 05 · manifest、路径安全与两个 HOME

**前置**：02（`protocol.Skip*` 常量）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：顶层 `internal/manifest`（schema、glob、恒排除、路径安全、文件系统展开）、`agent.Config` 的 `managed_home` / `reconcile_interval`、以及把 `testsupport.NewTestAgent` 的签名改成强制传 managed home。

**为什么放顶层 `internal/`**：hub 发布期要用同一套 glob 与恒排除规则做校验（spec §3.2 明确要求两侧各判一次），而 hub 碰不到 `agent/internal/*`。定位同 `internal/clock`：中立于 hub 与 agent，不构成两者之间的依赖。见 00-overview 的偏离记录 #2、#3。

---

### Task 1: schema、恒排除与 glob

**Files:**
- Create: `internal/manifest/manifest.go`
- Test: `internal/manifest/manifest_test.go`

**Interfaces:**
- Produces: `Mode` / `Include` / `Manifest` / `Default` / `Parse` / `Validate` / `JSON` / `AlwaysExcluded` / `IsAlwaysExcluded` / `Match` / `MatchGlob`（逐字符见 00-overview）

**判定顺序（spec §3.2，不可颠倒）**：恒排除 > `manifest.exclude` > `manifest.include`。任何 include 都无法把恒排除路径拉回来。

- [ ] **Step 1: 写失败的测试**

Create `internal/manifest/manifest_test.go`：

```go
package manifest_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
)

func TestDefaultManifestCoversClaudeCode(t *testing.T) {
	m := manifest.Default()
	require.NoError(t, m.Validate())

	var paths []string
	for _, inc := range m.Include {
		paths = append(paths, inc.Path)
	}
	// 路径根是 HOME，不是 ~/.claude —— 因为 ~/.claude.json 在 .claude/ 之外
	// （spec §3.1）。
	require.Equal(t, []string{
		".claude/settings.json",
		".claude/CLAUDE.md",
		".claude/keybindings.json",
		".claude/agents/**",
		".claude/commands/**",
		".claude/skills/**",
		".claude.json",
	}, paths)

	last := m.Include[len(m.Include)-1]
	require.Equal(t, manifest.ModeKeys, last.Mode)
	require.Equal(t, []string{"mcpServers"}, last.Keys)
	require.Equal(t, []string{"**/.DS_Store"}, m.Exclude)
}

func TestParseRoundTrip(t *testing.T) {
	b, err := manifest.Default().JSON()
	require.NoError(t, err)
	got, err := manifest.Parse(b)
	require.NoError(t, err)
	require.Equal(t, manifest.Default(), got)

	// JSON() 必须稳定：同一份 manifest 两次序列化字节相同，
	// 否则冻结进 revision 的字节会无谓地变化。
	b2, err := got.JSON()
	require.NoError(t, err)
	require.Equal(t, b, b2)
}

func TestValidateRejectsBadManifest(t *testing.T) {
	for name, m := range map[string]manifest.Manifest{
		"版本不对":    {Version: 2, Include: []manifest.Include{{Path: "a", Mode: manifest.ModeFile}}},
		"空 include": {Version: 1},
		"未知 mode":  {Version: 1, Include: []manifest.Include{{Path: "a", Mode: "weird"}}},
		"keys 无键":  {Version: 1, Include: []manifest.Include{{Path: "a", Mode: manifest.ModeKeys}}},
		"绝对路径":    {Version: 1, Include: []manifest.Include{{Path: "/etc/passwd", Mode: manifest.ModeFile}}},
		"含 ..":     {Version: 1, Include: []manifest.Include{{Path: "../x", Mode: manifest.ModeFile}}},
		"恒排除路径":   {Version: 1, Include: []manifest.Include{{Path: ".claude/.credentials.json", Mode: manifest.ModeFile}}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, m.Validate())
		})
	}
}

func TestAlwaysExcludedList(t *testing.T) {
	// 这份清单是产品 §4.3 的「恒定不可去除」，改动需要发版。
	require.Equal(t, []string{
		".claude/projects/**",
		".claude/todos/**",
		".claude/shell-snapshots/**",
		".claude/statsig/**",
		".claude/.credentials.json",
		"**/.git/**",
		"**/node_modules/**",
	}, manifest.AlwaysExcluded())
}

func TestIsAlwaysExcluded(t *testing.T) {
	for _, p := range []string{
		".claude/projects/abc/session.jsonl",
		".claude/todos/x.json",
		".claude/shell-snapshots/snap",
		".claude/statsig/cache",
		".claude/.credentials.json",
		".claude/skills/foo/.git/config",
		".claude/skills/foo/node_modules/x/index.js",
	} {
		require.True(t, manifest.IsAlwaysExcluded(p), "%s 必须被恒排除", p)
	}
	for _, p := range []string{
		".claude/settings.json",
		".claude/skills/projects-helper/SKILL.md", // 名字里带 projects 不算
		".claude.json",
	} {
		require.False(t, manifest.IsAlwaysExcluded(p), "%s 不该被恒排除", p)
	}
}

// 任何 include 都拉不回恒排除的路径（spec §3.2）。
func TestMatchPrecedence(t *testing.T) {
	m := manifest.Manifest{
		Version: 1,
		Include: []manifest.Include{
			{Path: ".claude/**", Mode: manifest.ModeTree},
		},
		Exclude: []string{".claude/scratch/**"},
	}
	_, ok := m.Match(".claude/skills/a/SKILL.md")
	require.True(t, ok)

	_, ok = m.Match(".claude/scratch/tmp.md")
	require.False(t, ok, "manifest.exclude 生效")

	_, ok = m.Match(".claude/projects/x.jsonl")
	require.False(t, ok, "恒排除优先于 include")

	_, ok = m.Match(".claude/.credentials.json")
	require.False(t, ok, "恒排除优先于 include")
}

func TestMatchReturnsTheInclude(t *testing.T) {
	m := manifest.Default()
	inc, ok := m.Match(".claude.json")
	require.True(t, ok)
	require.Equal(t, manifest.ModeKeys, inc.Mode)
	require.Equal(t, []string{"mcpServers"}, inc.Keys)

	inc, ok = m.Match(".claude/skills/foo/bar/SKILL.md")
	require.True(t, ok, "** 必须跨层匹配")
	require.Equal(t, manifest.ModeTree, inc.Mode)
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{".claude/skills/**", ".claude/skills/a/b/c.md", true},
		{".claude/skills/**", ".claude/skills", false},
		{".claude/settings.json", ".claude/settings.json", true},
		{".claude/settings.json", ".claude/settings.jsonx", false},
		{"**/.DS_Store", ".claude/skills/.DS_Store", true},
		{"**/.DS_Store", ".DS_Store", true},
		{"**/node_modules/**", "a/node_modules/b/c", true},
		{"**/node_modules/**", "a/node_modules", false},
		{".claude/*.json", ".claude/settings.json", true},
		{".claude/*.json", ".claude/a/b.json", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, manifest.MatchGlob(c.pattern, c.path),
			"MatchGlob(%q, %q)", c.pattern, c.path)
	}
}

func TestJSONShape(t *testing.T) {
	b, err := manifest.Default().JSON()
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	require.EqualValues(t, 1, raw["version"])
	require.Contains(t, raw, "include")
	require.Contains(t, raw, "exclude")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/manifest/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `internal/manifest/manifest.go`：

```go
// Package manifest 定义受管范围的 schema 与判定规则（spec §3）。
//
// 放在顶层 internal/ 而非 agent/internal/：hub 发布期要用同一套 glob 与
// 恒排除规则做校验，而 hub 碰不到 agent/internal/*。定位同 internal/clock
// ——中立于 hub 与 agent，不构成两者之间的依赖。
//
// 路径根一律是 **HOME**，不是 ~/.claude（spec §3.1）：~/.claude.json 在
// .claude/ 之外，用 ../.claude.json 表达既丑又与「禁止 ..」的路径安全规则
// 正面冲突。顺带的收益是 v2 引入 OpenCode / Codex 时直接加 .opencode/**。
package manifest

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

type Mode string

const (
	// ModeFile 单个文件整体受管；漂移比对口径是渲染后全文 hash。
	ModeFile Mode = "file"
	// ModeTree glob 匹配的整棵子树受管，**含新增文件**——这是「在某台机器上
	// 新写一个 skill 能被收编」的支点（spec §3.1）。
	ModeTree Mode = "tree"
	// ModeKeys 只重写 JSON 顶层的指定键，其余键原样保留。
	ModeKeys Mode = "keys"
)

type Include struct {
	Path string   `json:"path"`
	Mode Mode     `json:"mode"`
	Keys []string `json:"keys,omitempty"`
}

type Manifest struct {
	Version int       `json:"version"`
	Include []Include `json:"include"`
	Exclude []string  `json:"exclude,omitempty"`
}

// Default 是导入向导用的出厂 manifest（spec §3.1）。
func Default() Manifest {
	return Manifest{
		Version: 1,
		Include: []Include{
			{Path: ".claude/settings.json", Mode: ModeFile},
			{Path: ".claude/CLAUDE.md", Mode: ModeFile},
			{Path: ".claude/keybindings.json", Mode: ModeFile},
			{Path: ".claude/agents/**", Mode: ModeTree},
			{Path: ".claude/commands/**", Mode: ModeTree},
			{Path: ".claude/skills/**", Mode: ModeTree},
			{Path: ".claude.json", Mode: ModeKeys, Keys: []string{"mcpServers"}},
		},
		Exclude: []string{"**/.DS_Store"},
	}
}

// alwaysExcluded 是恒排除清单（spec §3.2，产品 §4.3 的「恒定不可去除」）。
//
// 它不在 manifest 里，因为「不可去除」不能只是 UI 上的一句话——放进一个
// 用户可编辑的字段就等于可去除。增删这份清单需要发版。
var alwaysExcluded = []string{
	".claude/projects/**",         // 会话历史
	".claude/todos/**",            //
	".claude/shell-snapshots/**",  //
	".claude/statsig/**",          //
	".claude/.credentials.json",   // OAuth 登录态，机器私有
	"**/.git/**",                  //
	"**/node_modules/**",          //
}

func AlwaysExcluded() []string {
	out := make([]string, len(alwaysExcluded))
	copy(out, alwaysExcluded)
	return out
}

func IsAlwaysExcluded(rel string) bool {
	for _, p := range alwaysExcluded {
		if MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

func Parse(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: 解析失败: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// JSON 稳定序列化。冻结进 revision 的就是这份字节。
func (m Manifest) JSON() ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("manifest: 序列化失败: %w", err)
	}
	return b, nil
}

func (m Manifest) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("manifest: version 只支持 1，收到 %d", m.Version)
	}
	if len(m.Include) == 0 {
		return fmt.Errorf("manifest: include 不能为空")
	}
	for _, inc := range m.Include {
		if err := SafeRelPath(strings.TrimSuffix(inc.Path, "/**")); err != nil {
			return fmt.Errorf("manifest: include %q: %w", inc.Path, err)
		}
		switch inc.Mode {
		case ModeFile, ModeTree:
		case ModeKeys:
			if len(inc.Keys) == 0 {
				return fmt.Errorf("manifest: include %q 是 keys 模式但没给 keys", inc.Path)
			}
		default:
			return fmt.Errorf("manifest: include %q 的 mode %q 未知", inc.Path, inc.Mode)
		}
		if IsAlwaysExcluded(inc.Path) {
			return fmt.Errorf("manifest: include %q 属于恒排除路径，不可纳管", inc.Path)
		}
	}
	for _, ex := range m.Exclude {
		if strings.Contains(ex, "..") {
			return fmt.Errorf("manifest: exclude %q 含 ..", ex)
		}
	}
	return nil
}

// Match 报告 rel 命中哪条 include。
//
// 判定顺序恒为：恒排除 > manifest.exclude > manifest.include（spec §3.2）。
// 任何 include 都无法把恒排除路径拉回来。
func (m Manifest) Match(rel string) (Include, bool) {
	if IsAlwaysExcluded(rel) {
		return Include{}, false
	}
	for _, ex := range m.Exclude {
		if MatchGlob(ex, rel) {
			return Include{}, false
		}
	}
	for _, inc := range m.Include {
		if MatchGlob(inc.Path, rel) {
			return inc, true
		}
	}
	return Include{}, false
}

// MatchGlob 支持 * （单层）与 ** （跨层）。
//
// 不用 path.Match：它的 * 会跨过 /，而 ".claude/*.json" 必须只匹配一层。
func MatchGlob(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, seg []string) bool {
	if len(pat) == 0 {
		return len(seg) == 0
	}
	if pat[0] == "**" {
		// ** 至少吃掉一段：".claude/skills/**" 不匹配 ".claude/skills" 本身，
		// 它匹配的是子树里的文件。
		for i := 1; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	}
	if len(seg) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], seg[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], seg[1:])
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/manifest/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/manifest/
git commit -m "feat: manifest schema、恒排除与 glob 匹配"
```

---

### Task 2: 路径安全

**Files:**
- Create: `internal/manifest/safety.go`
- Test: `internal/manifest/safety_test.go`

**Interfaces:**
- Produces:
  ```go
  func SafeRelPath(rel string) error
  func ResolveUnder(root, rel string) (string, error)
  ```

**规则（spec §3.3）**：拒绝绝对路径、拒绝任何 `..` 段；展开后的真实路径必须在 `root` 之下，且 `filepath.EvalSymlinks` 之后再判一次以防符号链接逃逸。

- [ ] **Step 1: 写失败的测试**

Create `internal/manifest/safety_test.go`：

```go
package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
)

func TestSafeRelPath(t *testing.T) {
	for _, ok := range []string{".claude/settings.json", ".claude.json", "a/b/c"} {
		require.NoError(t, manifest.SafeRelPath(ok), "%q 应当合法", ok)
	}
	for _, bad := range []string{
		"", "/etc/passwd", "../escape", "a/../../b", "a/..", "./a/../../b",
		"C:\\Windows\\system32",
	} {
		require.Error(t, manifest.SafeRelPath(bad), "%q 必须被拒", bad)
	}
}

func TestResolveUnderReturnsAbsolutePath(t *testing.T) {
	root := t.TempDir()
	abs, err := manifest.ResolveUnder(root, ".claude/settings.json")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, ".claude/settings.json"), abs)
}

// 符号链接逃逸：EvalSymlinks 之后必须再判一次（spec §3.3）。
func TestResolveUnderRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要特权")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))

	_, err := manifest.ResolveUnder(root, "link/secret")
	require.Error(t, err, "顺着符号链接跑出 root 必须被拒")
}

// 不存在的文件也要能算出路径：apply 的 create 动作正是对不存在的路径落盘。
func TestResolveUnderAllowsMissingFile(t *testing.T) {
	root := t.TempDir()
	abs, err := manifest.ResolveUnder(root, ".claude/agents/new.md")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, ".claude/agents/new.md"), abs)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/manifest/ -run Safe -v`
Expected: FAIL，`undefined: manifest.SafeRelPath`

- [ ] **Step 3: 实现**

Create `internal/manifest/safety.go`：

```go
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafeRelPath 校验一条受管相对路径（spec §3.3）。
// manifest 展开与 apply 落盘两处都要调用它，任一不通过即整体拒绝。
func SafeRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("manifest: 路径为空")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("manifest: 拒绝绝对路径 %q", rel)
	}
	// Windows 盘符：即便当前只支持 linux/darwin，也不该让它悄悄通过。
	if len(rel) >= 2 && rel[1] == ':' {
		return fmt.Errorf("manifest: 拒绝带盘符的路径 %q", rel)
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return fmt.Errorf("manifest: 路径 %q 含 .. 段", rel)
		}
	}
	return nil
}

// ResolveUnder 把受管相对路径解析成绝对路径，并确认它确实在 root 之下。
//
// 两道判定：先做词法校验（SafeRelPath），再对已存在的部分做
// EvalSymlinks —— 只有前者挡不住「root 下有个符号链接指向外面」。
func ResolveUnder(root, rel string) (string, error) {
	if err := SafeRelPath(rel); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("manifest: 解析 root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}

	abs := filepath.Join(rootAbs, filepath.FromSlash(rel))
	if !within(rootAbs, abs) {
		return "", fmt.Errorf("manifest: %q 解析后跑出了 %s", rel, rootAbs)
	}

	// 目标可能还不存在（apply 的 create 动作）。此时对**存在的最深祖先**
	// 求真实路径，符号链接逃逸就是在这一层被抓住的。
	probe := abs
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			suffix := strings.TrimPrefix(abs, probe)
			if !within(rootAbs, filepath.Join(resolved, suffix)) {
				return "", fmt.Errorf("manifest: %q 经符号链接跑出了 %s", rel, rootAbs)
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("manifest: 解析 %q: %w", rel, err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return filepath.Join(rootAbs, filepath.FromSlash(rel)), nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

> `ResolveUnder` 返回的是**未经 EvalSymlinks 的** `filepath.Join(rootAbs, rel)`。这是有意的：落盘要写到用户看到的那个路径上，而不是解析后的真身；解析只用于判定。测试 `TestResolveUnderReturnsAbsolutePath` 在 macOS 上 `t.TempDir()` 会给出 `/var/...`（真身是 `/private/var/...`），因此断言里也要用 `filepath.EvalSymlinks(root)` 之后的值——实现这一步时若断言失败，先确认是不是这个原因，把测试里的 `root` 换成 `evalRoot, _ := filepath.EvalSymlinks(root)` 再比。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/manifest/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/manifest/
git commit -m "feat: 受管路径的安全校验与符号链接逃逸防护"
```

---

### Task 3: 在真实文件系统上展开

**Files:**
- Create: `internal/manifest/expand.go`
- Test: `internal/manifest/expand_test.go`

**Interfaces:**
- Consumes: `protocol.MaxFileSize`、`protocol.Skip*`
- Produces: `Entry` / `Skip` / `Expansion` / `func (m Manifest) Expand(root string) (Expansion, error)`

**规则**

- `file`：路径存在且是常规文件才进 `Files`；不存在则**不报错也不进清单**（apply 的 create 动作靠 revision 清单驱动，不靠展开）。
- `tree`：递归遍历，**含新增文件**。
- `keys`：与 `file` 同样看待（是否存在），mode 由 include 决定。
- 非常规文件（符号链接、FIFO、设备）跳过并记 `SkipNotRegular`；超限记 `SkipTooLarge`；读不了记 `SkipUnreadable`；恒排除记 `SkipAlwaysExcluded`（只在 tree 遍历里出现，因为 include 本身已被 `Validate` 挡住）。

- [ ] **Step 1: 写失败的测试**

Create `internal/manifest/expand_test.go`：

```go
package manifest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

func rels(exp manifest.Expansion) []string {
	out := make([]string, 0, len(exp.Files))
	for _, f := range exp.Files {
		out = append(out, f.Rel)
	}
	return out
}

func TestExpandThreeModes(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/settings.json", `{"model":"opus"}`)
	write(t, root, ".claude/CLAUDE.md", "# 规矩")
	write(t, root, ".claude/skills/foo/SKILL.md", "skill")
	write(t, root, ".claude/skills/bar/nested/deep.md", "deep")
	write(t, root, ".claude.json", `{"mcpServers":{}}`)
	// keybindings.json 不存在：不该报错，也不该进清单
	// agents/ 与 commands/ 不存在：同上

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{
		".claude.json",
		".claude/CLAUDE.md",
		".claude/settings.json",
		".claude/skills/bar/nested/deep.md",
		".claude/skills/foo/SKILL.md",
	}, rels(exp))

	byRel := map[string]manifest.Entry{}
	for _, f := range exp.Files {
		byRel[f.Rel] = f
	}
	require.Equal(t, manifest.ModeKeys, byRel[".claude.json"].Inc.Mode)
	require.Equal(t, manifest.ModeFile, byRel[".claude/settings.json"].Inc.Mode)
	require.Equal(t, manifest.ModeTree, byRel[".claude/skills/foo/SKILL.md"].Inc.Mode)
	require.Equal(t, filepath.Join(root, ".claude/CLAUDE.md"), byRel[".claude/CLAUDE.md"].Abs)
}

// tree 模式含新增文件——这是收编能成立的支点（spec §3.1）。
func TestExpandTreePicksUpNewFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "a")
	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))

	write(t, root, ".claude/skills/新技能/SKILL.md", "b")
	exp, err = manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Len(t, exp.Files, 2)
}

func TestExpandSkipsAlwaysExcluded(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	write(t, root, ".claude/skills/foo/node_modules/x/index.js", "nope")
	write(t, root, ".claude/skills/foo/.git/config", "nope")

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))

	var reasons []string
	for _, s := range exp.Skipped {
		reasons = append(reasons, s.Reason)
	}
	require.NotEmpty(t, exp.Skipped)
	for _, r := range reasons {
		require.Equal(t, protocol.SkipAlwaysExcluded, r)
	}
}

func TestExpandSkipsOversizeFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	big := filepath.Join(root, ".claude/skills/foo/HUGE.md")
	require.NoError(t, os.WriteFile(big, make([]byte, protocol.MaxFileSize+1), 0o644))

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))
	require.Len(t, exp.Skipped, 1)
	require.Equal(t, ".claude/skills/foo/HUGE.md", exp.Skipped[0].Rel)
	require.Equal(t, protocol.SkipTooLarge, exp.Skipped[0].Reason)
}

func TestExpandSkipsNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接与 FIFO 需要特权")
	}
	root := t.TempDir()
	write(t, root, ".claude/skills/foo/SKILL.md", "ok")
	require.NoError(t, os.Symlink(
		filepath.Join(root, ".claude/skills/foo/SKILL.md"),
		filepath.Join(root, ".claude/skills/foo/link.md")))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, ".claude/skills/foo/pipe"), 0o644))

	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Equal(t, []string{".claude/skills/foo/SKILL.md"}, rels(exp))
	require.Len(t, exp.Skipped, 2)
	for _, s := range exp.Skipped {
		require.Equal(t, protocol.SkipNotRegular, s.Reason)
	}
}

func TestExpandIgnoresMissingPaths(t *testing.T) {
	root := t.TempDir()
	exp, err := manifest.Default().Expand(root)
	require.NoError(t, err)
	require.Empty(t, exp.Files)
	require.Empty(t, exp.Skipped, "不存在不算跳过，只是没有")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/manifest/ -run Expand -v`
Expected: FAIL，`m.Expand undefined`

- [ ] **Step 3: 实现**

Create `internal/manifest/expand.go`：

```go
package manifest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/protocol"
)

type Entry struct {
	Rel  string
	Abs  string
	Inc  Include
	Mode os.FileMode
}

type Skip struct {
	Rel    string
	Reason string // protocol.Skip* 之一
}

type Expansion struct {
	Files   []Entry // 按 Rel 升序
	Skipped []Skip  // 按 Rel 升序
}

// Expand 在真实文件系统上展开 manifest。root 是 managed home。
//
// 「不存在」不算跳过，只是没有：apply 的 create 动作由 revision 清单驱动，
// 不靠展开结果。
func (m Manifest) Expand(root string) (Expansion, error) {
	var exp Expansion
	seen := map[string]bool{}

	add := func(rel string, inc Include) {
		if seen[rel] {
			return
		}
		if IsAlwaysExcluded(rel) {
			seen[rel] = true
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipAlwaysExcluded})
			return
		}
		for _, ex := range m.Exclude {
			if MatchGlob(ex, rel) {
				seen[rel] = true
				return // 用户自己排除的，不必报给他看
			}
		}
		abs, err := ResolveUnder(root, rel)
		if err != nil {
			seen[rel] = true
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipUnreadable})
			return
		}
		// Lstat 而不是 Stat：符号链接本身就是要拒的东西，
		// 跟着它走反而会把 root 之外的内容读进来。
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			return
		}
		seen[rel] = true
		if err != nil {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipUnreadable})
			return
		}
		if !info.Mode().IsRegular() {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipNotRegular})
			return
		}
		if info.Size() > protocol.MaxFileSize {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipTooLarge})
			return
		}
		exp.Files = append(exp.Files, Entry{Rel: rel, Abs: abs, Inc: inc, Mode: info.Mode().Perm()})
	}

	for _, inc := range m.Include {
		switch inc.Mode {
		case ModeFile, ModeKeys:
			add(inc.Path, inc)

		case ModeTree:
			base := strings.TrimSuffix(inc.Path, "/**")
			absBase, err := ResolveUnder(root, base)
			if err != nil {
				return Expansion{}, fmt.Errorf("manifest: 展开 %s: %w", inc.Path, err)
			}
			werr := filepath.WalkDir(absBase, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil // 整棵子树不存在
					}
					return nil // 单个目录读不了：跳过，不让整次展开失败
				}
				if d.IsDir() {
					// 恒排除的目录整棵剪掉，省下遍历 node_modules 的开销。
					rel, rerr := filepath.Rel(root, p)
					if rerr == nil && IsAlwaysExcluded(filepath.ToSlash(rel)+"/x") {
						return fs.SkipDir
					}
					return nil
				}
				rel, rerr := filepath.Rel(root, p)
				if rerr != nil {
					return nil
				}
				add(filepath.ToSlash(rel), inc)
				return nil
			})
			if werr != nil {
				return Expansion{}, fmt.Errorf("manifest: 遍历 %s: %w", inc.Path, werr)
			}
		}
	}

	sort.Slice(exp.Files, func(i, j int) bool { return exp.Files[i].Rel < exp.Files[j].Rel })
	sort.Slice(exp.Skipped, func(i, j int) bool { return exp.Skipped[i].Rel < exp.Skipped[j].Rel })
	return exp, nil
}
```

> 剪枝那行 `IsAlwaysExcluded(rel+"/x")` 是因为恒排除清单里的条目形如 `**/node_modules/**`，它匹配的是目录**里的文件**而不是目录本身。拼一个假的子路径去问，比给 `IsAlwaysExcluded` 加一个「目录版」重载要省事，也不会有两套语义。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/manifest/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/manifest/
git commit -m "feat: manifest 在真实文件系统上的展开"
```

---

### Task 4: agent 的两个 HOME

**Files:**
- Modify: `agent/config.go`
- Test: `agent/config_test.go`

**Interfaces:**
- Produces:
  ```go
  // Config 追加两个字段
  ManagedHome       string `yaml:"managed_home"`
  ReconcileInterval string `yaml:"reconcile_interval"`

  func (c *Config) ManagedHomeDir() (string, error)
  func (c *Config) ReconcileEvery() time.Duration
  ```

M0 只有 `$ORCINY_HOME`（agent 自己的数据）。M1 引入第二个：**被管理的 HOME**（spec §2.3）。两者必须分开，否则一个集成测试会去动开发者本人的 `~/.claude`。安装脚本不写这两个字段（留空走默认）。

- [ ] **Step 1: 写失败的测试**

追加到 `agent/config_test.go`：

```go
func TestManagedHomeDefaultsToUserHome(t *testing.T) {
	c := &agent.Config{}
	got, err := c.ManagedHomeDir()
	require.NoError(t, err)
	want, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestManagedHomeIsExplicitWhenSet(t *testing.T) {
	dir := t.TempDir()
	c := &agent.Config{ManagedHome: dir}
	got, err := c.ManagedHomeDir()
	require.NoError(t, err)
	require.Equal(t, dir, got)
}

func TestReconcileEveryDefaultsTo5m(t *testing.T) {
	require.Equal(t, 5*time.Minute, (&agent.Config{}).ReconcileEvery())
	require.Equal(t, 30*time.Second, (&agent.Config{ReconcileInterval: "30s"}).ReconcileEvery())
	// 解不出来就退回默认，不让一个手抖的配置把对账整个关掉
	require.Equal(t, 5*time.Minute, (&agent.Config{ReconcileInterval: "五分钟"}).ReconcileEvery())
	// 过小的值会把 CPU 烧掉，钉到下限
	require.Equal(t, 10*time.Second, (&agent.Config{ReconcileInterval: "1s"}).ReconcileEvery())
}

func TestConfigRoundTripKeepsNewFields(t *testing.T) {
	dir := t.TempDir()
	want := &agent.Config{
		HubURL:            "https://hub.example",
		MachineID:         "m1",
		ManagedHome:       "/tmp/managed",
		ReconcileInterval: "1m",
	}
	require.NoError(t, agent.SaveConfig(dir, want))
	got, err := agent.LoadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/ -run ManagedHome -v`
Expected: FAIL，`c.ManagedHomeDir undefined`

- [ ] **Step 3: 实现**

在 `agent/config.go` 的 `Config` 里追加字段与方法：

```go
	// ManagedHome 是**被管理的** HOME，即 ~/.claude 所在的那个 home
	// （spec §2.3）。空 = os.UserHomeDir()。
	//
	// 它与 $ORCINY_HOME（agent 自己的数据目录）必须分开：测试要把两者都
	// 放临时目录，否则一个集成测试会去动开发者本人的 ~/.claude。
	// 安装脚本不写这个字段。
	ManagedHome string `yaml:"managed_home"`

	// ReconcileInterval 是定时全量对账周期（spec §8.1）。空 = 5m。
	ReconcileInterval string `yaml:"reconcile_interval"`
```

在文件末尾追加：

```go
// DefaultReconcileInterval 是定时全量对账的默认周期（spec §8.1）。
const DefaultReconcileInterval = 5 * time.Minute

// minReconcileInterval 挡住手抖写成 1s 的配置：对账要遍历整棵 skills 子树，
// 太密会把空闲 CPU 从「≈0」变成常驻百分之几。
const minReconcileInterval = 10 * time.Second

// ManagedHomeDir 返回被管理的 HOME。
func (c *Config) ManagedHomeDir() (string, error) {
	if c.ManagedHome != "" {
		return c.ManagedHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("取用户 home 失败，请在 %s 里显式设置 managed_home: %w",
			ConfigFileName, err)
	}
	return home, nil
}

// ReconcileEvery 解析对账周期。解不出来或过小时退回安全值——
// 一个手抖的配置不该把对账整个关掉，也不该把 CPU 烧掉。
func (c *Config) ReconcileEvery() time.Duration {
	if c.ReconcileInterval == "" {
		return DefaultReconcileInterval
	}
	d, err := time.ParseDuration(c.ReconcileInterval)
	if err != nil {
		return DefaultReconcileInterval
	}
	if d < minReconcileInterval {
		return minReconcileInterval
	}
	return d
}
```

import 追加 `"time"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/
git commit -m "feat: agent 配置区分两个 HOME 与对账周期"
```

---

### Task 5: testsupport 强制显式 managed home

**Files:**
- Modify: `internal/testsupport/agent.go`
- Modify: `internal/testsupport/agent_test.go`、`internal/testsupport/lifecycle_test.go`（共 15 处调用）

**Interfaces:**
- Produces:
  ```go
  func NewTestAgent(t *testing.T, th *TestHub, managedHome string) *TestAgent
  // TestAgent 追加字段 ManagedHome string
  ```

spec §2.3 要求：「这条写进 `internal/testsupport` 的构造函数签名里，让人无法忘记」。签名变更会打到 15 处调用，用 sed 统一改。

- [ ] **Step 1: 改签名与实现**

在 `internal/testsupport/agent.go`：

```go
// TestAgent 是一台已完成 enroll 的测试机器：密钥已生成、hub 公钥已钉扎、
// agent.yml 已写好。
type TestAgent struct {
	Dir         string
	ManagedHome string
	IdentityDir string
	Fingerprint string
	MachineID   string
}

// NewTestAgent 走真实的公开 enroll 入口接入给定的 TestHub。
//
// managedHome 必须显式给出（spec §2.3）：它是被管理的 HOME，
// 即 ~/.claude 所在之处。参数而不是可选项，是为了让「忘了隔离」这件事
// 在编译期就发生——一次疏忽会让集成测试去改开发者本人的 ~/.claude。
// 不关心配置管理的用例传 t.TempDir() 即可。
func NewTestAgent(t *testing.T, th *TestHub, managedHome string) *TestAgent {
	t.Helper()
	require.NotEmpty(t, managedHome, "managedHome 必须显式给出")

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

	// enroll 写过 agent.yml 了，这里补上 managed_home。
	cfg, err := agent.LoadConfig(dir)
	require.NoError(t, err, "读回 agent.yml")
	cfg.ManagedHome = managedHome
	require.NoError(t, agent.SaveConfig(dir, cfg), "写回 agent.yml")

	return &TestAgent{
		Dir:         dir,
		ManagedHome: managedHome,
		IdentityDir: filepath.Join(dir, "identity"),
		Fingerprint: res.Fingerprint,
		MachineID:   res.MachineID,
	}
}
```

- [ ] **Step 2: 批量更新 15 处调用**

```bash
sed -i '' 's/testsupport\.NewTestAgent(t, th)/testsupport.NewTestAgent(t, th, t.TempDir())/g' \
  internal/testsupport/agent_test.go internal/testsupport/lifecycle_test.go
grep -rn 'NewTestAgent(' internal agent hub --include='*.go' | grep -v 'func NewTestAgent'
```

Expected: 每一处都带三个实参

- [ ] **Step 3: 跑测试确认通过**

Run: `go test -tags=testing ./...`
Expected: PASS

- [ ] **Step 4: 加一条守卫测试**

追加到 `internal/testsupport/agent_test.go`：

```go
// 两个 HOME 必须真的分开（spec §2.3）。
func TestTestAgentHasSeparateHomes(t *testing.T) {
	th := testsupport.NewTestHub(t)
	managed := t.TempDir()
	ta := testsupport.NewTestAgent(t, th, managed)

	require.Equal(t, managed, ta.ManagedHome)
	require.NotEqual(t, ta.Dir, ta.ManagedHome, "agent 数据目录与受管 HOME 不能是同一个")

	cfg, err := agent.LoadConfig(ta.Dir)
	require.NoError(t, err)
	require.Equal(t, managed, cfg.ManagedHome, "managed_home 必须落进 agent.yml")

	home, err := os.UserHomeDir()
	if err == nil {
		require.NotEqual(t, home, ta.ManagedHome, "测试绝不能指向开发者本人的 home")
	}
}
```

Run: `go test -tags=testing ./internal/testsupport/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/testsupport/
git commit -m "test: NewTestAgent 强制显式指定受管 HOME"
```
