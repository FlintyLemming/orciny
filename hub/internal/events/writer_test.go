package events_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestWriteCreatesEventRecord(t *testing.T) {
	app := newApp(t)
	w := events.NewWriter(app)

	require.NoError(t, w.Write(events.KindTokenIssued, "", map[string]any{"prefix": "abcd1234"}))

	recs, err := app.FindRecordsByFilter("events", "kind = 'token.issued'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Empty(t, recs[0].GetString("machine"))
	require.Equal(t, "abcd1234", detailOf(t, recs[0])["prefix"])
}

func TestWriteLinksMachine(t *testing.T) {
	app := newApp(t)
	m := newMachine(t, app, "fp-1")

	require.NoError(t, events.NewWriter(app).Write(events.KindMachineEnrolled, m.Id, nil))

	recs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, m.Id, recs[0].GetString("machine"))
}

func TestWriteTxUsesGivenApp(t *testing.T) {
	app := newApp(t)
	w := events.NewWriter(app)

	// 事务回滚时事件必须一并消失，否则会留下「记录说建过、实际没建」的假事件。
	err := app.RunInTransaction(func(tx core.App) error {
		if err := w.WriteTx(tx, events.KindMachineEnrolled, "", nil); err != nil {
			return err
		}
		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	recs, err := app.FindRecordsByFilter("events", "kind = 'machine.enrolled'", "", 0, 0)
	require.NoError(t, err)
	require.Empty(t, recs)
}

func TestEventDetailSurvivesRoundTrip(t *testing.T) {
	app := newApp(t)
	require.NoError(t, events.NewWriter(app).Write(events.KindAuthFailed, "", map[string]any{
		"reason":      "签名不匹配",
		"fingerprint": "abc",
	}))

	recs, err := app.FindRecordsByFilter("events", "kind = 'auth.failed'", "", 0, 0)
	require.NoError(t, err)
	require.Len(t, recs, 1)
	d := detailOf(t, recs[0])
	require.Equal(t, "签名不匹配", d["reason"])
	require.Equal(t, "abc", d["fingerprint"])
}

// M1 的事件 kind 必须集中定义在 events 包，调用处不许写字面量。
func TestM1EventKinds(t *testing.T) {
	require.Equal(t, "configset.published", events.KindConfigSetPublished)
	require.Equal(t, "configset.rolled_back", events.KindConfigSetRolledBack)
	require.Equal(t, "assign.changed", events.KindAssignChanged)
	require.Equal(t, "apply.ok", events.KindApplyOK)
	require.Equal(t, "apply.failed", events.KindApplyFailed)
	require.Equal(t, "apply.rollback_failed", events.KindApplyRollbackFailed)
	require.Equal(t, "drift.reported", events.KindDriftReported)
	require.Equal(t, "drift.adopted", events.KindDriftAdopted)
	require.Equal(t, "drift.restored", events.KindDriftRestored)
	require.Equal(t, "drift.ignored", events.KindDriftIgnored)
	require.Equal(t, "drift.superseded", events.KindDriftSuperseded)
	require.Equal(t, "credential.created", events.KindCredentialCreated)
	require.Equal(t, "credential.rotated", events.KindCredentialRotated)
	require.Equal(t, "credential.deleted", events.KindCredentialDeleted)
	require.Equal(t, "import.completed", events.KindImportCompleted)
}

// detailOf 解出 detail 字段。JSON 字段没有点号取值的 getter，
// 只能整段反序列化——顺带也验证了它确实是合法 JSON。
func detailOf(t *testing.T, r *core.Record) map[string]any {
	t.Helper()
	var d map[string]any
	require.NoError(t, r.UnmarshalJSONField("detail", &d))
	return d
}

var errRollback = errRollbackType{}

type errRollbackType struct{}

func (errRollbackType) Error() string { return "回滚" }

func newMachine(t *testing.T, app core.App, fp string) *core.Record {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("machines")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("fingerprint", fp)
	r.Set("pub_key", "k")
	r.Set("status", "offline")
	require.NoError(t, app.Save(r))
	return r
}
