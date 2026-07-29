package agent

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatusRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Status{
		State:       "connected",
		Since:       time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		HubURL:      "https://hub.example.com",
		Fingerprint: "abc",
		Version:     "0.1.0",
	}
	require.NoError(t, SaveStatus(dir, in))

	out, err := LoadStatus(dir)
	require.NoError(t, err)
	require.Equal(t, in.State, out.State)
	require.Equal(t, in.HubURL, out.HubURL)
	require.True(t, in.Since.Equal(out.Since))
}

func TestLoadStatusMissing(t *testing.T) {
	_, err := LoadStatus(t.TempDir())
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStatusFileNeverContainsKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveStatus(dir, &Status{State: "connected", Fingerprint: "abc"}))

	b, err := os.ReadFile(dir + "/" + StatusFileName)
	require.NoError(t, err)
	require.NotContains(t, string(b), "PRIVATE KEY")
	require.NotContains(t, string(b), "pub_key")
}
