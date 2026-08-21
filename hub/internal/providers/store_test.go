package providers_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newStore(t *testing.T) (*providers.Store, *tests.TestApp) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	return providers.NewStore(app, events.NewWriter(app)), app
}

func seedCred(t *testing.T, app core.App, name string) string {
	t.Helper()
	c, err := app.FindCollectionByNameOrId("credentials")
	require.NoError(t, err)
	r := core.NewRecord(c)
	r.Set("name", name)
	r.Set("cipher_value", "x")
	r.Set("last4", "1234")
	require.NoError(t, app.Save(r))
	return r.Id
}

func zhipuInput(credID string) providers.Input {
	p, _ := providers.PresetByID("zhipu")
	return providers.Input{
		Name:       "智谱 GLM · 个人",
		Preset:     p.ID,
		BaseURL:    p.BaseURL + "/", // 故意带尾斜杠，落库时要被归一化掉
		AuthField:  p.AuthField,
		Credential: credID,
		Models:     p.Models,
		Defaults:   p.Defaults,
	}
}

func TestCreateNormalizesBaseURL(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "zhipu_key")))
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", rec.GetString("base_url"))

	var models []string
	require.NoError(t, rec.UnmarshalJSONField("models", &models))
	require.NotEmpty(t, models)

	var defaults providers.ModelSlots
	require.NoError(t, rec.UnmarshalJSONField("defaults", &defaults))
	require.True(t, defaults.Full())
}

func TestCreateRejectsBadAuthField(t *testing.T) {
	s, app := newStore(t)
	in := zhipuInput(seedCred(t, app, "k"))
	in.AuthField = "ANTHROPIC_SECRET"
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadAuthField)
}

func TestCreateRejectsBadBaseURL(t *testing.T) {
	s, app := newStore(t)
	in := zhipuInput(seedCred(t, app, "k"))
	in.BaseURL = "  "
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadBaseURL)
}

func TestUpdateChangesBaseURLAndModels(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	in := zhipuInput(rec.GetString("credential"))
	in.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	in.Models = append(in.Models, "glm-experimental")
	got, err := s.Update(rec.Id, in)
	require.NoError(t, err)
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4", got.GetString("base_url"))

	var models []string
	require.NoError(t, got.UnmarshalJSONField("models", &models))
	require.Contains(t, models, "glm-experimental")
}

func TestUsingCredential(t *testing.T) {
	s, app := newStore(t)
	credID := seedCred(t, app, "shared_key")
	a, err := s.Create(zhipuInput(credID))
	require.NoError(t, err)

	in := zhipuInput(credID)
	in.Name = "智谱 GLM · 团队"
	b, err := s.Create(in)
	require.NoError(t, err)

	ids, err := s.UsingCredential(credID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{a.Id, b.Id}, ids)

	ids, err = s.UsingCredential(seedCred(t, app, "lonely"))
	require.NoError(t, err)
	require.Empty(t, ids)
}

// 精确匹配优先于 host 匹配：同一平台的 /api/anthropic 与 /api/coding
// 是不同产品线（spec §6.4）。
func TestMatchBaseURLPrefersExactProvider(t *testing.T) {
	s, app := newStore(t)
	credID := seedCred(t, app, "k")

	coding := zhipuInput(credID)
	coding.Name = "智谱 · Coding Plan"
	coding.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	codingRec, err := s.Create(coding)
	require.NoError(t, err)

	anthropic := zhipuInput(credID)
	anthropic.Name = "智谱 · Anthropic 兼容"
	anthropicRec, err := s.Create(anthropic)
	require.NoError(t, err)

	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/coding/paas/v4/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, codingRec.Id, m.ProviderID)

	m, err = s.MatchBaseURL("https://open.bigmodel.cn/api/anthropic")
	require.NoError(t, err)
	require.Equal(t, anthropicRec.Id, m.ProviderID)
}

// 路径对不上但 host 对得上 → 模糊命中，Exact=false。
func TestMatchBaseURLFallsBackToHost(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/paas/v4")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.False(t, m.Exact)
	require.Equal(t, rec.Id, m.ProviderID)
}

// 库里没有但内置预设里有 → 第二档。
func TestMatchBaseURLFallsBackToPreset(t *testing.T) {
	s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://api.moonshot.cn/anthropic/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, "kimi", m.PresetID)
}

func TestMatchBaseURLNoMatch(t *testing.T) {
	s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://某个没人听说过的中转.test/v1")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
	require.Empty(t, m.ProviderID)
	require.Empty(t, m.PresetID)
}

func TestDeleteRefusesWhenBound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	// 造一个 head_provider 指向它的配置集。
	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "主力配置")
	set.Set("head_provider", rec.Id)
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete(rec.Id), providers.ErrInUse)
}

func TestDeleteRefusesWhenDraftBound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)

	sets, err := app.FindCollectionByNameOrId("config_sets")
	require.NoError(t, err)
	set := core.NewRecord(sets)
	set.Set("name", "草稿里绑着")
	set.Set("draft_binding", providers.Binding{Provider: rec.Id})
	require.NoError(t, app.Save(set))

	require.ErrorIs(t, s.Delete(rec.Id), providers.ErrInUse)
}

func TestDeleteWhenUnbound(t *testing.T) {
	s, app := newStore(t)
	rec, err := s.Create(zhipuInput(seedCred(t, app, "k")))
	require.NoError(t, err)
	require.NoError(t, s.Delete(rec.Id))

	_, err = s.Get(rec.Id)
	require.ErrorIs(t, err, providers.ErrNotFound)
}
