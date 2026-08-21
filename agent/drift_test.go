package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/manifest"
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

	out, err := runCLIPublic(t, dir, "drift")
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

// —— 测试辅助 ——

// runCLIPublic 与 agent 包内的 runCLI 同形，供 agent_test 使用。
func runCLIPublic(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	// 通过 agent.Execute 不方便改 args；直接复用 cobra 根命令需要在 agent 包内。
	// 这里用环境变量 + 子进程太重。改为调用公开 API 后手写格式化不现实。
	// 解决：把 CLI 入口通过 agent 包的测试导出——更简单的是用 os/exec 跑编译产物。
	// 计划里的 runCLI 在 cli_test.go（package agent）。agent_test 调公开函数：
	return agent.RunCLIForTest(dir, args...)
}

func seedManagedBaseline(t *testing.T, dir, home string, files map[string]string) {
	t.Helper()
	seedManagedBaselineWith(t, dir, home, files, nil, nil)
}

func seedManagedBaselineWithSecret(t *testing.T, dir, home, secret string) {
	t.Helper()
	baseline := `{"K":"sk-real-secret-9999"}`
	// 磁盘上是明文，blob 里是占位符形态。
	seedManagedBaselineWith(t, dir, home,
		map[string]string{".claude/settings.json": baseline},
		map[string]string{"k": secret},
		nil,
	)
}

func seedManagedBaselineWith(
	t *testing.T,
	dir, home string,
	files map[string]string,
	creds, vars map[string]string,
) {
	t.Helper()
	require.NoError(t, agent.SaveConfig(dir, &agent.Config{
		HubURL: "https://hub.example", MachineID: "m1", ManagedHome: home,
	}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)

	if creds == nil {
		creds = map[string]string{}
	}
	if vars == nil {
		vars = map[string]string{}
	}
	sec := &secrets.File{Creds: creds, Vars: vars, Machine: map[string]string{}}
	require.NoError(t, secrets.Save(dir, sec))

	mj, err := manifest.Default().JSON()
	require.NoError(t, err)

	st := &state.State{
		ConfigSet: "set1", Revision: "rev1", Seq: 1,
		Mode: "apply", Health: state.HealthOK,
		Files:    map[string]state.FileState{},
		Manifest: mj,
	}
	cache := blobcache.New(dir)
	for rel, disk := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(disk), 0o644))

		// blob 存还原后的占位符形态（与 apply 落盘前一致）。
		res := render.Restore([]byte(disk), render.Values{Creds: creds, Vars: vars})
		require.True(t, res.Safe, "seed 的基线必须能安全还原")
		blob, err := cache.Put(res.Content)
		require.NoError(t, err)
		st.Files[rel] = state.FileState{
			Blob: blob, Rendered: blobcache.Hash([]byte(disk)),
			Mode: 0o644, Size: uint32(len(disk)),
		}
	}
	require.NoError(t, state.Save(dir, st))
}
