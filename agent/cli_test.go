package agent

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
)

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
