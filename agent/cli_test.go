package agent

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/state"
)

// bytes 仍被 TestVersionCommandPrintsVersion 使用。

// runCLI 跑一条 agent 子命令，合并 stdout/stderr 返回。
func runCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	return RunCLIForTest(dir, args...)
}

// seedEnrolled 写 agent.yml + identity，模拟 enroll 之后的本机状态。
func seedEnrolled(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, SaveConfig(dir, &Config{
		HubURL: "https://hub.example", MachineID: "m1",
		ManagedHome: t.TempDir(),
	}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)
}

func TestVersionCommandPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	require.NoError(t, root.Execute())
	require.Equal(t, orciny.Version+"\n", out.String())
}

func TestRootCommandName(t *testing.T) {
	require.Equal(t, "orciny-agent", newRootCmd().Use)
}

func TestStatusCommandReportsNotRunning(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://hub.example", MachineID: "m1"}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"status", "--dir", dir})
	require.NoError(t, root.Execute())

	require.Contains(t, out.String(), "https://hub.example")
	require.Contains(t, out.String(), "未运行")
	require.NotContains(t, out.String(), "PRIVATE KEY")
}

func TestStatusCommandReportsSavedState(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://hub.example", MachineID: "m1"}))
	_, err := identity.LoadOrCreate(filepath.Join(dir, identity.DirName))
	require.NoError(t, err)
	require.NoError(t, SaveStatus(dir, &Status{
		State: "connected", Since: time.Now().UTC(),
		HubURL: "https://hub.example", Version: "0.1.0",
	}))

	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetArgs([]string{"status", "--dir", dir})
	require.NoError(t, root.Execute())

	require.Contains(t, out.String(), "connected")
}

func TestStatusCommandFailsWithoutEnroll(t *testing.T) {
	var out bytes.Buffer
	root := newRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"status", "--dir", t.TempDir()})
	err := root.Execute()
	require.Error(t, err, "还没 enroll 时应当明确报错")
	require.Contains(t, err.Error(), "尚未 enroll", "错误里要说清楚下一步做什么，而不是甩一个 ENOENT")
}

func TestSyncCommandFailsWithoutEnroll(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, dir, "sync")
	require.Error(t, err)
	// 错误可能在 err 或合并后的 out 里（cobra 会把 RunE 的 error 打到 stderr）
	combined := out
	if err != nil {
		combined += err.Error()
	}
	require.Contains(t, combined, "尚未 enroll")
}

func TestStatusShowsConfigFields(t *testing.T) {
	dir := t.TempDir()
	seedEnrolled(t, dir)
	require.NoError(t, state.Save(dir, &state.State{
		ConfigSet: "set1", Revision: "rev7", Seq: 7,
		Mode: "apply", Health: state.HealthDegraded,
		Files: map[string]state.FileState{".claude/CLAUDE.md": {Rendered: "x"}},
	}))

	out, err := runCLI(t, dir, "status")
	require.NoError(t, err)
	require.Contains(t, out, "rev7")
	require.Contains(t, out, "degraded")
	require.Contains(t, out, "apply")
}

// 没有 state.json 时 status 也要能跑，只是说「尚未应用任何配置」。
func TestStatusWithoutState(t *testing.T) {
	dir := t.TempDir()
	seedEnrolled(t, dir)
	out, err := runCLI(t, dir, "status")
	require.NoError(t, err)
	require.Contains(t, out, "尚未应用")
}
