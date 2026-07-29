package probe_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/probe"
)

func TestHostReportsRuntimeValues(t *testing.T) {
	hostname, goos, goarch := probe.Host()

	require.Equal(t, runtime.GOOS, goos)
	require.Equal(t, runtime.GOARCH, goarch)

	expected, err := os.Hostname()
	require.NoError(t, err)
	require.Equal(t, expected, hostname)
	require.NotEmpty(t, hostname)
}
