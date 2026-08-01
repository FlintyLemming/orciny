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
