# 子计划 01 · 数据模型与内容寻址存储

**前置**：无（与 02 可并行）
**读这份之前先读** [00-overview.md](00-overview.md) 的 Global Constraints 与全局接口契约。

**交付物**：`hub/internal/migrations/002_configsets.go`（八个 collection + 索引）、`hub/internal/blobs`（Put / Get / Has / GCOrphans）、`hub/internal/events` 的 M1 kind 常量。

**为什么先做这个**：blob 层是 revision、drift、import 三条线的共同地基，而它薄到只有四个函数（spec §4.2）——先立起来，后面每个子计划都能直接用。

---

### Task 1: 八个 collection 的迁移

**Files:**
- Create: `hub/internal/migrations/002_configsets.go`
- Modify: `hub/internal/migrations/migrations_test.go`（追加，不改已有用例）

**Interfaces:**
- Consumes: `001_initial.go` 建的 `machines`、`events`（作为 relation 目标）
- Produces: 八个 collection —— `blobs` / `config_sets` / `revisions` / `assignments` / `credentials` / `variables` / `drift_events` / `ignore_rules`，字段与索引见 00-overview「数据模型」

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/migrations/migrations_test.go`：

```go
func TestM1CollectionsExist(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{
		"blobs", "config_sets", "revisions", "assignments",
		"credentials", "variables", "drift_events", "ignore_rules",
	} {
		c, err := app.FindCollectionByNameOrId(name)
		require.NoError(t, err, "collection %s 必须存在", name)
		require.Nil(t, c.ListRule, "%s 的 list rule 必须是 nil（仅 superuser）", name)
		require.Nil(t, c.ViewRule, "%s 的 view rule 必须是 nil", name)
		require.Nil(t, c.CreateRule, "%s 的 create rule 必须是 nil", name)
		require.Nil(t, c.UpdateRule, "%s 的 update rule 必须是 nil", name)
		require.Nil(t, c.DeleteRule, "%s 的 delete rule 必须是 nil", name)
	}
}

func TestRevisionsSeqIsUniquePerConfigSet(t *testing.T) {
	app := newApp(t)
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "主力配置")
	require.NoError(t, app.Save(set))

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	mk := func(setID string, seq int) *core.Record {
		r := core.NewRecord(revs)
		r.Set("config_set", setID)
		r.Set("seq", seq)
		r.Set("files", []any{})
		r.Set("manifest", map[string]any{"version": 1})
		r.Set("checksum", "deadbeef")
		r.Set("source", "publish")
		return r
	}
	require.NoError(t, app.Save(mk(set.Id, 1)))
	require.Error(t, app.Save(mk(set.Id, 1)), "同一配置集内 seq 必须唯一")

	other := core.NewRecord(sets)
	other.Set("name", "另一份")
	require.NoError(t, app.Save(other))
	require.NoError(t, app.Save(mk(other.Id, 1)), "不同配置集的 seq 互不干扰")
}

func TestAssignmentsOneConfigSetPerMachine(t *testing.T) {
	app := newApp(t)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(machines)
	m.Set("fingerprint", "fp-1")
	m.Set("pub_key", "pk-1")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	mkSet := func(name string) string {
		r := core.NewRecord(sets)
		r.Set("name", name)
		require.NoError(t, app.Save(r))
		return r.Id
	}

	as, err := app.FindCollectionByNameOrId("assignments")
	require.NoError(t, err)
	mk := func(setID string) *core.Record {
		r := core.NewRecord(as)
		r.Set("machine", m.Id)
		r.Set("config_set", setID)
		r.Set("mode", "apply")
		r.Set("state", "pending")
		return r
	}
	require.NoError(t, app.Save(mk(mkSet("a"))))
	require.Error(t, app.Save(mk(mkSet("b"))), "一机一配置集由唯一索引强制")
}

// 同一路径同一时刻只能有一条待处理漂移；已解决的不占位（部分唯一索引）。
func TestDriftEventsOpenPathIsUnique(t *testing.T) {
	app := newApp(t)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(machines)
	m.Set("fingerprint", "fp-2")
	m.Set("pub_key", "pk-2")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	de, err := app.FindCollectionByNameOrId("drift_events")
	require.NoError(t, err)
	mk := func(state string) *core.Record {
		r := core.NewRecord(de)
		r.Set("machine", m.Id)
		r.Set("path", ".claude/CLAUDE.md")
		r.Set("kind", "modified")
		r.Set("state", state)
		return r
	}
	first := mk("open")
	require.NoError(t, app.Save(first))
	require.Error(t, app.Save(mk("open")), "同一路径只能有一条 open 漂移")

	first.Set("state", "adopted")
	require.NoError(t, app.Save(first))
	require.NoError(t, app.Save(mk("open")), "旧的已收编，新的 open 应当可以建")
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/migrations/...`
Expected: FAIL，`collection blobs 必须存在`

- [ ] **Step 3: 写迁移**

Create `hub/internal/migrations/002_configsets.go`：

```go
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// maxBlobSize 与 protocol.MaxFileSize 同值（512 KiB，spec §5.4）。
// 这里写字面量而不是 import protocol，是为了让 01 与 02 能真正并行开工；
// 02 完成后不需要回头改这里——迁移一旦跑过就不该再动。
const maxBlobSize = 512 << 10

func init() {
	m.Register(up002, down002, "002_configsets.go")
}

// M1 的八个 collection（spec §4.1）。追加式：不动 001。
// API rule 一律 nil —— 仅 superuser 可访问，与 M0 一致。
func up002(app core.App) error {
	machines, err := app.FindCollectionByNameOrId("machines")
	if err != nil {
		return err
	}

	// --- blobs：内容寻址存储 ---------------------------------------
	// content 是 file 字段，落在 pb_data/storage，不进 SQLite 行。
	blobs := core.NewBaseCollection("blobs")
	blobs.Fields.Add(
		&core.TextField{Name: "hash", Required: true, Max: 64},
		&core.NumberField{Name: "size"},
		&core.FileField{Name: "content", MaxSelect: 1, MaxSize: maxBlobSize},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	blobs.AddIndex("idx_blobs_hash", true, "hash", "")
	if err := app.Save(blobs); err != nil {
		return err
	}

	// --- config_sets -----------------------------------------------
	sets := core.NewBaseCollection("config_sets")
	sets.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 200},
		&core.TextField{Name: "note", Max: 2000},
		&core.JSONField{Name: "manifest", MaxSize: 65536},
		&core.BoolField{Name: "paused"},
		// head 指向已发布的最新版本。导入中的草稿态为空，因此不 Required。
		&core.TextField{Name: "head", Max: 64},
		&core.JSONField{Name: "draft", MaxSize: 2 << 20},
		&core.JSONField{Name: "draft_refs", MaxSize: 65536},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	sets.AddIndex("idx_config_sets_name", true, "name", "")
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- revisions：不可变 ------------------------------------------
	revs := core.NewBaseCollection("revisions")
	revs.Fields.Add(
		&core.RelationField{Name: "config_set", Required: true, CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.NumberField{Name: "seq", Required: true},
		&core.JSONField{Name: "files", MaxSize: 2 << 20},
		// manifest 随版本冻结，否则历史版本无法解释（spec §4.1）。
		&core.JSONField{Name: "manifest", MaxSize: 65536},
		&core.TextField{Name: "checksum", Max: 64},
		&core.JSONField{Name: "refs", MaxSize: 65536},
		&core.TextField{Name: "note", Max: 2000},
		&core.SelectField{Name: "source", MaxSelect: 1, Values: []string{"publish", "adopt", "rollback", "import"}},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	revs.AddIndex("idx_revisions_set_seq", true, "config_set, seq", "")
	revs.AddIndex("idx_revisions_set", false, "config_set", "")
	if err := app.Save(revs); err != nil {
		return err
	}

	// head 只能在 revisions 建好之后才能改成 relation。留 text 也可以，
	// 但那样前端 expand 不到版本号，UI 每次都要多一次请求。
	sets.Fields.RemoveByName("head")
	sets.Fields.Add(&core.RelationField{Name: "head", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false})
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- assignments：一机一配置集 ----------------------------------
	assigns := core.NewBaseCollection("assignments")
	assigns.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "config_set", Required: true, CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.SelectField{Name: "mode", MaxSelect: 1, Values: []string{"apply", "survey"}},
		&core.SelectField{Name: "state", MaxSelect: 1, Values: []string{
			"pending", "applying", "aligned", "failed", "degraded", "paused",
		}},
		&core.RelationField{Name: "applied_revision", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.DateField{Name: "applied_at"},
		&core.TextField{Name: "last_error", Max: 4000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	assigns.AddIndex("idx_assignments_machine", true, "machine", "")
	assigns.AddIndex("idx_assignments_set", false, "config_set", "")
	if err := app.Save(assigns); err != nil {
		return err
	}

	// --- credentials -------------------------------------------------
	creds := core.NewBaseCollection("credentials")
	creds.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 64},
		&core.TextField{Name: "cipher_value", Required: true, Max: 8192},
		&core.TextField{Name: "last4", Max: 8},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	creds.AddIndex("idx_credentials_name", true, "name", "")
	if err := app.Save(creds); err != nil {
		return err
	}

	// --- variables：机器变量，非秘密 ---------------------------------
	vars := core.NewBaseCollection("variables")
	vars.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "key", Required: true, Max: 64},
		&core.TextField{Name: "value", Max: 4000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	vars.AddIndex("idx_variables_machine_key", true, "machine, key", "")
	if err := app.Save(vars); err != nil {
		return err
	}

	// --- drift_events：收件箱 ----------------------------------------
	drifts := core.NewBaseCollection("drift_events")
	drifts.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "config_set", CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.SelectField{Name: "kind", MaxSelect: 1, Values: []string{"added", "modified", "deleted"}},
		&core.TextField{Name: "base_hash", Max: 64},
		&core.RelationField{Name: "current_blob", CollectionId: blobs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.NumberField{Name: "mode"},
		&core.TextField{Name: "diff", Max: 1 << 20},
		&core.BoolField{Name: "restore_partial"},
		&core.BoolField{Name: "truncated"},
		&core.SelectField{Name: "state", MaxSelect: 1, Values: []string{
			"open", "adopted", "restored", "ignored", "superseded",
		}},
		&core.RelationField{Name: "resolved_revision", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.DateField{Name: "resolved_at"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// 部分唯一索引（spec §4.1）：同一路径同一时刻只能有一条待处理漂移，
	// 重复检测到就更新那条，不新建。已解决的记录不占位。
	drifts.AddIndex("idx_drift_open_path", true, "machine, path", "state = 'open'")
	drifts.AddIndex("idx_drift_set_state", false, "config_set, state", "")
	if err := app.Save(drifts); err != nil {
		return err
	}

	// --- ignore_rules -------------------------------------------------
	ignores := core.NewBaseCollection("ignore_rules")
	ignores.Fields.Add(
		// machine 可空 = 全局规则。
		&core.RelationField{Name: "machine", CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	ignores.AddIndex("idx_ignore_rules_machine_path", false, "machine, path", "")
	return app.Save(ignores)
}

func down002(app core.App) error {
	// 逆序删：先删有外键指向别人的。
	for _, name := range []string{
		"ignore_rules", "drift_events", "variables", "credentials",
		"assignments", "revisions", "config_sets", "blobs",
	} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
```

> `head` 字段先建成 text 再换成 relation，是因为 relation 的目标 collection 必须已存在，而 `revisions` 又要 relation 到 `config_sets`——两者互指，只能分两次保存。

顺手在 `TestM1CollectionsExist` 之后加一条类型断言，免得将来有人把这段两步走的代码「简化」掉：

```go
func TestConfigSetHeadIsRelationToRevisions(t *testing.T) {
	app := newApp(t)
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)

	head, ok := sets.Fields.GetByName("head").(*core.RelationField)
	require.True(t, ok, "config_sets.head 必须是 relation")
	require.Equal(t, revs.Id, head.CollectionId)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/migrations/...`
Expected: PASS，含 M0 的既有用例

- [ ] **Step 5: 提交**

```bash
git add hub/internal/migrations/
git commit -m "feat: M1 八个 collection 的迁移"
```

---

### Task 2: events 的 M1 kind 常量

**Files:**
- Modify: `hub/internal/events/writer.go`
- Test: `hub/internal/events/writer_test.go`

**Interfaces:**
- Produces: 00-overview「`events.kind` 新增取值」里的 15 个常量

一次性全部定义，后续子计划直接引用，避免每个子计划各加一批、互相冲突。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/events/writer_test.go`：

```go
// M1 的事件 kind 必须集中定义在 events 包，调用处不许写字面量。
func TestM1EventKinds(t *testing.T) {
	require.Equal(t, "configset.published", events.KindConfigSetPublished)
	require.Equal(t, "configset.rolled_back", events.KindConfigSetRolledBack)
	require.Equal(t, "assign.changed", events.KindAssignChanged)
	require.Equal(t, "apply.ok", events.KindApplyOK)
	require.Equal(t, "apply.failed", events.KindApplyFailed)
	require.Equal(t, "apply.rollback_failed", events.KindApplyRollbackFailed)
	require.Equal(t, "drift.reported", events.KindDriftReported)
	require.Equal(t, "drift.adopted", events.KindDriftAdopted)
	require.Equal(t, "drift.restored", events.KindDriftRestored)
	require.Equal(t, "drift.ignored", events.KindDriftIgnored)
	require.Equal(t, "drift.superseded", events.KindDriftSuperseded)
	require.Equal(t, "credential.created", events.KindCredentialCreated)
	require.Equal(t, "credential.rotated", events.KindCredentialRotated)
	require.Equal(t, "credential.deleted", events.KindCredentialDeleted)
	require.Equal(t, "import.completed", events.KindImportCompleted)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/events/...`
Expected: FAIL，`undefined: events.KindConfigSetPublished`

- [ ] **Step 3: 加常量**

在 `hub/internal/events/writer.go` 已有的 const 块之后追加：

```go
// M1 新增（spec §8.5）。events.kind 是自由文本，加取值不需要迁移。
const (
	KindConfigSetPublished  = "configset.published"
	KindConfigSetRolledBack = "configset.rolled_back"
	KindAssignChanged       = "assign.changed"

	KindApplyOK             = "apply.ok"
	KindApplyFailed         = "apply.failed"
	KindApplyRollbackFailed = "apply.rollback_failed"

	KindDriftReported   = "drift.reported"
	KindDriftAdopted    = "drift.adopted"
	KindDriftRestored   = "drift.restored"
	KindDriftIgnored    = "drift.ignored"
	KindDriftSuperseded = "drift.superseded"

	KindCredentialCreated = "credential.created"
	KindCredentialRotated = "credential.rotated"
	KindCredentialDeleted = "credential.deleted"

	KindImportCompleted = "import.completed"
)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/events/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/events/
git commit -m "feat: 定义 M1 的事件 kind 常量"
```

---

### Task 3: blobs 层

**Files:**
- Create: `hub/internal/blobs/blobs.go`
- Test: `hub/internal/blobs/blobs_test.go`

**Interfaces:**
- Consumes: Task 1 的 `blobs` collection
- Produces: 见 00-overview「`hub/internal/blobs`」

**设计要点（spec §4.2 / §7.1）**：blob 永不 GC，除非删整个配置集——引用计数天然只增，所以这层只有 `Put` / `Get` / `Has` / `GCOrphans` 四个函数，没有引用计数字段。

- [ ] **Step 1: 写失败的测试**

Create `hub/internal/blobs/blobs_test.go`：

```go
package blobs_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestHashIsSHA256Hex(t *testing.T) {
	// 空内容的 sha256，写死期望值：这个口径进了 revision.files，改了等于历史全废。
	require.Equal(t,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		blobs.Hash(nil))
}

func TestPutIsDeduplicated(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	h1, err := s.Put([]byte("hello"))
	require.NoError(t, err)
	h2, err := s.Put([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, h1, h2)

	recs, err := app.FindAllRecords("blobs")
	require.NoError(t, err)
	require.Len(t, recs, 1, "同内容两次写只该落一份")
}

func TestGetRoundTrips(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	content := []byte("{\n  \"model\": \"opus\"\n}\n")
	h, err := s.Put(content)
	require.NoError(t, err)

	got, err := s.Get(h)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)
	_, err := s.Get("0000000000000000000000000000000000000000000000000000000000000000")
	require.ErrorIs(t, err, blobs.ErrNotFound)
}

func TestHas(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)
	h, err := s.Put([]byte("x"))
	require.NoError(t, err)

	ok, err := s.Has(h)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = s.Has(blobs.Hash([]byte("y")))
	require.NoError(t, err)
	require.False(t, ok)
}

// 并发写同一 hash：唯一索引会让其中一方失败，Put 必须把它当成「已存在」。
func TestConcurrentPutSameHash(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := s.Put([]byte("同一份内容"))
			errs <- err
		}()
	}
	for range n {
		require.NoError(t, <-errs)
	}
	recs, err := app.FindAllRecords("blobs")
	require.NoError(t, err)
	require.Len(t, recs, 1)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/blobs/...`
Expected: FAIL，`no required module provides package .../hub/internal/blobs`

- [ ] **Step 3: 实现**

Create `hub/internal/blobs/blobs.go`：

```go
// Package blobs 是内容寻址存储：一份内容全库只存一次，按 sha256 索引
// （spec §7.1）。
//
// 这层刻意薄：Revision 不可变、不可删，引用计数天然只增，因此不需要计数
// 字段，只有删配置集时才扫一次孤儿（spec §4.2）。
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

var ErrNotFound = errors.New("blobs: 内容不存在")

// Hash 返回内容的 sha256（hex）。这个口径进了 revision.files 与 wire，
// 不得更改。
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

type Store struct {
	app core.App
}

func New(app core.App) *Store { return &Store{app: app} }

// Put 写入内容并返回其 hash。已存在则直接返回，不重复落盘。
func (s *Store) Put(content []byte) (string, error) { return s.PutTx(s.app, content) }

// PutTx 在给定的 app（可能是事务）上写入。在事务里务必用它，
// 否则事务回滚了 blob 还留着。
func (s *Store) PutTx(txApp core.App, content []byte) (string, error) {
	h := Hash(content)

	if r, err := txApp.FindFirstRecordByData("blobs", "hash", h); err == nil && r != nil {
		return h, nil
	}

	c, err := txApp.FindCollectionByNameOrId("blobs")
	if err != nil {
		return "", fmt.Errorf("blobs: 找不到 collection: %w", err)
	}
	f, err := filesystem.NewFileFromBytes(content, h)
	if err != nil {
		return "", fmt.Errorf("blobs: 构造文件: %w", err)
	}
	r := core.NewRecord(c)
	r.Set("hash", h)
	r.Set("size", len(content))
	r.Set("content", f)
	if err := txApp.Save(r); err != nil {
		// 并发写同一 hash 时唯一索引会让后来者失败。此时对方已经写成功，
		// 结果与我们想要的完全一致，因此当成成功。
		if existing, e := txApp.FindFirstRecordByData("blobs", "hash", h); e == nil && existing != nil {
			return h, nil
		}
		return "", fmt.Errorf("blobs: 写入 %s: %w", h, err)
	}
	return h, nil
}

// Get 按 hash 取内容。
func (s *Store) Get(hash string) ([]byte, error) {
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	name := r.GetString("content")
	if name == "" {
		return nil, fmt.Errorf("%w: %s（记录在但文件为空）", ErrNotFound, hash)
	}

	fsys, err := s.app.NewFilesystem()
	if err != nil {
		return nil, fmt.Errorf("blobs: 打开存储: %w", err)
	}
	defer fsys.Close()

	rd, err := fsys.GetReader(r.BaseFilesPath() + "/" + name)
	if err != nil {
		return nil, fmt.Errorf("blobs: 读取 %s: %w", hash, err)
	}
	defer rd.Close()

	b, err := io.ReadAll(rd)
	if err != nil {
		return nil, fmt.Errorf("blobs: 读取 %s: %w", hash, err)
	}
	return b, nil
}

func (s *Store) Has(hash string) (bool, error) {
	r, err := s.app.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return false, nil
	}
	return true, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/blobs/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add hub/internal/blobs/
git commit -m "feat: 内容寻址的 blob 存储"
```

---

### Task 4: 孤儿 GC

**Files:**
- Modify: `hub/internal/blobs/blobs.go`
- Test: `hub/internal/blobs/blobs_test.go`

**Interfaces:**
- Produces: `func (s *Store) GCOrphans(setID string) (int, error)`

只在删配置集之后调用：扫一遍所有 revision 的 `files`、所有 config_set 的 `draft`、所有 `drift_events.current_blob`，凡是没被引用的 blob 删掉。`setID` 只用于日志与事件，不影响扫描范围（引用可能来自任何配置集）。

- [ ] **Step 1: 写失败的测试**

追加到 `hub/internal/blobs/blobs_test.go`：

```go
func TestGCOrphansOnlyDeletesUnreferenced(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	kept, err := s.Put([]byte("被 revision 引用"))
	require.NoError(t, err)
	inDraft, err := s.Put([]byte("被草稿引用"))
	require.NoError(t, err)
	orphan, err := s.Put([]byte("没人引用"))
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s1")
	set.Set("draft", []map[string]any{{"path": "a", "hash": inDraft, "size": 1, "mode": 420}})
	require.NoError(t, app.Save(set))

	revsC, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	rev := core.NewRecord(revsC)
	rev.Set("config_set", set.Id)
	rev.Set("seq", 1)
	rev.Set("files", []map[string]any{{"path": "b", "hash": kept, "size": 1, "mode": 420}})
	rev.Set("source", "publish")
	require.NoError(t, app.Save(rev))

	n, err := s.GCOrphans(set.Id)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	for _, h := range []string{kept, inDraft} {
		ok, err := s.Has(h)
		require.NoError(t, err)
		require.True(t, ok, "被引用的 blob 不该被删")
	}
	ok, err := s.Has(orphan)
	require.NoError(t, err)
	require.False(t, ok)
}
```

测试文件顶部补 import：`"github.com/pocketbase/pocketbase/core"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -tags=testing ./hub/internal/blobs/ -run GCOrphans -v`
Expected: FAIL，`s.GCOrphans undefined`

- [ ] **Step 3: 实现**

追加到 `hub/internal/blobs/blobs.go`：

```go
// GCOrphans 删除没有任何引用的 blob。只在删除整个配置集之后调用
// （spec §4.2：Revision 不可变不可删，平时引用计数只增）。
//
// setID 只用于日志：引用可能来自任何配置集，扫描范围永远是全库。
func (s *Store) GCOrphans(setID string) (int, error) {
	referenced := map[string]bool{}

	collect := func(collection, field string) error {
		recs, err := s.app.FindAllRecords(collection)
		if err != nil {
			return fmt.Errorf("blobs: 扫描 %s: %w", collection, err)
		}
		for _, r := range recs {
			var entries []struct {
				Hash string `json:"hash"`
			}
			// JSONField 读不了点号 key，必须走 UnmarshalJSONField。
			if err := r.UnmarshalJSONField(field, &entries); err != nil {
				continue // 空字段或形状不符：当作没有引用
			}
			for _, e := range entries {
				if e.Hash != "" {
					referenced[e.Hash] = true
				}
			}
		}
		return nil
	}
	if err := collect("revisions", "files"); err != nil {
		return 0, err
	}
	if err := collect("config_sets", "draft"); err != nil {
		return 0, err
	}

	drifts, err := s.app.FindAllRecords("drift_events")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 drift_events: %w", err)
	}
	for _, d := range drifts {
		if id := d.GetString("current_blob"); id != "" {
			if b, err := s.app.FindRecordById("blobs", id); err == nil && b != nil {
				referenced[b.GetString("hash")] = true
			}
		}
	}

	all, err := s.app.FindAllRecords("blobs")
	if err != nil {
		return 0, fmt.Errorf("blobs: 扫描 blobs: %w", err)
	}
	n := 0
	for _, b := range all {
		if referenced[b.GetString("hash")] {
			continue
		}
		if err := s.app.Delete(b); err != nil {
			return n, fmt.Errorf("blobs: 删除孤儿 %s: %w", b.GetString("hash"), err)
		}
		n++
	}
	s.app.Logger().Info("清理孤儿 blob", "config_set", setID, "deleted", n)
	return n, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -tags=testing ./hub/internal/blobs/...`
Expected: PASS

- [ ] **Step 5: 全量回归并提交**

Run: `go test -tags=testing ./...`
Expected: PASS

```bash
git add hub/internal/blobs/
git commit -m "feat: blob 孤儿回收"
```
