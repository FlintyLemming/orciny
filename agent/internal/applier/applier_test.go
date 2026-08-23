package applier_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type fixture struct {
	dir     string
	home    string
	clk     *clock.Fake
	failOn  map[string]bool // 相对路径 → 写它时报错
	applier *applier.Applier
}

// failFS 包一层 OSFS，指定路径的写会失败。这是唯一能可靠触发回滚的办法。
type failFS struct {
	inner applier.FS
	home  string
	fail  map[string]bool
}

func (f failFS) Write(path string, data []byte, perm os.FileMode) error {
	rel, _ := filepath.Rel(f.home, path)
	if f.fail[filepath.ToSlash(rel)] {
		return os.ErrPermission
	}
	return f.inner.Write(path, data, perm)
}
func (f failFS) Read(path string) ([]byte, error)      { return f.inner.Read(path) }
func (f failFS) Remove(path string) error              { return f.inner.Remove(path) }
func (f failFS) Stat(path string) (os.FileInfo, error) { return f.inner.Stat(path) }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	// macOS 的 /var → /private/var：ResolveUnder 会 EvalSymlinks，
	// failFS 用相对路径匹配时必须同一口径，否则写失败注入永远不触发。
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	fx := &fixture{
		dir:    t.TempDir(),
		home:   home,
		clk:    clock.NewFake(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)),
		failOn: map[string]bool{},
	}
	fx.applier = applier.New(applier.Options{
		Dir:         fx.dir,
		ManagedHome: fx.home,
		Clock:       fx.clk,
		FS:          failFS{inner: applier.OSFS(), home: fx.home, fail: fx.failOn},
	})
	return fx
}

func (fx *fixture) write(t *testing.T, rel, content string, perm os.FileMode) {
	t.Helper()
	p := filepath.Join(fx.home, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), perm))
}

func (fx *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fx.home, rel))
	require.NoError(t, err)
	return string(b)
}

func TestApplyCreatesAndOverwrites(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/CLAUDE.md", "旧内容", 0o644)

	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":     []byte("新内容"),
		".claude/settings.json": []byte(`{"K":"{{provider.claude.auth_token}}"}`),
	}, map[string]uint32{".claude/settings.json": 0o600})

	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: blobcache.Hash([]byte("旧内容")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK, "错误：%s", ack.Error)
	require.Equal(t, "新内容", fx.read(t, ".claude/CLAUDE.md"))
	require.Equal(t, `{"K":"sk-real-value"}`, fx.read(t, ".claude/settings.json"))

	// 权限位：含凭据 0600，其余 0644（spec §7.4）
	info, err := os.Stat(filepath.Join(fx.home, ".claude/settings.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	info, err = os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	require.Equal(t, snap.RevisionID, next.Revision)
	require.Equal(t, snap.Checksum, next.Checksum)
	require.Equal(t, state.HealthOK, next.Health)
	require.Equal(t, blobcache.Hash([]byte("新内容")), next.Files[".claude/CLAUDE.md"].Rendered)
}

func TestApplyDeletes(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/gone.md", "待删", 0o644)

	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("keep")}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/gone.md": {Blob: "a", Rendered: blobcache.Hash([]byte("待删")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK)
	require.NoFileExists(t, filepath.Join(fx.home, ".claude/gone.md"))
	require.NotContains(t, next.Files, ".claude/gone.md")
}

// 幂等重放：同一 revision apply 两次，第二次全 skip、零写入（spec §7.3）。
func TestApplyIsIdempotent(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("内容")}, nil)

	p1, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)
	_, st1 := fx.applier.Apply(snap, p1, nil)

	p2, err := applier.BuildPlan(snap, st1, blobs, sec().Lookup)
	require.NoError(t, err)
	require.Equal(t, 0, p2.Writes(), "第二次必须零写入")

	before, err := os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p2, st1)
	require.True(t, ack.OK)
	after, err := os.Stat(filepath.Join(fx.home, ".claude/CLAUDE.md"))
	require.NoError(t, err)
	require.Equal(t, before.ModTime(), after.ModTime(), "skip 不该碰文件")
}

// 失败回滚：注入一个写失败，断言全部路径回到 apply 前状态（spec §7.4）。
func TestApplyRollsBackOnFailure(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/CLAUDE.md", "原始 A", 0o644)
	fx.write(t, ".claude/b.md", "原始 B", 0o644)
	fx.failOn[".claude/z-fails.md"] = true

	snap, blobs := snapFor(map[string][]byte{
		".claude/CLAUDE.md":  []byte("新 A"),
		".claude/b.md":       []byte("新 B"),
		".claude/z-fails.md": []byte("写不进去"),
	}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/CLAUDE.md": {Blob: "x", Rendered: blobcache.Hash([]byte("原始 A")), Mode: 0o644},
		".claude/b.md":      {Blob: "y", Rendered: blobcache.Hash([]byte("原始 B")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, next := fx.applier.Apply(snap, p, st)
	require.False(t, ack.OK)
	require.True(t, ack.RolledBack)
	require.Equal(t, "原始 A", fx.read(t, ".claude/CLAUDE.md"), "必须回到 apply 前")
	require.Equal(t, "原始 B", fx.read(t, ".claude/b.md"))
	require.NoFileExists(t, filepath.Join(fx.home, ".claude/z-fails.md"))
	require.Equal(t, st.Revision, next.Revision, "回滚成功时 state 不前进")
	require.Equal(t, state.HealthOK, next.Health, "回滚成功不进 degraded")
}

// 回滚本身失败 → degraded，此后拒绝一切自动 apply（spec §7.4 第 2 条）。
func TestRollbackFailureEntersDegraded(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/a.md", "原始 A", 0o644)
	// a.md 先被成功改写，随后 z 失败触发回滚——但回滚要重写 a.md，
	// 而此刻 a.md 也写不动了。
	fx.failOn[".claude/z-fails.md"] = true

	snap, blobs := snapFor(map[string][]byte{
		".claude/a.md":       []byte("新 A"),
		".claude/z-fails.md": []byte("写不进去"),
	}, nil)
	st := &state.State{Files: map[string]state.FileState{
		".claude/a.md": {Blob: "x", Rendered: blobcache.Hash([]byte("原始 A")), Mode: 0o644},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)

	// 让 a.md 的**回滚写**也失败：在 z 失败之后才把 a.md 加进 failOn。
	// failFS 读的是同一张 map，applier 逐步执行，因此这里直接把两个都设上，
	// 并让 a.md 的第一次写走「先成功再失败」——用计数器实现。
	fx.failOn[".claude/a.md"] = false

	ack, next := fx.applier.ApplyWithHook(snap, p, st, func(rel string, done int) {
		if rel == ".claude/a.md" {
			fx.failOn[".claude/a.md"] = true // 之后的回滚写会失败
		}
	})
	require.False(t, ack.OK)
	require.False(t, ack.RolledBack, "回滚没成功")
	require.Equal(t, state.HealthDegraded, next.Health)

	// degraded 之后拒绝一切自动 apply
	ack2, _ := fx.applier.Apply(snap, p, next)
	require.False(t, ack2.OK)
	require.Contains(t, ack2.Error, "degraded")
}

// apply 前快照：所有将被写/删的路径的当前内容原样留一份（spec §7.4）。
func TestSnapshotIsTakenBeforeWriting(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, ".claude/settings.json", `{"K":"sk-真实密钥"}`, 0o600)

	snap, blobs := snapFor(map[string][]byte{
		".claude/settings.json": []byte(`{"K":"{{provider.claude.auth_token}}"}`),
	}, map[string]uint32{".claude/settings.json": 0o600})
	st := &state.State{Files: map[string]state.FileState{
		".claude/settings.json": {Blob: "x", Rendered: "old", Mode: 0o600},
	}}
	p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
	require.NoError(t, err)
	ack, _ := fx.applier.Apply(snap, p, st)
	require.True(t, ack.OK)

	entries, err := os.ReadDir(filepath.Join(fx.dir, "snapshots"))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	snapDir := filepath.Join(fx.dir, "snapshots", entries[0].Name())
	info, err := os.Stat(snapDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	saved, err := os.ReadFile(filepath.Join(snapDir, ".claude/settings.json"))
	require.NoError(t, err)
	require.Equal(t, `{"K":"sk-真实密钥"}`, string(saved), "快照存的是原样内容，含真实值")
	require.FileExists(t, filepath.Join(snapDir, "manifest.json"))
}

func TestSnapshotsKeepOnly5(t *testing.T) {
	fx := newFixture(t)
	for i := range 8 {
		fx.clk.Advance(time.Second)
		snap, blobs := snapFor(map[string][]byte{
			".claude/CLAUDE.md": []byte(string(rune('a' + i))),
		}, nil)
		st, _ := state.Load(fx.dir)
		p, err := applier.BuildPlan(snap, st, blobs, sec().Lookup)
		require.NoError(t, err)
		ack, next := fx.applier.Apply(snap, p, st)
		require.True(t, ack.OK)
		require.NoError(t, state.Save(fx.dir, next))
	}
	entries, err := os.ReadDir(filepath.Join(fx.dir, "snapshots"))
	require.NoError(t, err)
	require.Len(t, entries, applier.SnapshotKeep)
}

func TestAckCarriesResultsAndDuration(t *testing.T) {
	fx := newFixture(t)
	snap, blobs := snapFor(map[string][]byte{".claude/CLAUDE.md": []byte("x")}, nil)
	p, err := applier.BuildPlan(snap, nil, blobs, sec().Lookup)
	require.NoError(t, err)

	ack, _ := fx.applier.Apply(snap, p, nil)
	require.True(t, ack.OK)
	require.Equal(t, snap.RevisionID, ack.RevisionID)
	require.Len(t, ack.Results, 1)
	require.Equal(t, protocol.ActionCreate, ack.Results[0].Action)
	require.Equal(t, ".claude/CLAUDE.md", ack.Results[0].Path)
}
