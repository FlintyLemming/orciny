package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSaveLoadConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Config{HubURL: "https://hub.example.com", MachineID: "abc123", LogFile: true}
	require.NoError(t, SaveConfig(dir, in))

	out, err := LoadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestConfigFileIsYAMLWithSnakeCaseKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveConfig(dir, &Config{HubURL: "https://h", MachineID: "m"}))

	b, err := os.ReadFile(filepath.Join(dir, "agent.yml"))
	require.NoError(t, err)
	require.Contains(t, string(b), "hub_url: https://h")
	require.Contains(t, string(b), "machine_id: m")
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDefaultDirHonoursOrcinyHome(t *testing.T) {
	t.Setenv("ORCINY_HOME", "/custom/place")
	require.Equal(t, "/custom/place", DefaultDir())
}

func TestDefaultDirFallsBackToHome(t *testing.T) {
	t.Setenv("ORCINY_HOME", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".orciny"), DefaultDir())
}

func TestManagedHomeDefaultsToUserHome(t *testing.T) {
	c := &Config{}
	got, err := c.ManagedHomeDir()
	require.NoError(t, err)
	want, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestManagedHomeIsExplicitWhenSet(t *testing.T) {
	dir := t.TempDir()
	c := &Config{ManagedHome: dir}
	got, err := c.ManagedHomeDir()
	require.NoError(t, err)
	require.Equal(t, dir, got)
}

func TestReconcileEveryDefaultsTo5m(t *testing.T) {
	require.Equal(t, 5*time.Minute, (&Config{}).ReconcileEvery())
	require.Equal(t, 30*time.Second, (&Config{ReconcileInterval: "30s"}).ReconcileEvery())
	// 解不出来就退回默认，不让一个手抖的配置把对账整个关掉
	require.Equal(t, 5*time.Minute, (&Config{ReconcileInterval: "五分钟"}).ReconcileEvery())
	// 过小的值会把 CPU 烧掉，钉到下限
	require.Equal(t, 10*time.Second, (&Config{ReconcileInterval: "1s"}).ReconcileEvery())
}

func TestConfigRoundTripKeepsNewFields(t *testing.T) {
	dir := t.TempDir()
	want := &Config{
		HubURL:            "https://hub.example",
		MachineID:         "m1",
		ManagedHome:       "/tmp/managed",
		ReconcileInterval: "1m",
	}
	require.NoError(t, SaveConfig(dir, want))
	got, err := LoadConfig(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
