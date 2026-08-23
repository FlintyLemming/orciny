package drift_test

import (
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
	"github.com/FlintyLemming/orciny/hub/internal/variables"
	"github.com/FlintyLemming/orciny/protocol"
)

type sentMsg struct {
	machine string
	kind    protocol.Kind
	payload any
}

type fakeSender struct {
	mu     sync.Mutex
	sent   []sentMsg
	online map[string]bool
}

func (f *fakeSender) SendTo(machineID string, kind protocol.Kind, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMsg{machineID, kind, payload})
	return nil
}

func (f *fakeSender) Online(machineID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.online[machineID]
}

func (f *fakeSender) of(kind protocol.Kind) []sentMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []sentMsg
	for _, m := range f.sent {
		if m.kind == kind {
			out = append(out, m)
		}
	}
	return out
}

type rig struct {
	app       *tests.TestApp
	blobs     *blobs.Store
	sets      *configsets.Service
	revs      *revisions.Service
	events    *events.Writer
	creds     *credentials.Store
	provs     *providers.Store
	sync      *configsync.Service
	sender    *fakeSender
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
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	creds := credentials.NewStore(app, key, ev)
	vars := variables.NewStore(app)

	sender := &fakeSender{online: map[string]bool{}}
	sets := configsets.NewService(app, b, ev)
	revs := revisions.NewService(app, b, ev)
	provs := providers.NewStore(app, ev)
	syncSvc := configsync.NewService(configsync.Deps{
		App: app, Blobs: b, Sets: sets, Revs: revs, Creds: creds, Vars: vars,
		Providers: provs, Events: ev, Sender: sender,
	})

	r := &rig{
		app:    app,
		blobs:  b,
		sets:   sets,
		revs:   revs,
		events: ev,
		creds:  creds,
		provs:  provs,
		sync:   syncSvc,
		sender: sender,
	}
	r.svc = drift.NewService(drift.Deps{
		App: app, Blobs: b, Sets: r.sets, Revs: r.revs, Events: ev,
		Sync: r.sync, Providers: provs, Creds: creds,
	})
	r.sync.SetDrift(r.svc)

	// 预置一台机器
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", "fp-drift-1")
	m.Set("pub_key", "pk-drift-1")
	m.Set("status", "online")
	m.Set("name", "主力机")
	m.Set("agent_version", orciny.Version)
	require.NoError(t, app.Save(m))
	r.machineID = m.Id
	r.sender.online[m.Id] = true

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

func (r *rig) createMachine(t *testing.T, fp, name string) string {
	t.Helper()
	c, err := r.app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	m := core.NewRecord(c)
	m.Set("fingerprint", fp)
	m.Set("pub_key", "pk-"+fp)
	m.Set("status", "online")
	if name != "" {
		m.Set("name", name)
	}
	require.NoError(t, r.app.Save(m))
	r.sender.online[m.Id] = true
	return m.Id
}

// secondMachine 再建一台机器并指派到同一配置集。
func (r *rig) secondMachine(t *testing.T) string {
	t.Helper()
	id := r.createMachine(t, "fp-drift-2", "副机")
	if r.setID != "" {
		_, err := r.sets.Assign(id, r.setID, configsets.ModeApply)
		require.NoError(t, err)
	}
	return id
}

// secondMachineWithOwnSet 再建一台机器，配独立配置集并发布。
func (r *rig) secondMachineWithOwnSet(t *testing.T) (machineID, setID string) {
	t.Helper()
	machineID = r.createMachine(t, "fp-drift-own", "独立机")
	set, err := r.sets.Create("另一套", "")
	require.NoError(t, err)
	_, err = r.sets.SetDraftFile(set.Id, "b", []byte("基线\n"), 0o644, nil)
	require.NoError(t, err)
	_, err = r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(machineID, set.Id, configsets.ModeApply)
	require.NoError(t, err)
	return machineID, set.Id
}

func (r *rig) report(t *testing.T, items ...protocol.DriftItem) {
	t.Helper()
	r.reportAs(t, r.machineID, items...)
}

func (r *rig) reportAs(t *testing.T, machineID string, items ...protocol.DriftItem) {
	t.Helper()
	require.NoError(t, r.svc.HandleReport(machineID, protocol.DriftReport{
		Items: items, Final: true,
	}))
}

func (r *rig) drifts(t *testing.T) []*core.Record {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("drift_events",
		"machine = {:m}", "path", 0, 0, map[string]any{"m": r.machineID})
	require.NoError(t, err)
	return recs
}

// openDrifts 返回全部 open 漂移（跨机器），供冲突与多选测试使用。
func (r *rig) openDrifts(t *testing.T) []*core.Record {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("drift_events",
		"state = 'open'", "path", 0, 0, nil)
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
	return r.createMachine(t, "fp-drift-other", "")
}

func (r *rig) requireEvent(t *testing.T, kind string) {
	t.Helper()
	recs, err := r.app.FindRecordsByFilter("events",
		"kind = {:k} && machine = {:m}", "", 1, 0,
		map[string]any{"k": kind, "m": r.machineID})
	require.NoError(t, err)
	require.NotEmpty(t, recs, "应有事件 %s", kind)
}
