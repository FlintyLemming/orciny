package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/events"
)

func newMachine(t *testing.T, app *tests.TestApp, fp string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("fingerprint", fp)
	r.Set("pub_key", "pk-"+fp)
	r.Set("status", "offline")
	require.NoError(t, app.Save(r))
	return r.Id
}

func TestAssignCreatesThenUpdates(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp1")
	a, err := s.Create("a", "")
	require.NoError(t, err)
	b, err := s.Create("b", "")
	require.NoError(t, err)

	rec, err := s.Assign(m, a.Id, "survey")
	require.NoError(t, err)
	require.Equal(t, "survey", rec.GetString("mode"))
	require.Equal(t, "pending", rec.GetString("state"))

	rec2, err := s.Assign(m, b.Id, "apply")
	require.NoError(t, err)
	require.Equal(t, rec.Id, rec2.Id, "一机一配置集：改指派是更新同一条记录")
	require.Equal(t, b.Id, rec2.GetString("config_set"))
	require.Equal(t, "apply", rec2.GetString("mode"))
	require.Equal(t, "pending", rec2.GetString("state"), "换配置集要回到 pending")

	kinds := []string{}
	recs, err := app.FindAllRecords("events")
	require.NoError(t, err)
	for _, r := range recs {
		kinds = append(kinds, r.GetString("kind"))
	}
	require.Contains(t, kinds, events.KindAssignChanged)
}

func TestAssignRejectsUnknownMode(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp2")
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.Assign(m, set.Id, "whatever")
	require.Error(t, err)
}

func TestAssignedMachines(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	m1 := newMachine(t, app, "fp3")
	m2 := newMachine(t, app, "fp4")
	_, err = s.Assign(m1, set.Id, "apply")
	require.NoError(t, err)
	_, err = s.Assign(m2, set.Id, "survey")
	require.NoError(t, err)

	got, err := s.AssignedMachines(set.Id)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{m1, m2}, got)
}

func TestAssignmentOfUnassignedMachine(t *testing.T) {
	app, s := newService(t)
	m := newMachine(t, app, "fp5")
	_, err := s.Assignment(m)
	require.ErrorIs(t, err, configsets.ErrNoAssignment)
}
