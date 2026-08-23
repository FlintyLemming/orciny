package variables_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/variables"
)

func newStore(t *testing.T) (*tests.TestApp, *variables.Store, string) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	mc, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(mc)
	m.Set("fingerprint", "fp")
	m.Set("pub_key", "pk")
	m.Set("status", "offline")
	require.NoError(t, app.Save(m))

	return app, variables.NewStore(app), m.Id
}

func TestVariablesRoundTrip(t *testing.T) {
	_, s, machineID := newStore(t)

	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))
	require.NoError(t, s.SetVariable(machineID, "tier", "1"))

	got, err := s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"workspace": "main", "tier": "1"}, got)

	// 同 key 再设一次是更新，不是新增
	require.NoError(t, s.SetVariable(machineID, "workspace", "side"))
	got, err = s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Equal(t, "side", got["workspace"])
	require.Len(t, got, 2)
}

func TestDeleteVariable(t *testing.T) {
	_, s, machineID := newStore(t)
	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))
	require.NoError(t, s.DeleteVariable(machineID, "workspace"))

	got, err := s.MachineVariables(machineID)
	require.NoError(t, err)
	require.Empty(t, got)

	// 删不存在的变量是幂等的，不是错误
	require.NoError(t, s.DeleteVariable(machineID, "workspace"))
}

func TestSetVariableRejectsBadName(t *testing.T) {
	_, s, machineID := newStore(t)
	err := s.SetVariable(machineID, "not a name", "v")
	require.ErrorIs(t, err, variables.ErrBadName)
}

// 机器变量与秘密加密没有任何共享代码——这就是它没有留在 secretbox 里的理由
// （M1.6 spec §2.5）。这条测试锁住「值是明文落库」这个既定语义。
func TestVariablesAreStoredInPlaintext(t *testing.T) {
	app, s, machineID := newStore(t)
	require.NoError(t, s.SetVariable(machineID, "workspace", "main"))

	recs, err := app.FindAllRecords("variables")
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "main", recs[0].GetString("value"))
}
