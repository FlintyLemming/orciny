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
	// revID 是一条真实的 revision 记录 id。shadowed_rev 是指向 revisions
	// 的 relation，PocketBase 会校验 id 存在——不能拿字符串字面量凑数。
	revID string
	saves *int
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
		app: app, blobs: b, machineID: m.Id, revID: newRevision(t, app), saves: &saves,
		svc: overrides.NewService(app, b, events.NewWriter(app), nil),
	}
}

// newRevision 建一条最小的 revision，供 shadowed_rev 指向。
func newRevision(t *testing.T, app *tests.TestApp) string {
	t.Helper()
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "覆盖层测试集")
	require.NoError(t, app.Save(set))

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	rev := core.NewRecord(revs)
	rev.Set("config_set", set.Id)
	rev.Set("seq", 1)
	rev.Set("source", "publish")
	require.NoError(t, app.Save(rev))
	return rev.Id
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
	out, err := r.svc.Apply(r.machineID, r.revID, files)
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
	out, err := r.svc.Apply(r.machineID, r.revID, in)
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
	require.Equal(t, r.revID, rec.GetString("shadowed_rev"))
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
	out, err := r.svc.Apply(r.machineID, r.revID, []protocol.FileEntry{other})
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
