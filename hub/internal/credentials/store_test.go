package credentials_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
)

func newStore(t *testing.T) (*tests.TestApp, *credentials.Store) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)

	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	return app, credentials.NewStore(app, key, events.NewWriter(app))
}

func TestCreateStoresCipherAndLast4(t *testing.T) {
	_, s := newStore(t)
	r, err := s.Create("anthropic_key", "sk-ant-abcdefgh1234", "主力")
	require.NoError(t, err)
	require.Equal(t, "1234", r.GetString("last4"))
	require.NotContains(t, r.GetString("cipher_value"), "sk-ant", "库里不能出现明文")

	v, err := s.Value("anthropic_key")
	require.NoError(t, err)
	require.Equal(t, "sk-ant-abcdefgh1234", v)
}

func TestCreateRejectsShortValueAndBadName(t *testing.T) {
	_, s := newStore(t)
	_, err := s.Create("k", "short7x", "")
	require.ErrorIs(t, err, credentials.ErrShortValue)

	_, err = s.Create("bad name", "longenough123", "")
	require.ErrorIs(t, err, credentials.ErrBadName)
}

func TestRotateChangesValueOnly(t *testing.T) {
	_, s := newStore(t)
	r, err := s.Create("k", "sk-oldvalue-1111", "note")
	require.NoError(t, err)

	require.NoError(t, s.Rotate("k", "sk-newvalue-2222"))

	v, err := s.Value("k")
	require.NoError(t, err)
	require.Equal(t, "sk-newvalue-2222", v)

	got, err := s.Values([]string{"k"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"k": "sk-newvalue-2222"}, got)
	require.Equal(t, "k", r.GetString("name"))
}

func TestValuesSkipsUnknownNames(t *testing.T) {
	_, s := newStore(t)
	_, err := s.Create("known", "sk-known-9999", "")
	require.NoError(t, err)

	got, err := s.Values([]string{"known", "gone"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"known": "sk-known-9999"}, got)
}

// 被 head revision 或 draft 引用的凭据不许删（spec §6.5）。
func TestDeleteIsBlockedWhileReferenced(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("in_use", "sk-inuse-7777", "")
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s")
	set.Set("draft_refs", map[string]any{"creds": []string{"in_use"}, "vars": []string{}})
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete("in_use"), credentials.ErrInUse)

	setIDs, _, _, err := s.ReferencedBy("in_use")
	require.NoError(t, err)
	require.Equal(t, []string{set.Id}, setIDs)

	// 草稿不再引用之后就可以删
	set.Set("draft_refs", map[string]any{"creds": []string{}, "vars": []string{}})
	require.NoError(t, app.Save(set))
	require.NoError(t, s.Delete("in_use"))
}

// 历史版本的引用只警告不阻止：历史不可变，但也不会再被下发。
func TestDeleteAllowedWhenOnlyOldRevisionReferences(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("old_only", "sk-oldonly-5555", "")
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "s")
	require.NoError(t, app.Save(set))

	revs, err := app.FindCollectionByNameOrId("revisions")
	require.NoError(t, err)
	old := core.NewRecord(revs)
	old.Set("config_set", set.Id)
	old.Set("seq", 1)
	old.Set("refs", map[string]any{"creds": []string{"old_only"}, "vars": []string{}})
	old.Set("source", "publish")
	require.NoError(t, app.Save(old))

	head := core.NewRecord(revs)
	head.Set("config_set", set.Id)
	head.Set("seq", 2)
	head.Set("refs", map[string]any{"creds": []string{}, "vars": []string{}})
	head.Set("source", "publish")
	require.NoError(t, app.Save(head))

	set.Set("head", head.Id)
	require.NoError(t, app.Save(set))

	_, revIDs, _, err := s.ReferencedBy("old_only")
	require.NoError(t, err)
	require.Equal(t, []string{old.Id}, revIDs, "历史引用要报出来")
	require.NoError(t, s.Delete("old_only"), "但不阻止删除")
}

func TestVerifyAllFailsOnWrongMasterKey(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("k", "sk-value-3333", "")
	require.NoError(t, err)
	require.NoError(t, s.VerifyAll())

	other, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	bad := credentials.NewStore(app, other, events.NewWriter(app))
	require.Error(t, bad.VerifyAll(), "换了主密钥必须报错，不能静默把凭据当损坏数据")
}

func TestEventsAreWritten(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("k", "sk-value-4444", "")
	require.NoError(t, err)
	require.NoError(t, s.Rotate("k", "sk-value-5555"))
	require.NoError(t, s.Delete("k"))

	recs, err := app.FindAllRecords("events")
	require.NoError(t, err)
	var kinds []string
	for _, r := range recs {
		kinds = append(kinds, r.GetString("kind"))
	}
	require.Subset(t, kinds, []string{
		events.KindCredentialCreated, events.KindCredentialRotated, events.KindCredentialDeleted,
	})
}

// 被 Provider 引用的凭据不可删除（spec §5.3）。
// 不拦住的话：删凭据 → 下次重注入 → 全机队拿到空值 → Claude Code 全体 401。
func TestDeleteRefusesWhenReferencedByProvider(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)

	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", "智谱 GLM · 个人")
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))

	err = s.Delete("zhipu_key")
	require.ErrorIs(t, err, credentials.ErrInUse)
	require.Contains(t, err.Error(), "服务配置", "错误信息要说清是被谁引用的")
}

func TestReferencedByReportsProviders(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)
	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)
	p := core.NewRecord(c)
	p.Set("name", "智谱 GLM · 个人")
	p.Set("base_url", "https://open.bigmodel.cn/api/anthropic")
	p.Set("auth_field", "ANTHROPIC_AUTH_TOKEN")
	p.Set("credential", cred.Id)
	require.NoError(t, app.Save(p))

	setIDs, revIDs, providerIDs, err := s.ReferencedBy("zhipu_key")
	require.NoError(t, err)
	require.Empty(t, setIDs)
	require.Empty(t, revIDs)
	require.Equal(t, []string{p.Id}, providerIDs)
}

func TestValueByID(t *testing.T) {
	app, s := newStore(t)
	_, err := s.Create("zhipu_key", "sk-zhipu-abcdefghij", "")
	require.NoError(t, err)
	cred, err := app.FindFirstRecordByData("credentials", "name", "zhipu_key")
	require.NoError(t, err)

	v, err := s.ValueByID(cred.Id)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdefghij", v)

	_, err = s.ValueByID("不存在的 id")
	require.ErrorIs(t, err, credentials.ErrNotFound)
}
