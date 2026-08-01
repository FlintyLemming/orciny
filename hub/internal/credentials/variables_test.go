package credentials_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"
)

func TestVariablesRoundTrip(t *testing.T) {
	app, s := newStore(t)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp")
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	require.NoError(t, s.SetVariable(m.Id, "workspace", "main"))
	require.NoError(t, s.SetVariable(m.Id, "tier", "1"))

	got, err := s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"workspace": "main", "tier": "1"}, got)

	// 同 key 再设一次是更新，不是新增
	require.NoError(t, s.SetVariable(m.Id, "workspace", "side"))
	got, err = s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.Equal(t, "side", got["workspace"])

	require.NoError(t, s.DeleteVariable(m.Id, "tier"))
	got, err = s.MachineVariables(m.Id)
	require.NoError(t, err)
	require.NotContains(t, got, "tier")
}

func TestMachineVariablesOfUnknownMachineIsEmpty(t *testing.T) {
	_, s := newStore(t)
	got, err := s.MachineVariables("nope")
	require.NoError(t, err)
	require.Empty(t, got)
}
