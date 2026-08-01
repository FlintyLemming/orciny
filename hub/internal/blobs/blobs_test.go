package blobs_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newApp(t *testing.T) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return app
}

func TestHashIsSHA256Hex(t *testing.T) {
	// 空内容的 sha256，写死期望值：这个口径进了 revision.files，改了等于历史全废。
	require.Equal(t,
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		blobs.Hash(nil))
}

func TestPutIsDeduplicated(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	h1, err := s.Put([]byte("hello"))
	require.NoError(t, err)
	h2, err := s.Put([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, h1, h2)

	recs, err := app.FindAllRecords("blobs")
	require.NoError(t, err)
	require.Len(t, recs, 1, "同内容两次写只该落一份")
}

func TestGetRoundTrips(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	content := []byte("{\n  \"model\": \"opus\"\n}\n")
	h, err := s.Put(content)
	require.NoError(t, err)

	got, err := s.Get(h)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)
	_, err := s.Get("0000000000000000000000000000000000000000000000000000000000000000")
	require.ErrorIs(t, err, blobs.ErrNotFound)
}

func TestHas(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)
	h, err := s.Put([]byte("x"))
	require.NoError(t, err)

	ok, err := s.Has(h)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = s.Has(blobs.Hash([]byte("y")))
	require.NoError(t, err)
	require.False(t, ok)
}

// 并发写同一 hash：唯一索引会让其中一方失败，Put 必须把它当成「已存在」。
func TestConcurrentPutSameHash(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			_, err := s.Put([]byte("同一份内容"))
			errs <- err
		}()
	}
	for range n {
		require.NoError(t, <-errs)
	}
	recs, err := app.FindAllRecords("blobs")
	require.NoError(t, err)
	require.Len(t, recs, 1)
}

func TestGCOrphansOnlyDeletesUnreferenced(t *testing.T) {
	app := newApp(t)
	s := blobs.New(app)

	kept, err := s.Put([]byte("被 revision 引用"))
	require.NoError(t, err)
	inDraft, err := s.Put([]byte("被草稿引用"))
	require.NoError(t, err)
	orphan, err := s.Put([]byte("没人引用"))
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s1")
	set.Set("draft", []map[string]any{{"path": "a", "hash": inDraft, "size": 1, "mode": 420}})
	require.NoError(t, app.Save(set))

	revsC, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	rev := core.NewRecord(revsC)
	rev.Set("config_set", set.Id)
	rev.Set("seq", 1)
	rev.Set("files", []map[string]any{{"path": "b", "hash": kept, "size": 1, "mode": 420}})
	rev.Set("source", "publish")
	require.NoError(t, app.Save(rev))

	n, err := s.GCOrphans(set.Id)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	for _, h := range []string{kept, inDraft} {
		ok, err := s.Has(h)
		require.NoError(t, err)
		require.True(t, ok, "被引用的 blob 不该被删")
	}
	ok, err := s.Has(orphan)
	require.NoError(t, err)
	require.False(t, ok)
}
