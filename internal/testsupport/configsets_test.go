//go:build testing

package testsupport_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/testsupport"
)

func TestSeedConfigSetPublishesRevision(t *testing.T) {
	th := testsupport.NewTestHub(t)
	setID, revID := th.SeedConfigSet(t, "主力", map[string]string{
		".claude/CLAUDE.md":     "# 规矩\n",
		".claude/settings.json": `{"model":"opus"}`,
	})
	require.NotEmpty(t, setID)
	require.NotEmpty(t, revID)

	set, err := th.App.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	require.Equal(t, revID, set.GetString("head"))

	rev, err := th.App.FindRecordById("revisions", revID)
	require.NoError(t, err)
	require.EqualValues(t, 1, rev.GetInt("seq"))
}
