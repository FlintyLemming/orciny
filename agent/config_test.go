package agent

import (
	"os"
	"path/filepath"
	"testing"

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
