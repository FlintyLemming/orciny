package drift_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
)

type rig struct {
	app       *tests.TestApp
	blobs     *blobs.Store
	sets      *configsets.Service
	revs      *revisions.Service
	events    *events.Writer
	svc       *drift.Service
	machineID string
	setID     string
	revID     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	b := blobs.New(app)
	ev := events.NewWriter(app)
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	_ = credentials.NewStore(app, key, ev) // 确保主密钥存在，后续若需凭据可复用

	r := &rig{
		app:    app,
		blobs:  b,
		sets:   configsets.NewService(app, b, ev),
		revs:   revisions.NewService(app, b, ev),
		events: ev,
	}
	r.svc = drift.NewService(drift.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Events: ev,
	})

	// 预置一台机器
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", "fp-drift-1")
	m.Set("pub_key", "pk-drift-1")
	m.Set("status", "online")
	require.NoError(t, app.Save(m))
	r.machineID = m.Id

	return r
}

// assign 发布一份最小配置集并指派给本机。
func (r *rig) assign(t *testing.T) {
	t.Helper()
	set, err := r.sets.Create("主力", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, ".claude/CLAUDE.md", []byte("原始规矩\n"), 0o644, nil)
	require.NoError(t, err)
	rev, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, configsets.ModeApply)
	require.NoError(t, err)
	r.setID = set.Id
	r.revID = rev.Id
}

func (r *rig) drifts(t *testing.T) []*core.Record {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("drift_events",
		"machine = {:m}", "path", 0, 0, map[string]any{"m": r.machineID})
	require.NoError(t, err)
	return recs
}

func (r *rig) openDrifts(t *testing.T) []*core.Record {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("drift_events",
		"machine = {:m} && state = 'open'", "path", 0, 0,
		map[string]any{"m": r.machineID})
	require.NoError(t, err)
	return recs
}

func (r *rig) driftsByPath(t *testing.T) map[string]*core.Record {
	t.Helper()
	out := map[string]*core.Record{}
	for _, rec := range r.drifts(t) {
		out[rec.GetString("path")] = rec
	}
	return out
}

func (r *rig) hashOf(t *testing.T, path string) string {
	t.Helper()
	files, err := r.revs.Files(r.revID)
	require.NoError(t, err)
	for _, f := range files {
		if f.Path == path {
			return f.Hash
		}
	}
	t.Fatalf("版本里没有路径 %s", path)
	return ""
}

func (r *rig) resolve(t *testing.T, path, state string) {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("drift_events",
		"machine = {:m} && path = {:p} && state = 'open'", "", 1, 0,
		map[string]any{"m": r.machineID, "p": path})
	require.NoError(t, err)
	require.Len(t, recs, 1)
	recs[0].Set("state", state)
	require.NoError(t, r.app.Save(recs[0]))
}

func (r *rig) addIgnoreRule(t *testing.T, machineID, path string) {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("ignore_rules")
	require.NoError(t, err)
	rec := core.NewRecord(c)
	if machineID != "" {
		rec.Set("machine", machineID)
	}
	rec.Set("path", path)
	require.NoError(t, r.app.Save(rec))
}

func (r *rig) otherMachineID(t *testing.T) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", "fp-drift-other")
	m.Set("pub_key", "pk-drift-other")
	m.Set("status", "offline")
	require.NoError(t, r.app.Save(m))
	return m.Id
}

func (r *rig) requireEvent(t *testing.T, kind string) {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("events",
		"kind = {:k} && machine = {:m}", "", 1, 0,
		map[string]any{"k": kind, "m": r.machineID})
	require.NoError(t, err)
	require.NotEmpty(t, recs, "应有事件 %s", kind)
}
