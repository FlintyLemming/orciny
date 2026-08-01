# 子计划 06 · agent 本地状态、凭据缓存、blob 缓存与渲染

**前置**：02（`protocol.Ref` / `Render`）、05（`internal/manifest`）
**读这份之前先读** [00-overview.md](00-overview.md)。

**交付物**：`agent/internal/state`（`state.json`）、`agent/internal/secrets`（`secrets.json`）、`agent/internal/blobcache`（`~/.orciny/blobs/`）、`agent/internal/render` 的**渲染方向**（还原方向在子计划 11）。

**为什么 agent 缓存凭据明文（spec §6.2）**：这不是图省事，是三个功能的前提——漂移上报前要把磁盘里的真实值替回占位符（得有值才能替）、hub 离线时仍能重渲染与回滚、5 分钟一次的对账不该依赖网络。安全上不增加实质暴露面：这些值本来就以明文躺在同一台机器、同一个用户的 `~/.claude/settings.json` 里。

---

### Task 1: `state.json`

**Files:**
- Create: `agent/internal/state/state.go`
- Test: `agent/internal/state/state_test.go`

**Interfaces:**
- Produces: `FileState` / `State` / `Path` / `Load` / `Save` / `HealthOK` / `HealthDegraded`（逐字符见 00-overview）

**两个 hash 的分工（spec §6.3）**：`blob` 是渲染**前**的内容 hash（= Revision 清单里的 hash），用来判断本地 blob cache 是否命中；`rendered` 是渲染**后**落盘内容的 hash，是**漂移比对的唯一依据**。凭据轮换后 agent 重写文件并更新 `rendered`，因此**轮换不产生漂移**。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/state/state_test.go`：

```go
package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/state"
)

func sample() *state.State {
	return &state.State{
		ConfigSet: "set1",
		Revision:  "rev7",
		Seq:       7,
		Checksum:  "abc",
		AppliedAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Mode:      "apply",
		Health:    state.HealthOK,
		Files: map[string]state.FileState{
			".claude/settings.json": {Blob: "b1", Rendered: "r1", Mode: 0o600, Size: 1234},
			".claude.json":          {Blob: "b2", Rendered: "r2", Mode: 0o644, Keys: []string{"mcpServers"}},
		},
		Ignored: []string{".claude/skills/scratch/**"},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, state.Save(dir, sample()))

	got, err := state.Load(dir)
	require.NoError(t, err)
	require.Equal(t, sample(), got)
}

func TestSaveIs0600(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, state.Save(dir, sample()))

	info, err := os.Stat(state.Path(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.Equal(t, filepath.Join(dir, "state.json"), state.Path(dir))
}

// state.json 丢失是 survey 自愈的触发条件（spec §7.6），
// 因此调用方必须能用 errors.Is 精确判断。
func TestLoadMissingIsNotExist(t *testing.T) {
	_, err := state.Load(t.TempDir())
	require.True(t, errors.Is(err, os.ErrNotExist), "缺失必须能被 errors.Is 认出来")
}

// 损坏的 state.json 与丢失同等对待：都得走 survey 自愈，绝不能猜。
func TestLoadCorruptIsNotExist(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(state.Path(dir), []byte("{不是 json"), 0o600))
	_, err := state.Load(dir)
	require.True(t, errors.Is(err, os.ErrNotExist),
		"损坏与丢失同等对待：都不知道基线是什么，都必须走 survey")
}

func TestHealthValues(t *testing.T) {
	require.Equal(t, "ok", state.HealthOK)
	require.Equal(t, "degraded", state.HealthDegraded)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/state/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/state/state.go`：

```go
// Package state 读写 ~/.orciny/state.json（spec §7.8）。
//
// 单独成包而不是塞进 applier：applier 写它，watcher 读它当漂移基线，
// CLI 的 status / drift / pause 也要读。塞进 applier 会让 watcher
// 依赖 applier，而两者本无关系。
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

const FileName = "state.json"

// health 的取值（spec §7.4）。degraded 表示回滚都失败过一次，
// 此后不再自动 apply 任何后续版本，直到人工解除。
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
)

// FileState 为每个受管路径同时记两个 hash（spec §6.3）。
type FileState struct {
	// Blob 是渲染**前**的内容 hash，等于 Revision 清单里的 hash。
	// 用途：判断本地 blob cache 是否命中、要不要向 hub 索取。
	Blob string `json:"blob"`
	// Rendered 是渲染**后**落盘内容的 hash。
	// 用途：漂移比对的唯一依据。轮换后重写文件并更新它，因此轮换不产生漂移。
	Rendered string   `json:"rendered"`
	Mode     uint32   `json:"mode"`
	Size     uint32   `json:"size,omitempty"`
	Keys     []string `json:"keys,omitempty"`
}

type State struct {
	ConfigSet string               `json:"config_set"`
	Revision  string               `json:"revision"`
	Seq       uint32               `json:"seq"`
	Checksum  string               `json:"checksum"`
	AppliedAt time.Time            `json:"applied_at"`
	Mode      string               `json:"mode"`
	Health    string               `json:"health"`
	Files     map[string]FileState `json:"files"`
	Ignored   []string             `json:"ignored"`
	Paused    bool                 `json:"paused"`
}

func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load 读回本机状态。
//
// **损坏与丢失同等对待**，都返回包装了 os.ErrNotExist 的错误：两种情形下
// agent 都不知道基线是什么，而 spec §7.6 的规矩是——不知道基线就绝不覆盖
// 用户的文件，一律上报状态丢失、由 hub 打回 survey 做全量对账。
func Load(dir string) (*State, error) {
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, err // 保留 os.ErrNotExist
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("state: %s 损坏，按状态丢失处理: %w", FileName, os.ErrNotExist)
	}
	if s.Files == nil {
		s.Files = map[string]FileState{}
	}
	return &s, nil
}

func Save(dir string, s *State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: 序列化: %w", err)
	}
	return atomicfile.Write(Path(dir), b, 0o600)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/state/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/state/
git commit -m "feat: agent 本地状态 state.json"
```

---

### Task 2: `secrets.json`

**Files:**
- Create: `agent/internal/secrets/secrets.go`
- Test: `agent/internal/secrets/secrets_test.go`

**Interfaces:**
- Consumes: `protocol.Ref` / `protocol.RefCred` / `RefVar` / `RefMachine`
- Produces: `File` / `Path` / `Load` / `Save` / `Lookup` / `Equal`

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/secrets/secrets_test.go`：

```go
package secrets_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestSaveLoadRoundTripAndPerm(t *testing.T) {
	dir := t.TempDir()
	want := &secrets.File{
		Creds:   map[string]string{"k": "sk-value-1234"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac", "os": "darwin"},
	}
	require.NoError(t, secrets.Save(dir, want))

	info, err := os.Stat(secrets.Path(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "凭据缓存必须 0600")

	got, err := secrets.Load(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// 首次运行时文件还不存在，这不是错误。
func TestLoadMissingReturnsEmpty(t *testing.T) {
	f, err := secrets.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, f.Creds)
	require.Empty(t, f.Vars)
	require.Empty(t, f.Machine)
}

func TestLookup(t *testing.T) {
	f := &secrets.File{
		Creds:   map[string]string{"k": "sk-value"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac"},
	}
	v, ok := f.Lookup(protocol.Ref{Kind: protocol.RefCred, Name: "k"})
	require.True(t, ok)
	require.Equal(t, "sk-value", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefVar, Name: "ws"})
	require.True(t, ok)
	require.Equal(t, "main", v)

	v, ok = f.Lookup(protocol.Ref{Kind: protocol.RefMachine, Name: "hostname"})
	require.True(t, ok)
	require.Equal(t, "mac", v)

	_, ok = f.Lookup(protocol.Ref{Kind: protocol.RefCred, Name: "gone"})
	require.False(t, ok)
}

// 轮换检测靠它：拉回来的快照 revision 没变但 secrets 变了，
// 就只重渲染受影响的文件（spec §5.1）。
func TestEqual(t *testing.T) {
	a := &secrets.File{Creds: map[string]string{"k": "v"}, Vars: map[string]string{"w": "1"}}
	b := &secrets.File{Creds: map[string]string{"k": "v"}, Vars: map[string]string{"w": "1"}}
	require.True(t, a.Equal(b))

	c := &secrets.File{Creds: map[string]string{"k": "v2"}, Vars: map[string]string{"w": "1"}}
	require.False(t, a.Equal(c))

	require.False(t, a.Equal(nil))
	require.True(t, (*secrets.File)(nil).Equal(nil))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/secrets/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/secrets/secrets.go`：

```go
// Package secrets 读写 ~/.orciny/secrets.json（0600，spec §6.2）。
//
// 这里存的是**明文**的凭据与变量值。理由不是图省事，是三个功能的前提：
//  1. 漂移 diff 不泄密：上报前要把磁盘里的真实值替回 {{cred.x}}，得有值才能替
//  2. hub 离线时仍能自愈：重渲染基线、恢复、回滚都不必等 hub
//  3. 对账不依赖网络：5 分钟一次的对账若每次都要向 hub 换凭据，hub 一断就瞎
//
// 安全上这不增加实质暴露面：这些值本来就以明文躺在同一台机器、同一个用户、
// 同样权限的 ~/.claude/settings.json 里。真正的边界是「机器被攻陷」，
// 而那时 settings.json 已经先失守了。
package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

const FileName = "secrets.json"

type File struct {
	Creds   map[string]string `json:"creds"`
	Vars    map[string]string `json:"vars"`
	Machine map[string]string `json:"machine"`
}

func Path(dir string) string { return filepath.Join(dir, FileName) }

// Load 读回缓存。文件不存在返回空 File 而不是错误——首次运行的常态。
func Load(dir string) (*File, error) {
	b, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return &File{Creds: map[string]string{}, Vars: map[string]string{}, Machine: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: 读取: %w", err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("secrets: %s 损坏: %w", FileName, err)
	}
	if f.Creds == nil {
		f.Creds = map[string]string{}
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	if f.Machine == nil {
		f.Machine = map[string]string{}
	}
	return &f, nil
}

func Save(dir string, f *File) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("secrets: 序列化: %w", err)
	}
	return atomicfile.Write(Path(dir), b, 0o600)
}

// Lookup 是渲染时的取值函数，直接喂给 protocol.Render。
func (f *File) Lookup(r protocol.Ref) (string, bool) {
	if f == nil {
		return "", false
	}
	switch r.Kind {
	case protocol.RefCred:
		v, ok := f.Creds[r.Name]
		return v, ok
	case protocol.RefVar:
		v, ok := f.Vars[r.Name]
		return v, ok
	case protocol.RefMachine:
		v, ok := f.Machine[r.Name]
		return v, ok
	default:
		return "", false
	}
}

// Equal 用于检测凭据轮换：拉回来的快照 revision 没变但 secrets 变了，
// 就只重渲染受影响的文件（spec §5.1）。
func (f *File) Equal(o *File) bool {
	if f == nil || o == nil {
		return f == nil && o == nil
	}
	return sameMap(f.Creds, o.Creds) && sameMap(f.Vars, o.Vars) && sameMap(f.Machine, o.Machine)
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/secrets/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add agent/internal/secrets/
git commit -m "feat: agent 侧凭据与变量缓存"
```

---

### Task 3: 本地 blob 缓存

**Files:**
- Create: `agent/internal/blobcache/cache.go`
- Test: `agent/internal/blobcache/cache_test.go`

**Interfaces:**
- Produces: `Cache` / `New` / `Has` / `Get` / `Put` / `PutHash` / `Missing`

布局 `~/.orciny/blobs/<前两位>/<hash>`：分桶是为了不让一个目录里堆几千个文件。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/blobcache/cache_test.go`：

```go
package blobcache_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
)

// hash 口径必须与 hub 侧 blobs.Hash 逐位一致，否则缓存永远不命中且没有任何
// 报错。agent 不许 import hub，因此这里钉死同一个期望值。
func TestHashMatchesHubCanon(t *testing.T) {
	require.Equal(t,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		blobcache.Hash(nil))
}

func TestPutGetRoundTrip(t *testing.T) {
	c := blobcache.New(t.TempDir())
	content := []byte("# CLAUDE.md\n")

	h, err := c.Put(content)
	require.NoError(t, err)
	require.True(t, c.Has(h))

	got, err := c.Get(h)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestPutIsBucketed(t *testing.T) {
	dir := t.TempDir()
	c := blobcache.New(dir)
	h, err := c.Put([]byte("x"))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dir, "blobs", h[:2], h))
}

// hub 发来的 BlobData 带着 hash，落盘前必须自己再算一遍——
// 一个被篡改的 hub 不该能往 agent 的缓存里塞任意内容。
func TestPutHashRejectsMismatch(t *testing.T) {
	c := blobcache.New(t.TempDir())
	err := c.PutHash("0000000000000000000000000000000000000000000000000000000000000000",
		[]byte("内容对不上"))
	require.Error(t, err)
	require.False(t, c.Has("0000000000000000000000000000000000000000000000000000000000000000"))
}

func TestPutHashAcceptsMatching(t *testing.T) {
	c := blobcache.New(t.TempDir())
	content := []byte("对得上")
	require.NoError(t, c.PutHash(blobcache.Hash(content), content))
	got, err := c.Get(blobcache.Hash(content))
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestMissingReturnsOnlyAbsentHashes(t *testing.T) {
	c := blobcache.New(t.TempDir())
	have, err := c.Put([]byte("有"))
	require.NoError(t, err)
	absent := blobcache.Hash([]byte("没有"))

	// 同一个 hash 重复出现只该返回一次
	require.Equal(t, []string{absent}, c.Missing([]string{have, absent, absent, have}))
	require.Empty(t, c.Missing(nil))
}

func TestGetMissingIsNotExist(t *testing.T) {
	c := blobcache.New(t.TempDir())
	_, err := c.Get(blobcache.Hash([]byte("nope")))
	require.True(t, os.IsNotExist(err))
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/blobcache/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/blobcache/cache.go`：

```go
// Package blobcache 是 ~/.orciny/blobs/ 下的本地内容缓存。
//
// 有了它，同一份内容只从 hub 拉一次；hub 离线时也能重渲染基线与回滚。
package blobcache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// DirName 是缓存在 agent 目录下的子目录名。
const DirName = "blobs"

// Hash 与 hub 侧 blobs.Hash 同源：sha256 的 hex。
// 口径不一致会让缓存永远不命中，且没有任何报错——因此有一条钉死期望值的测试。
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type Cache struct{ dir string }

// New 返回挂在 agent 目录 dir 下的缓存。
func New(dir string) *Cache { return &Cache{dir: filepath.Join(dir, DirName)} }

// path 分两级：一个目录里堆几千个文件对某些文件系统不友好。
func (c *Cache) path(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(c.dir, "_", hash)
	}
	return filepath.Join(c.dir, hash[:2], hash)
}

func (c *Cache) Has(hash string) bool {
	info, err := os.Stat(c.path(hash))
	return err == nil && info.Mode().IsRegular()
}

func (c *Cache) Get(hash string) ([]byte, error) {
	return os.ReadFile(c.path(hash)) // 保留 os.ErrNotExist
}

func (c *Cache) Put(content []byte) (string, error) {
	h := Hash(content)
	if err := c.write(h, content); err != nil {
		return "", err
	}
	return h, nil
}

// PutHash 落盘一份声称是 hash 的内容，落盘前自己再算一遍。
//
// 这是 agent 少数几处能自我保护的地方：hub 发来的 BlobData 带着 hash，
// 若不校验，一个被篡改的 hub 就能往缓存里塞任意内容，而缓存正是
// 「hub 离线时也能自愈」所依赖的基线。
func (c *Cache) PutHash(hash string, content []byte) error {
	if got := Hash(content); got != hash {
		return fmt.Errorf("blobcache: 内容与 hash 不符（声称 %s，实为 %s）", hash, got)
	}
	return c.write(hash, content)
}

func (c *Cache) write(hash string, content []byte) error {
	// 0600：缓存里躺的是**渲染前**的内容（占位符形态），仍然按敏感对待。
	if err := atomicfile.Write(c.path(hash), content, 0o600); err != nil {
		return fmt.Errorf("blobcache: 写入 %s: %w", hash, err)
	}
	return nil
}

// Missing 返回本地没有的 hash，去重并保持首次出现的顺序。
func (c *Cache) Missing(hashes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hashes {
		if seen[h] || c.Has(h) {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/blobcache/...`
Expected: PASS

- [ ] **Step 5: 检查模块边界并提交**

Run: `go list -deps ./agent/... | grep 'orciny/hub' || echo "边界干净"`
Expected: `边界干净`

```bash
git add agent/internal/blobcache/
git commit -m "feat: agent 本地 blob 缓存"
```

---

### Task 4: 渲染

**Files:**
- Create: `agent/internal/render/render.go`
- Test: `agent/internal/render/render_test.go`

**Interfaces:**
- Consumes: `protocol.Parse` / `protocol.Render` / `protocol.Ref`
- Produces: `func Render(content []byte, look func(protocol.Ref) (string, bool)) ([]byte, error)`

还原方向（`Restore`）在子计划 11。这里只做渲染，因为下发闭环（阶段一）只需要它。

- [ ] **Step 1: 写失败的测试**

Create `agent/internal/render/render_test.go`：

```go
package render_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestRenderFillsFromSecrets(t *testing.T) {
	sec := &secrets.File{
		Creds:   map[string]string{"anthropic_key": "sk-ant-real"},
		Vars:    map[string]string{"ws": "main"},
		Machine: map[string]string{"hostname": "mac-mini"},
	}
	out, err := render.Render(
		[]byte(`{"env":{"K":"{{cred.anthropic_key}}"},"ws":"{{var.ws}}","host":"{{machine.hostname}}"}`),
		sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, `{"env":{"K":"sk-ant-real"},"ws":"main","host":"mac-mini"}`, string(out))
}

// 未定义引用绝不能渲染成字面 {{cred.x}} 落给 Claude Code（spec §6.1）。
func TestRenderRefusesUndefinedRef(t *testing.T) {
	sec := &secrets.File{Creds: map[string]string{}}
	_, err := render.Render([]byte(`{"K":"{{cred.deleted}}"}`), sec.Lookup)
	require.Error(t, err)

	var missing *protocol.MissingRefError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, "cred.deleted", missing.Ref.String())
	require.NotContains(t, err.Error(), "sk-", "错误信息里不该出现任何值")
}

func TestRenderPassesThroughLiteralBraces(t *testing.T) {
	sec := &secrets.File{}
	out, err := render.Render([]byte("模板语法写作 {{{{cred.x}}"), sec.Lookup)
	require.NoError(t, err)
	require.Equal(t, "模板语法写作 {{cred.x}}", string(out))
}

func TestRenderRejectsBadSyntax(t *testing.T) {
	sec := &secrets.File{}
	_, err := render.Render([]byte("{{cred.x"), sec.Lookup)
	require.ErrorIs(t, err, protocol.ErrBadPlaceholder)
}

// 渲染是纯函数：同样的输入两次得到同样的字节，
// 否则 rendered hash 会随机变化，每次对账都报漂移。
func TestRenderIsDeterministic(t *testing.T) {
	sec := &secrets.File{Creds: map[string]string{"a": "1", "b": "2", "c": "3"}}
	in := []byte("{{cred.a}}{{cred.b}}{{cred.c}}{{cred.a}}")
	first, err := render.Render(in, sec.Lookup)
	require.NoError(t, err)
	for range 20 {
		again, err := render.Render(in, sec.Lookup)
		require.NoError(t, err)
		require.Equal(t, first, again)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./agent/internal/render/...`
Expected: FAIL，包不存在

- [ ] **Step 3: 实现**

Create `agent/internal/render/render.go`：

```go
// Package render 是 agent 侧的渲染与还原（spec §6）。
//
// 渲染：Revision 里的占位符 → 磁盘上的真实值（本文件）。
// 还原：磁盘上的真实值 → 上报用的占位符（restore.go，子计划 11）。
//
// 词法与转义规则在 protocol，两侧必须逐位一致；这里只负责「值从哪来」。
package render

import (
	"fmt"

	"github.com/FlintyLemming/orciny/protocol"
)

// Render 把内容里的占位符换成真实值。
//
// 任一引用取不到值即整体失败，绝不落一个含字面 {{cred.x}} 的配置给
// Claude Code（spec §6.1）——那会让它拿一个假 key 去请求，
// 错误信息出现在离现场最远的地方。
func Render(content []byte, look func(protocol.Ref) (string, bool)) ([]byte, error) {
	segs, err := protocol.Parse(content)
	if err != nil {
		return nil, err
	}
	out, err := protocol.Render(segs, look)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./agent/internal/render/...`
Expected: PASS

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add agent/internal/render/
git commit -m "feat: agent 侧渲染"
```
