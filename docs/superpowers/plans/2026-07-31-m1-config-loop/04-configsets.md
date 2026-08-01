# 子计划 04 · 配置集、草稿、发布、diff 与回滚

**前置**：01（collection、blobs）、02（`protocol.FileEntry` / `Refs` / `Checksum`）、03（凭据引用校验用得到）、05（`internal/manifest`）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`hub/internal/configsets`（草稿维护、manifest、发布期校验、指派）与 `hub/internal/revisions`（发布、checksum、版本 diff、回滚），以及 `testsupport.SeedConfigSet`。

**核心设计（spec §4.2）**：草稿与发布**共用一套形状**——`config_sets.draft` 与 `revisions.files` 都是 `[]protocol.FileEntry`。于是「草稿 vs head」与「v3 vs v1」是**同一个 diff 函数**，前端也只有一种数据形状要处理。发布 = 把 draft 冻结成一条 revision。

> **注意执行顺序**：本子计划的 `Validate` 依赖 `internal/manifest`（子计划 05）。若 05 尚未完成，先做 05。

---

### Task 1: 配置集与草稿

**Files:**
- Create: `hub/internal/configsets/service.go`
- Test: `hub/internal/configsets/service_test.go`

**Interfaces:**
- Consumes: `blobs.Store`、`events.Writer`、`protocol.FileEntry` / `protocol.Refs`、`manifest.Manifest`
- Produces:
  ```go
  func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service
  func (s *Service) Create(name, note string) (*core.Record, error)
  func (s *Service) SetDraftFile(setID, path string, content []byte, mode uint32, keys []string) (protocol.FileEntry, error)
  func (s *Service) RemoveDraftFile(setID, path string) error
  func (s *Service) Draft(setID string) ([]protocol.FileEntry, error)
  func (s *Service) SetDraft(setID string, files []protocol.FileEntry) error
  func (s *Service) Manifest(setID string) (manifest.Manifest, error)
  func (s *Service) SetManifest(setID string, m manifest.Manifest) error
  ```

**关键行为**：每次改草稿都要重算 `draft_refs`（该草稿引用到的凭据名与变量名），凭据页的「被谁引用」与删除保护都查它（spec §6.5）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/configsets/service_test.go`：

```go
package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

func newService(t *testing.T) (*tests.TestApp, *configsets.Service) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app, configsets.NewService(app, blobs.New(app), events.NewWriter(app))
}

func TestCreateSeedsDefaultManifest(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("主力配置", "从 mac 采集")
	require.NoError(t, err)
	require.Equal(t, "主力配置", set.GetString("name"))
	require.Empty(t, set.GetString("head"), "新建的配置集处于草稿态，head 为空")

	m, err := s.Manifest(set.Id)
	require.NoError(t, err)
	require.Equal(t, manifest.Default(), m)
}

func TestSetDraftFileStoresBlobAndEntry(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	content := []byte("# CLAUDE.md\n")
	e, err := s.SetDraftFile(set.Id, ".claude/CLAUDE.md", content, 0o644, nil)
	require.NoError(t, err)
	require.Equal(t, blobs.Hash(content), e.Hash)
	require.Equal(t, uint32(len(content)), e.Size)
	require.Equal(t, uint32(0o644), e.Mode)

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Len(t, draft, 1)
	require.Equal(t, e, draft[0])

	got, err := blobs.New(app).Get(e.Hash)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestSetDraftFileReplacesSamePath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v1"), 0o644, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v2"), 0o644, nil)
	require.NoError(t, err)

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Len(t, draft, 1, "同一路径只该有一条")
	require.Equal(t, blobs.Hash([]byte("v2")), draft[0].Hash)
}

func TestDraftIsSortedByPath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	for _, p := range []string{".claude/z.md", ".claude.json", ".claude/a.md"} {
		_, err := s.SetDraftFile(set.Id, p, []byte("x"), 0o644, nil)
		require.NoError(t, err)
	}
	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	require.Equal(t, []string{".claude.json", ".claude/a.md", ".claude/z.md"},
		[]string{draft[0].Path, draft[1].Path, draft[2].Path})
}

func TestDraftRefsAreRecomputedOnEveryChange(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, "a", []byte(`{{cred.key_a}} {{var.ws}}`), 0o600, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, "b", []byte(`{{cred.key_b}}`), 0o644, nil)
	require.NoError(t, err)

	var refs struct {
		Creds []string `json:"creds"`
		Vars  []string `json:"vars"`
	}
	rec, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Equal(t, []string{"key_a", "key_b"}, refs.Creds)
	require.Equal(t, []string{"ws"}, refs.Vars)

	// 删掉引用 key_b 的文件之后，refs 必须跟着缩
	require.NoError(t, s.RemoveDraftFile(set.Id, "b"))
	rec, err = app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.NoError(t, rec.UnmarshalJSONField("draft_refs", &refs))
	require.Equal(t, []string{"key_a"}, refs.Creds)
}

func TestSetDraftFileRejectsOversizeAndBadPath(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/big", make([]byte, protocol.MaxFileSize+1), 0o644, nil)
	require.ErrorContains(t, err, "512")

	_, err = s.SetDraftFile(set.Id, "../escape", []byte("x"), 0o644, nil)
	require.Error(t, err)

	_, err = s.SetDraftFile(set.Id, ".claude/.credentials.json", []byte("x"), 0o600, nil)
	require.ErrorContains(t, err, "恒排除")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsets/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `hub/internal/configsets/service.go`：

```go
// Package configsets 管配置集本体：草稿、manifest、发布期校验、指派。
//
// 草稿与发布共用一套形状（spec §4.2）：config_sets.draft 与 revisions.files
// 都是 []protocol.FileEntry，于是「草稿 vs head」与「v3 vs v1」是同一个
// diff 函数，前端也只有一种数据形状要处理。
package configsets

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type Service struct {
	app   core.App
	blobs *blobs.Store
	ev    *events.Writer
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service {
	return &Service{app: app, blobs: b, ev: ev}
}

// Refs 是 config_sets.draft_refs 与 revisions.refs 的形状。
type Refs struct {
	Creds []string `json:"creds"`
	Vars  []string `json:"vars"`
}

func (s *Service) Create(name, note string) (*core.Record, error) {
	c, err := s.app.FindCollectionByNameOrId("config_sets")
	if err != nil {
		return nil, fmt.Errorf("configsets: 找不到 collection: %w", err)
	}
	mj, err := manifest.Default().JSON()
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("note", note)
	r.Set("manifest", json.RawMessage(mj))
	r.Set("draft", []protocol.FileEntry{})
	r.Set("draft_refs", Refs{Creds: []string{}, Vars: []string{}})
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("configsets: 创建 %s: %w", name, err)
	}
	return r, nil
}

func (s *Service) record(setID string) (*core.Record, error) {
	r, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("configsets: 配置集 %s 不存在: %w", setID, err)
	}
	return r, nil
}

func (s *Service) Draft(setID string) ([]protocol.FileEntry, error) {
	r, err := s.record(setID)
	if err != nil {
		return nil, err
	}
	return decodeFiles(r, "draft")
}

// SetDraft 整体替换草稿清单。导入向导与收编的组装路径用它。
func (s *Service) SetDraft(setID string, files []protocol.FileEntry) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	return s.saveDraft(r, files)
}

// SetDraftFile 写一个草稿文件：内容落 blob，清单里更新或新增一条。
func (s *Service) SetDraftFile(setID, path string, content []byte, mode uint32, keys []string) (protocol.FileEntry, error) {
	var zero protocol.FileEntry
	if err := manifest.SafeRelPath(path); err != nil {
		return zero, fmt.Errorf("configsets: 路径 %s 非法: %w", path, err)
	}
	// 恒排除双侧各判一次：hub 侧防止「发布了一个包含 .credentials.json
	// 的版本」（spec §3.2）。
	if manifest.IsAlwaysExcluded(path) {
		return zero, fmt.Errorf("configsets: %s 属于恒排除路径，不可纳管", path)
	}
	if len(content) > protocol.MaxFileSize {
		return zero, fmt.Errorf("configsets: %s 有 %d 字节，超过单文件上限 512 KiB",
			path, len(content))
	}

	r, err := s.record(setID)
	if err != nil {
		return zero, err
	}
	hash, err := s.blobs.Put(content)
	if err != nil {
		return zero, err
	}
	entry := protocol.FileEntry{
		Path: path, Hash: hash, Size: uint32(len(content)), Mode: mode, Keys: keys,
	}

	files, err := decodeFiles(r, "draft")
	if err != nil {
		return zero, err
	}
	replaced := false
	for i := range files {
		if files[i].Path == path {
			files[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		files = append(files, entry)
	}
	if err := s.saveDraft(r, files); err != nil {
		return zero, err
	}
	return entry, nil
}

func (s *Service) RemoveDraftFile(setID, path string) error {
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	files, err := decodeFiles(r, "draft")
	if err != nil {
		return err
	}
	out := files[:0]
	for _, f := range files {
		if f.Path != path {
			out = append(out, f)
		}
	}
	return s.saveDraft(r, out)
}

// saveDraft 排序、重算引用集合、落库。引用集合是凭据删除保护的依据
// （spec §6.5），因此每次改草稿都要重算，不能懒更新。
func (s *Service) saveDraft(r *core.Record, files []protocol.FileEntry) error {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	refs, err := s.collectRefs(files)
	if err != nil {
		return err
	}
	r.Set("draft", files)
	r.Set("draft_refs", refs)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存草稿: %w", err)
	}
	return nil
}

// collectRefs 读回每个 blob 的内容并提取占位符引用，去重后按名字升序。
func (s *Service) collectRefs(files []protocol.FileEntry) (Refs, error) {
	creds := map[string]bool{}
	vars := map[string]bool{}
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			return Refs{}, fmt.Errorf("configsets: 读取 %s 的内容: %w", f.Path, err)
		}
		rs, err := protocol.Refs(content)
		if err != nil {
			return Refs{}, fmt.Errorf("configsets: %s 的占位符语法错误: %w", f.Path, err)
		}
		for _, ref := range rs {
			switch ref.Kind {
			case protocol.RefCred:
				creds[ref.Name] = true
			case protocol.RefVar:
				vars[ref.Name] = true
			}
			// machine.* 是内置值，不进引用集合。
		}
	}
	return Refs{Creds: sortedKeys(creds), Vars: sortedKeys(vars)}, nil
}

func (s *Service) Manifest(setID string) (manifest.Manifest, error) {
	r, err := s.record(setID)
	if err != nil {
		return manifest.Manifest{}, err
	}
	raw := r.GetString("manifest")
	if raw == "" {
		return manifest.Default(), nil
	}
	return manifest.Parse([]byte(raw))
}

func (s *Service) SetManifest(setID string, m manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	r, err := s.record(setID)
	if err != nil {
		return err
	}
	b, err := m.JSON()
	if err != nil {
		return err
	}
	r.Set("manifest", json.RawMessage(b))
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("configsets: 保存 manifest: %w", err)
	}
	return nil
}

func decodeFiles(r *core.Record, field string) ([]protocol.FileEntry, error) {
	var files []protocol.FileEntry
	// JSONField 读不了点号 key，也不能用 GetString 之后自己 Unmarshal 之外的路子。
	if err := r.UnmarshalJSONField(field, &files); err != nil {
		return nil, fmt.Errorf("configsets: 解析 %s: %w", field, err)
	}
	if files == nil {
		files = []protocol.FileEntry{}
	}
	return files, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsets/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/
git commit -m "feat: 配置集与草稿维护"
```

---

### Task 2: 发布期校验

**Files:**
- Create: `hub/internal/configsets/validate.go`
- Test: `hub/internal/configsets/validate_test.go`

**Interfaces:**
- Produces:
  ```go
  type Problem struct {
      Path   string
      Kind   string // always_excluded / too_large / undefined_ref / bad_path
      Detail string
  }
  func (s *Service) Validate(setID string, known map[string]bool) ([]Problem, error)
  ```

`known` 是「当前存在的凭据名与变量名集合」，键形如 `cred.foo` / `var.bar`。未定义引用在发布期就要拒（spec §6.1），不能等到 agent 渲染时才炸。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/configsets/validate_test.go`：

```go
package configsets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestValidateAcceptsCleanDraft(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{cred.k}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{"cred.k": true})
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestValidateReportsUndefinedRef(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{cred.gone}}","W":"{{var.ws}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{"var.ws": true})
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "undefined_ref", problems[0].Kind)
	require.Contains(t, problems[0].Detail, "cred.gone")
}

// machine.* 是内置值，永远算已定义。
func TestValidateAcceptsMachineRefsWithoutKnownSet(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md",
		[]byte("本机是 {{machine.hostname}}"), 0o644, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, nil)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestValidateReportsOversizeEntry(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	// 绕过 SetDraftFile 的前置检查，直接塞一条超限清单项，
	// 模拟「导入采集时漏判」这类历史数据。
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{
		{Path: ".claude/big", Hash: "aa", Size: protocol.MaxFileSize + 1, Mode: 0o644},
	}))
	problems, err := s.Validate(set.Id, nil)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "too_large", problems[0].Kind)
}

func TestValidateReportsAlwaysExcluded(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{
		{Path: ".claude/.credentials.json", Hash: "aa", Size: 10, Mode: 0o600},
		{Path: ".claude/projects/x.jsonl", Hash: "bb", Size: 10, Mode: 0o644},
	}))
	problems, err := s.Validate(set.Id, nil)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	for _, p := range problems {
		require.Equal(t, "always_excluded", p.Kind)
	}
}
```

> `SetDraft` 在这几个用例里绕过了 `SetDraftFile` 的前置检查，因此不会去读 blob。实现时 `SetDraft` 里的 `collectRefs` 对读不到的 hash 要能容忍：读不到就跳过该文件的引用提取（清单项是外部给的，内容可能尚未落库）。相应地把 `collectRefs` 里的 `blobs.Get` 失败从「返回错误」改成「跳过这一条」，并在 `Validate` 里把「blob 缺失」报成 `Problem{Kind: "missing_blob"}`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsets/ -run Validate -v`
Expected: FAIL，`s.Validate undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/configsets/validate.go`：

```go
package configsets

import (
	"fmt"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Problem 是一条发布期校验失败。UI 逐条展示并阻止发布。
type Problem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// 校验类别
const (
	ProblemAlwaysExcluded = "always_excluded"
	ProblemTooLarge       = "too_large"
	ProblemUndefinedRef   = "undefined_ref"
	ProblemBadPath        = "bad_path"
	ProblemMissingBlob    = "missing_blob"
)

// Validate 检查草稿能不能发布。known 是当前存在的凭据与变量，
// 键形如 "cred.foo" / "var.bar"；machine.* 是内置值，永远算已定义。
//
// 未定义引用必须在这里拒掉：让它进 Revision，agent 渲染时只能拒绝
// apply 该文件，而那时用户已经点过发布了（spec §6.1）。
func (s *Service) Validate(setID string, known map[string]bool) ([]Problem, error) {
	files, err := s.Draft(setID)
	if err != nil {
		return nil, err
	}

	var problems []Problem
	for _, f := range files {
		if err := manifest.SafeRelPath(f.Path); err != nil {
			problems = append(problems, Problem{f.Path, ProblemBadPath, err.Error()})
			continue
		}
		if manifest.IsAlwaysExcluded(f.Path) {
			problems = append(problems, Problem{f.Path, ProblemAlwaysExcluded,
				"该路径属于恒排除清单，不可纳管"})
			continue
		}
		if f.Size > protocol.MaxFileSize {
			problems = append(problems, Problem{f.Path, ProblemTooLarge,
				fmt.Sprintf("%d 字节，超过单文件上限 512 KiB", f.Size)})
			continue
		}

		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			problems = append(problems, Problem{f.Path, ProblemMissingBlob,
				"内容不在库里：" + f.Hash})
			continue
		}
		refs, err := protocol.Refs(content)
		if err != nil {
			problems = append(problems, Problem{f.Path, ProblemUndefinedRef, err.Error()})
			continue
		}
		for _, ref := range refs {
			if ref.Kind == protocol.RefMachine {
				continue
			}
			if !known[ref.String()] {
				problems = append(problems, Problem{f.Path, ProblemUndefinedRef,
					"未定义的引用 " + ref.String()})
			}
		}
	}
	return problems, nil
}
```

同时把 `service.go` 的 `collectRefs` 改成容忍缺失的 blob：

```go
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			// 清单项可能来自外部组装（导入、收编），内容尚未落库。
			// 引用集合宁可少算，也不该让写草稿这一步失败——
			// 真正的把关在 Validate。
			continue
		}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsets/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/
git commit -m "feat: 配置集发布期校验"
```

---

### Task 3: 指派

**Files:**
- Create: `hub/internal/configsets/assign.go`
- Test: `hub/internal/configsets/assign_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s *Service) Assign(machineID, setID, mode string) (*core.Record, error)
  func (s *Service) AssignedMachines(setID string) ([]string, error)
  func (s *Service) Assignment(machineID string) (*core.Record, error)
  var ErrNoAssignment = errors.New("configsets: 该机器未指派配置集")
  ```

`mode` 只接受 `apply` / `survey`（spec §7.6 的强制二选一）。一机一配置集由唯一索引强制，因此 `Assign` 是 upsert。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/configsets/assign_test.go`：

```go
package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
)

func newMachine(t *testing.T, app *tests.TestApp, fp string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("fingerprint", fp)
	r.Set("pub_key", "pk-"+fp)
	r.Set("status", "offline")
	require.NoError(t, app.Save(r))
	return r.Id
}

func TestAssignCreatesThenUpdates(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp1")
	a, err := s.Create("a", "")
	require.NoError(t, err)
	b, err := s.Create("b", "")
	require.NoError(t, err)

	rec, err := s.Assign(m, a.Id, "survey")
	require.NoError(t, err)
	require.Equal(t, "survey", rec.GetString("mode"))
	require.Equal(t, "pending", rec.GetString("state"))

	rec2, err := s.Assign(m, b.Id, "apply")
	require.NoError(t, err)
	require.Equal(t, rec.Id, rec2.Id, "一机一配置集：改指派是更新同一条记录")
	require.Equal(t, b.Id, rec2.GetString("config_set"))
	require.Equal(t, "apply", rec2.GetString("mode"))
	require.Equal(t, "pending", rec2.GetString("state"), "换配置集要回到 pending")

	kinds := []string{}
	recs, err := app.FindAllRecords("events")
	require.NoError(t, err)
	for _, r := range recs {
		kinds = append(kinds, r.GetString("kind"))
	}
	require.Contains(t, kinds, events.KindAssignChanged)
}

func TestAssignRejectsUnknownMode(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp2")
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.Assign(m, set.Id, "whatever")
	require.Error(t, err)
}

func TestAssignedMachines(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	m1 := newMachine(t, app, "fp3")
	m2 := newMachine(t, app, "fp4")
	_, err = s.Assign(m1, set.Id, "apply")
	require.NoError(t, err)
	_, err = s.Assign(m2, set.Id, "survey")
	require.NoError(t, err)

	got, err := s.AssignedMachines(set.Id)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{m1, m2}, got)
}

func TestAssignmentOfUnassignedMachine(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp5")
	_, err := s.Assignment(m)
	require.ErrorIs(t, err, configsets.ErrNoAssignment)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/configsets/ -run Assign -v`
Expected: FAIL，`s.Assign undefined`

- [ ] **Step 3: 实现**

Create `hub/internal/configsets/assign.go`：

```go
package configsets

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/events"
)

var ErrNoAssignment = errors.New("configsets: 该机器未指派配置集")

// 指派状态与模式的取值（spec §4.1）。
const (
	ModeApply  = "apply"
	ModeSurvey = "survey"

	StatePending  = "pending"
	StateApplying = "applying"
	StateAligned  = "aligned"
	StateFailed   = "failed"
	StateDegraded = "degraded"
	StatePaused   = "paused"
)

// Assign 指派配置集给机器。一机一配置集由唯一索引强制，因此这里是 upsert。
//
// mode 必须由调用方显式给出：UI 上是「应用配置集」与「先看看」的强制二选一
// （spec §7.6），没有默认值可言——默认成 apply 会让第二台机器被无声覆盖。
func (s *Service) Assign(machineID, setID, mode string) (*core.Record, error) {
	if mode != ModeApply && mode != ModeSurvey {
		return nil, fmt.Errorf("configsets: mode 只能是 %s 或 %s，收到 %q", ModeApply, ModeSurvey, mode)
	}
	if _, err := s.record(setID); err != nil {
		return nil, err
	}

	r, err := s.Assignment(machineID)
	if errors.Is(err, ErrNoAssignment) {
		c, cerr := s.app.FindCollectionByNameOrId("assignments")
		if cerr != nil {
			return nil, fmt.Errorf("configsets: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
	} else if err != nil {
		return nil, err
	}

	changed := r.GetString("config_set") != setID
	r.Set("config_set", setID)
	r.Set("mode", mode)
	if changed || r.GetString("state") == "" {
		// 换了配置集就回到 pending：旧的 applied_revision 不再有意义。
		r.Set("state", StatePending)
		r.Set("applied_revision", "")
		r.Set("last_error", "")
	}
	if err := s.app.Save(r); err != nil {
		return nil, fmt.Errorf("configsets: 保存指派: %w", err)
	}
	if err := s.ev.Write(events.KindAssignChanged, machineID, map[string]any{
		"config_set": setID, "mode": mode,
	}); err != nil {
		s.app.Logger().Warn("写 assign.changed 事件失败", "error", err)
	}
	return r, nil
}

func (s *Service) Assignment(machineID string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("assignments",
		"machine = {:m}", "", 1, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("configsets: 查询指派: %w", err)
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAssignment, machineID)
	}
	return recs[0], nil
}

func (s *Service) AssignedMachines(setID string) ([]string, error) {
	recs, err := s.app.FindRecordsByFilter("assignments",
		"config_set = {:s}", "", 0, 0, map[string]any{"s": setID})
	if err != nil {
		return nil, fmt.Errorf("configsets: 查询指派机器: %w", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("machine"))
	}
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/configsets/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/configsets/
git commit -m "feat: 配置集指派"
```

---

### Task 4: 发布与回滚

**Files:**
- Create: `hub/internal/revisions/service.go`
- Test: `hub/internal/revisions/service_test.go`

**Interfaces:**
- Consumes: `configsets.Service`（读草稿）、`blobs`、`protocol.Checksum`
- Produces: `NewService` / `Publish` / `PublishFiles` / `Rollback` / `Files` / `Head` / `Diff` / `FileChange`（逐字符见 00-overview）

**规则**

- `seq` 在配置集内递增，由 `(config_set, seq)` 唯一索引兜底；并发发布时重试。
- `manifest` 随版本冻结（spec §4.1）。
- **回滚不删任何东西**：把旧版清单复制成一条新 Revision（seq+1，`source=rollback`），note 自动填「回滚自 v1」（spec §7.2）。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/revisions/service_test.go`：

```go
package revisions_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

func newBoth(t *testing.T) (*tests.TestApp, *configsets.Service, *revisions.Service) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	b := blobs.New(app)
	ev := events.NewWriter(app)
	return app, configsets.NewService(app, b, ev), revisions.NewService(app, b, ev)
}

func TestPublishFreezesDraft(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("v1 {{cred.k}}"), 0o600, nil)
	require.NoError(t, err)

	rev, err := rs.Publish(set.Id, "第一版", "publish")
	require.NoError(t, err)
	require.EqualValues(t, 1, rev.GetInt("seq"))
	require.Equal(t, "publish", rev.GetString("source"))
	require.NotEmpty(t, rev.GetString("manifest"), "manifest 必须随版本冻结")

	files, err := rs.Files(rev.Id)
	require.NoError(t, err)
	require.Equal(t, protocol.Checksum(files), rev.GetString("checksum"))

	var refs configsets.Refs
	require.NoError(t, rev.UnmarshalJSONField("refs", &refs))
	require.Equal(t, []string{"k"}, refs.Creds)

	head, err := rs.Head(set.Id)
	require.NoError(t, err)
	require.Equal(t, rev.Id, head.Id)

	updated, err := app.FindRecordById("config_sets", set.Id)
	require.NoError(t, err)
	require.Equal(t, rev.Id, updated.GetString("head"))
}

func TestPublishIncrementsSeq(t *testing.T) {
	_, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	for i := 1; i <= 3; i++ {
		_, err := cs.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte(fmt.Sprintf("v%d", i)), 0o644, nil)
		require.NoError(t, err)
		rev, err := rs.Publish(set.Id, "", "publish")
		require.NoError(t, err)
		require.EqualValues(t, i, rev.GetInt("seq"))
	}
}

// 并发发布不能产生重复 seq：唯一索引会拦下，Publish 必须重试。
func TestConcurrentPublishSeqIsUnique(t *testing.T) {
	app, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)
	_, err = cs.SetDraftFile(set.Id, "a", []byte("x"), 0o644, nil)
	require.NoError(t, err)

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = rs.Publish(set.Id, "", "publish")
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	recs, err := app.FindRecordsByFilter("revisions", "config_set = {:s}", "seq", 0, 0,
		map[string]any{"s": set.Id})
	require.NoError(t, err)
	require.Len(t, recs, n)
	for i, r := range recs {
		require.EqualValues(t, i+1, r.GetInt("seq"))
	}
}

// 回滚生成新版本，绝不改历史（spec §7.2）。
func TestRollbackCreatesNewRevision(t *testing.T) {
	_, cs, rs := newBoth(t)
	set, err := cs.Create("s", "")
	require.NoError(t, err)

	_, err = cs.SetDraftFile(set.Id, "a", []byte("v1"), 0o644, nil)
	require.NoError(t, err)
	v1, err := rs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	_, err = cs.SetDraftFile(set.Id, "a", []byte("v2"), 0o644, nil)
	require.NoError(t, err)
	v2, err := rs.Publish(set.Id, "", "publish")
	require.NoError(t, err)

	v3, err := rs.Rollback(set.Id, v1.Id)
	require.NoError(t, err)
	require.EqualValues(t, 3, v3.GetInt("seq"))
	require.Equal(t, "rollback", v3.GetString("source"))
	require.Contains(t, v3.GetString("note"), "v1")
	require.Equal(t, v1.GetString("checksum"), v3.GetString("checksum"),
		"回滚后的内容必须与被回滚到的版本一致")

	// 历史仍在
	for _, id := range []string{v1.Id, v2.Id} {
		_, err := rs.Files(id)
		require.NoError(t, err)
	}

	// 草稿也要跟着回到 v1，否则用户下一次发布会把 v2 的内容又推上去
	draft, err := cs.Draft(set.Id)
	require.NoError(t, err)
	v1Files, err := rs.Files(v1.Id)
	require.NoError(t, err)
	require.Equal(t, v1Files, draft)
}

func TestDiff(t *testing.T) {
	from := []protocol.FileEntry{
		{Path: "keep", Hash: "1", Mode: 0o644},
		{Path: "changed", Hash: "2", Mode: 0o644},
		{Path: "removed", Hash: "3", Mode: 0o644},
	}
	to := []protocol.FileEntry{
		{Path: "keep", Hash: "1", Mode: 0o644},
		{Path: "changed", Hash: "9", Mode: 0o644},
		{Path: "added", Hash: "4", Mode: 0o644},
	}
	got := revisions.Diff(from, to)
	require.Equal(t, []revisions.FileChange{
		{Path: "added", Kind: "added", FromHash: "", ToHash: "4"},
		{Path: "changed", Kind: "modified", FromHash: "2", ToHash: "9"},
		{Path: "removed", Kind: "removed", FromHash: "3", ToHash: ""},
	}, got)
}

// mode 变了也算改动：它进 checksum，也决定落盘权限。
func TestDiffDetectsModeChange(t *testing.T) {
	from := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o644}}
	to := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o600}}
	got := revisions.Diff(from, to)
	require.Len(t, got, 1)
	require.Equal(t, "modified", got[0].Kind)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/revisions/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `hub/internal/revisions/service.go`：

```go
// Package revisions 管不可变的版本：发布、checksum、版本间 diff、回滚。
//
// 历史不可变（产品 §4.2）：回滚不删任何东西，而是把旧版清单复制成一条
// 新 Revision（spec §7.2）。
package revisions

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/protocol"
)

type Service struct {
	app   core.App
	blobs *blobs.Store
	ev    *events.Writer
	sets  *configsets.Service
}

func NewService(app core.App, b *blobs.Store, ev *events.Writer) *Service {
	return &Service{app: app, blobs: b, ev: ev, sets: configsets.NewService(app, b, ev)}
}

// Publish 把当前草稿冻结成一条新 Revision。
func (s *Service) Publish(setID, note, source string) (*core.Record, error) {
	files, err := s.sets.Draft(setID)
	if err != nil {
		return nil, err
	}
	return s.PublishFiles(setID, files, note, source)
}

// PublishFiles 用给定清单发布。收编（source=adopt）与回滚走这条。
func (s *Service) PublishFiles(setID string, files []protocol.FileEntry, note, source string) (*core.Record, error) {
	set, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 配置集 %s 不存在: %w", setID, err)
	}
	m, err := s.sets.Manifest(setID)
	if err != nil {
		return nil, err
	}
	mj, err := m.JSON()
	if err != nil {
		return nil, err
	}

	sorted := make([]protocol.FileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	refs, err := s.refsOf(sorted)
	if err != nil {
		return nil, err
	}

	c, err := s.app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return nil, fmt.Errorf("revisions: 找不到 collection: %w", err)
	}

	// seq 冲突靠唯一索引兜底并重试。并发发布很罕见，但一旦发生，
	// 两条同号 revision 会让「回滚到 v2」变成歧义。
	var rev *core.Record
	for attempt := 0; attempt < 8; attempt++ {
		next, err := s.nextSeq(setID)
		if err != nil {
			return nil, err
		}
		r := core.NewRecord(c)
		r.Set("config_set", setID)
		r.Set("seq", next)
		r.Set("files", sorted)
		r.Set("manifest", json.RawMessage(mj))
		r.Set("checksum", protocol.Checksum(sorted))
		r.Set("refs", refs)
		r.Set("note", note)
		r.Set("source", source)
		if err := s.app.Save(r); err != nil {
			continue // 多半是 seq 撞了，重算再来
		}
		rev = r
		break
	}
	if rev == nil {
		return nil, fmt.Errorf("revisions: 分配 seq 失败（并发发布过多）")
	}

	set.Set("head", rev.Id)
	if err := s.app.Save(set); err != nil {
		return nil, fmt.Errorf("revisions: 更新 head: %w", err)
	}
	if err := s.ev.Write(events.KindConfigSetPublished, "", map[string]any{
		"config_set": setID, "revision": rev.Id, "seq": rev.GetInt("seq"), "source": source,
	}); err != nil {
		s.app.Logger().Warn("写 configset.published 事件失败", "error", err)
	}
	return rev, nil
}

// Rollback 把旧版清单复制成一条新 Revision，并把草稿也拉回旧版
// ——否则用户下一次发布会把刚回滚掉的内容又推上去。
func (s *Service) Rollback(setID, revisionID string) (*core.Record, error) {
	old, err := s.app.FindRecordById("revisions", revisionID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 版本 %s 不存在: %w", revisionID, err)
	}
	if old.GetString("config_set") != setID {
		return nil, fmt.Errorf("revisions: 版本 %s 不属于配置集 %s", revisionID, setID)
	}
	files, err := s.Files(revisionID)
	if err != nil {
		return nil, err
	}

	note := fmt.Sprintf("回滚自 v%d", old.GetInt("seq"))
	rev, err := s.PublishFiles(setID, files, note, "rollback")
	if err != nil {
		return nil, err
	}
	if err := s.sets.SetDraft(setID, files); err != nil {
		return nil, err
	}
	if err := s.ev.Write(events.KindConfigSetRolledBack, "", map[string]any{
		"config_set": setID, "from": revisionID, "to": rev.Id,
	}); err != nil {
		s.app.Logger().Warn("写 configset.rolled_back 事件失败", "error", err)
	}
	return rev, nil
}

func (s *Service) Files(revID string) ([]protocol.FileEntry, error) {
	r, err := s.app.FindRecordById("revisions", revID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 版本 %s 不存在: %w", revID, err)
	}
	var files []protocol.FileEntry
	if err := r.UnmarshalJSONField("files", &files); err != nil {
		return nil, fmt.Errorf("revisions: 解析 files: %w", err)
	}
	if files == nil {
		files = []protocol.FileEntry{}
	}
	return files, nil
}

// Head 返回配置集当前已发布的最新版本。尚未发布时返回错误。
func (s *Service) Head(setID string) (*core.Record, error) {
	set, err := s.app.FindRecordById("config_sets", setID)
	if err != nil {
		return nil, fmt.Errorf("revisions: 配置集 %s 不存在: %w", setID, err)
	}
	head := set.GetString("head")
	if head == "" {
		return nil, fmt.Errorf("revisions: 配置集 %s 尚未发布任何版本", setID)
	}
	return s.app.FindRecordById("revisions", head)
}

func (s *Service) nextSeq(setID string) (int, error) {
	recs, err := s.app.FindRecordsByFilter("revisions", "config_set = {:s}", "-seq", 1, 0,
		map[string]any{"s": setID})
	if err != nil {
		return 0, fmt.Errorf("revisions: 查询最大 seq: %w", err)
	}
	if len(recs) == 0 {
		return 1, nil
	}
	return recs[0].GetInt("seq") + 1, nil
}

func (s *Service) refsOf(files []protocol.FileEntry) (configsets.Refs, error) {
	creds := map[string]bool{}
	vars := map[string]bool{}
	for _, f := range files {
		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			return configsets.Refs{}, fmt.Errorf("revisions: 读取 %s 的内容: %w", f.Path, err)
		}
		rs, err := protocol.Refs(content)
		if err != nil {
			return configsets.Refs{}, fmt.Errorf("revisions: %s 的占位符语法错误: %w", f.Path, err)
		}
		for _, ref := range rs {
			switch ref.Kind {
			case protocol.RefCred:
				creds[ref.Name] = true
			case protocol.RefVar:
				vars[ref.Name] = true
			}
		}
	}
	return configsets.Refs{Creds: sortedKeys(creds), Vars: sortedKeys(vars)}, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Create `hub/internal/revisions/diff.go`：

```go
package revisions

import (
	"sort"

	"github.com/FlintyLemming/orciny/protocol"
)

// FileChange 是清单层面的一条差异。内容层面的 unified diff 另算
// （drift 包，spec §8.2）。
type FileChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"` // added / removed / modified
	FromHash string `json:"from_hash"`
	ToHash   string `json:"to_hash"`
}

// Diff 比两份清单。草稿与 revision 同形状，因此「草稿 vs head」与
// 「v3 vs v1」走的是同一个函数（spec §4.2）。
func Diff(from, to []protocol.FileEntry) []FileChange {
	index := func(fs []protocol.FileEntry) map[string]protocol.FileEntry {
		m := make(map[string]protocol.FileEntry, len(fs))
		for _, f := range fs {
			m[f.Path] = f
		}
		return m
	}
	a, b := index(from), index(to)

	var out []FileChange
	for path, nf := range b {
		of, ok := a[path]
		if !ok {
			out = append(out, FileChange{Path: path, Kind: "added", ToHash: nf.Hash})
			continue
		}
		// mode 变了也算改动：它进 checksum，也决定落盘权限。
		if of.Hash != nf.Hash || of.Mode != nf.Mode {
			out = append(out, FileChange{Path: path, Kind: "modified", FromHash: of.Hash, ToHash: nf.Hash})
		}
	}
	for path, of := range a {
		if _, ok := b[path]; !ok {
			out = append(out, FileChange{Path: path, Kind: "removed", FromHash: of.Hash})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/revisions/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/revisions/
git commit -m "feat: 版本发布、checksum、diff 与回滚"
```

---

### Task 5: testsupport 的配置集脚手架

**Files:**
- Create: `internal/testsupport/configsets.go`（`//go:build testing`）
- Test: `internal/testsupport/configsets_test.go`

**Interfaces:**
- Produces:
  ```go
  func (h *TestHub) SeedConfigSet(t *testing.T, name string, files map[string]string) (setID, revID string)
  func (h *TestHub) RequireAssignmentState(t *testing.T, machineID, want string)
  ```

后面五个子计划的集成测试都要「先有一个已发布的配置集」，把它收敛成一个函数。

- [ ] **Step 1: 写失败的测试**

Create `internal/testsupport/configsets_test.go`：

```go
//go:build testing

package testsupport_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestSeedConfigSetPublishesRevision(t *testing.T) {
	th := testsupport.NewTestHub(t)
	setID, revID := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md":      "# 规矩\n",
		".claude/settings.json":  `{"model":"opus"}`,
	})
	require.NotEmpty(t, setID)
	require.NotEmpty(t, revID)

	set, err := th.App.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, revID, set.GetString("head"))

	rev, err := th.App.FindRecordById("revisions", revID)
	require.NoError(t, err)
	require.EqualValues(t, 1, rev.GetInt("seq"))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./internal/testsupport/ -run SeedConfigSet -v`
Expected: FAIL，`th.SeedConfigSet undefined`

- [ ] **Step 3: 实现**

Create `internal/testsupport/configsets.go`：

```go
//go:build testing

package testsupport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
)

// SeedConfigSet 建一个配置集、写入给定文件、发布 v1，返回 (setID, revID)。
//
// files 的键是受管相对路径（以 HOME 为根），值是内容。权限位统一 0644；
// 需要 0600 的用例自行改写 revision。
func (h *TestHub) SeedConfigSet(t *testing.T, name string, files map[string]string) (string, string) {
	t.Helper()

	b := blobs.New(h.App)
	ev := events.NewWriter(h.App)
	cs := configsets.NewService(h.App, b, ev)
	rs := revisions.NewService(h.App, b, ev)

	set, err := cs.Create(name, "由 testsupport 生成")
	require.NoError(t, err, "创建配置集")
	for path, content := range files {
		_, err := cs.SetDraftFile(set.Id, path, []byte(content), 0o644, nil)
		require.NoError(t, err, "写草稿 %s", path)
	}
	rev, err := rs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err, "发布")
	return set.Id, rev.Id
}

// RequireAssignmentState 等到某机器的指派状态变成 want。
//
// 状态由 ApplyAck 的处理路径写入，发生在另一个 goroutine 上，
// 因此必须轮询而不是直接断言。
func (h *TestHub) RequireAssignmentState(t *testing.T, machineID, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		recs, err := h.App.FindRecordsByFilter("assignments", "machine = {:m}", "", 1, 0,
			map[string]any{"m": machineID})
		return err == nil && len(recs) == 1 && recs[0].GetString("state") == want
	}, 3*time.Second, 10*time.Millisecond, "机器 %s 的指派未变成 %s", machineID, want)
}
```

import 需要 `"time"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./internal/testsupport/...`
Expected: PASS

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add internal/testsupport/
git commit -m "test: 配置集脚手架"
```
