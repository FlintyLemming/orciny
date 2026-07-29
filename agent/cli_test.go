package agent

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
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
