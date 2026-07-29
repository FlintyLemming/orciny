package migrations_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

// newApp 起一个跑完全部迁移的临时 PocketBase。
// tests.NewTestApp 会克隆给定目录并执行 RunAllMigrations，
// 我们的迁移在 init() 里已注册进 core.AppMigrations，因此自动被应用。
func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestCollectionsExist(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{"machines", "enroll_tokens", "events"} {
		c, err := app.FindCollectionByNameOrId(name)
		require.NoError(t, err, "collection %s 必须存在", name)
		require.Nil(t, c.ListRule, "%s 的 list rule 必须是 nil（仅 superuser）", name)
		require.Nil(t, c.ViewRule, "%s 的 view rule 必须是 nil", name)
		require.Nil(t, c.CreateRule, "%s 的 create rule 必须是 nil", name)
		require.Nil(t, c.UpdateRule, "%s 的 update rule 必须是 nil", name)
		require.Nil(t, c.DeleteRule, "%s 的 delete rule 必须是 nil", name)
	}
}

func TestMachinesFields(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	for _, f := range []string{
		"name", "fingerprint", "pub_key", "hostname", "os", "arch",
		"agent_version", "tool_versions", "status", "last_seen", "created", "updated",
	} {
		require.NotNil(t, c.Fields.GetByName(f), "machines.%s 缺失", f)
	}

	status, ok := c.Fields.GetByName("status").(*core.SelectField)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"online", "offline", "paused"}, status.Values)
}

func TestMachinesFingerprintIsUnique(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)

	mk := func() *core.Record {
		r := core.NewRecord(c)
		r.Set("fingerprint", "same-fp")
		r.Set("pub_key", "k")
		r.Set("status", "offline")
		return r
	}
	require.NoError(t, app.Save(mk()))
	require.Error(t, app.Save(mk()), "同一 fingerprint 必须被唯一索引挡住")
}

func TestEnrollTokensAndEventsFields(t *testing.T) {
	app := newApp(t)

	tk, err := app.FindCollectionByNameOrId("enroll_tokens")
	require.NoError(t, err)
	for _, f := range []string{"token_hash", "expires_at", "used_at", "machine"} {
		require.NotNil(t, tk.Fields.GetByName(f), "enroll_tokens.%s 缺失", f)
	}

	ev, err := app.FindCollectionByNameOrId("events")
	require.NoError(t, err)
	for _, f := range []string{"kind", "machine", "detail", "created"} {
		require.NotNil(t, ev.Fields.GetByName(f), "events.%s 缺失", f)
	}

	rel, ok := ev.Fields.GetByName("machine").(*core.RelationField)
	require.True(t, ok)
	machines, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	require.Equal(t, machines.Id, rel.CollectionId)
	require.False(t, rel.Required, "机器被删后事件仍要保留，因此 machine 可空")
}
