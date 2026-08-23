package providers_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/secretbox"

	_ "github.com/FlintyLemming/orciny/hub/internal/migrations"
)

func newTestApp(t *testing.T) (*tests.TestApp, []byte) {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(app.Cleanup)
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	return app, key
}

func newStore(t *testing.T) (*tests.TestApp, *providers.Store, []byte) {
	t.Helper()
	app, key := newTestApp(t)
	return app, providers.NewStore(app, key, events.NewWriter(app)), key
}

func strp(s string) *string { return &s }

// minimalInput 是一条只配了 claude 端点、带平台级 key 的输入。
func minimalInput(name string) providers.Input {
	return providers.Input{
		Name: name,
		Key:  strp("sk-zhipu-abcdef123456"),
		Claude: providers.EndpointInput{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: providers.AuthToken,
			Models:    []string{"glm-5.2", "glm-4.7"},
		},
	}
}

// zhipuInput 从预设带出两组端点，claude 的 base_url 故意带尾斜杠——
// 落库时要被归一化掉（M1.5 spec §6.2）。
func zhipuInput() providers.Input {
	p, _ := providers.PresetByID("zhipu")
	return providers.Input{
		Name:   "智谱 GLM · 个人",
		Preset: p.ID,
		Key:    strp("sk-zhipu-abcdef123456"),
		Claude: providers.EndpointInput{
			BaseURL:   p.Claude.BaseURL + "/",
			AuthField: p.Claude.AuthField,
			Models:    p.Claude.Models,
			Defaults:  p.Claude.Defaults,
		},
	}
}

func TestCreateNormalizesBaseURL(t *testing.T) {
	_, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
	require.NoError(t, err)

	cl := providers.ClaudeOf(rec)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", cl.BaseURL)
	require.NotEmpty(t, cl.Models)
	require.True(t, cl.Defaults.Full())
}

func TestCreateRejectsBadAuthField(t *testing.T) {
	_, s, _ := newStore(t)
	in := zhipuInput()
	in.Claude.AuthField = "ANTHROPIC_SECRET"
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadAuthField)
}

func TestCreateRejectsBadBaseURL(t *testing.T) {
	_, s, _ := newStore(t)
	in := zhipuInput()
	in.Claude.BaseURL = "not-a-url"
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrBadBaseURL)
}

func TestUpdateChangesBaseURLAndModels(t *testing.T) {
	_, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
	require.NoError(t, err)

	in := zhipuInput()
	in.Key = nil // 不修改 key
	in.Claude.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	in.Claude.Models = append(in.Claude.Models, "glm-experimental")
	got, err := s.Update(rec.Id, in)
	require.NoError(t, err)

	cl := providers.ClaudeOf(got)
	require.Equal(t, "https://open.bigmodel.cn/api/coding/paas/v4", cl.BaseURL)
	require.Contains(t, cl.Models, "glm-experimental")
}

func TestCreateStoresBothEndpoints(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.OpenAI = providers.EndpointInput{
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
		AuthField:    providers.DefaultOpenAIAuthField,
		Models:       []string{"glm-5.2"},
		DefaultModel: "glm-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	cl := providers.ClaudeOf(r)
	require.True(t, cl.Configured())
	require.Equal(t, "3456", cl.KeyLast4, "末四位取实际生效的那把 key")

	oa := providers.OpenAIOf(r)
	require.True(t, oa.Configured())
	require.Equal(t, "glm-5.2", oa.DefaultModel)
}

// 只配一个端点是常态，另一个必须是「未配置」而不是校验失败。
func TestCreateWithOnlyClaudeEndpoint(t *testing.T) {
	_, s, _ := newStore(t)
	r, err := s.Create(minimalInput("只有 claude"))
	require.NoError(t, err)
	require.True(t, providers.ClaudeOf(r).Configured())
	require.False(t, providers.OpenAIOf(r).Configured())
}

// 两级 key：端点级非空则覆盖平台级（spec §2.3）。
func TestEndpointKeyOverridesPlatformKey(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("两把 key 的中转")
	in.Claude.Key = strp("sk-claude-side-000111")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://relay.example/v1",
		AuthField: providers.DefaultOpenAIAuthField,
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-side-000111", got, "端点级覆盖平台级")

	got, err = s.Key(r, providers.EndpointOpenAI)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got, "没设端点级就回落平台级")

	require.Equal(t, "0111", providers.ClaudeOf(r).KeyLast4)
	require.Equal(t, "3456", providers.OpenAIOf(r).KeyLast4)
}

// 「配置了 base_url 的端点必须能解出一把 key」，与四槽约束同级（spec §2.3）。
func TestCreateRejectsConfiguredEndpointWithoutAnyKey(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("没 key")
	in.Key = nil
	_, err := s.Create(in)
	require.ErrorIs(t, err, providers.ErrNoKey)
	require.Contains(t, err.Error(), "claude")
}

// 只有端点级 key、没有平台级也合法——中转平台两个口两把 key 的情形。
func TestEndpointOnlyKeyIsEnough(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("只有端点级")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	r, err := s.Create(in)
	require.NoError(t, err)
	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-only-9988", got)
}

func TestKeyRejectsShortValue(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("太短")
	in.Key = strp("1234567") // 7 < MinValueLen
	_, err := s.Create(in)
	require.ErrorIs(t, err, secretbox.ErrShortValue)
}

// Key 的三态（spec §5.2 的「留空则不修改，填写即替换」）。
func TestUpdateKeyTriState(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("三态")
	in.Claude.Key = strp("sk-claude-side-000111")
	r, err := s.Create(in)
	require.NoError(t, err)

	// nil = 不修改
	in.Key, in.Claude.Key = nil, nil
	in.Note = "改了备注"
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err := s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-side-000111", got, "留空不得清掉已有的 key")

	// "" = 清空端点级 → 回落平台级
	in.Claude.Key = strp("")
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err = s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", got)
	require.Equal(t, "3456", providers.ClaudeOf(r).KeyLast4,
		"清空端点级后末四位跟着回落到平台级那把")

	// 非空 = 替换
	in.Claude.Key = strp("sk-claude-new-777777")
	r, err = s.Update(r.Id, in)
	require.NoError(t, err)
	got, err = s.Key(r, providers.EndpointClaude)
	require.NoError(t, err)
	require.Equal(t, "sk-claude-new-777777", got)
}

func TestKeyReportsNoKeyForUnconfiguredSide(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("没平台级 key")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	r, err := s.Create(in)
	require.NoError(t, err)

	_, err = s.Key(r, providers.EndpointOpenAI)
	require.ErrorIs(t, err, providers.ErrNoKey)
}

// 四槽半填仍然拒绝——M1.5 的约束原样继承（spec §7）。
func TestHalfFilledSlotsStillRejected(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("半填")
	in.Claude.Defaults = providers.ModelSlots{Main: "glm-5.2"}
	_, err := s.Create(in)
	require.Error(t, err)
	require.Contains(t, err.Error(), "四个模型槽")
}

// openai 端点的 auth_field 是自由文本，不做二选一校验（spec §2.4）。
func TestOpenAIAuthFieldIsFreeText(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("自定义鉴权字段")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://relay.example/v1",
		AuthField: "X_CUSTOM_TOKEN",
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)
	require.Equal(t, "X_CUSTOM_TOKEN", providers.OpenAIOf(r).AuthField)
}

// openai 端点留空时不填 auth_field 也行，apply 会补上默认值。
func TestOpenAIAuthFieldDefaults(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("默认鉴权字段")
	in.OpenAI = providers.EndpointInput{
		BaseURL: "https://relay.example/v1",
		Models:  []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)
	require.Equal(t, providers.DefaultOpenAIAuthField, providers.OpenAIOf(r).AuthField)
}

// 启动自检：解不开就拒绝启动（spec §2.6）。这条不能丢——它防的是
// 「从备份恢复到新机器时忘了带 secret.key」。
func TestVerifyAllRejectsWrongMasterKey(t *testing.T) {
	app, s, _ := newStore(t)
	_, err := s.Create(minimalInput("智谱 GLM"))
	require.NoError(t, err)
	require.NoError(t, s.VerifyAll(), "同一把钥匙必须通过")

	other, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	bad := providers.NewStore(app, other, events.NewWriter(app))

	err = bad.VerifyAll()
	require.Error(t, err)
	require.Contains(t, err.Error(), "智谱 GLM")
	require.Contains(t, err.Error(), secretbox.KeyFileName,
		"错误信息里要有备份提示，指名 secret.key")
	require.Contains(t, err.Error(), secretbox.EnvKeyName)
}

func TestVerifyAllNamesTheFailingEndpoint(t *testing.T) {
	app, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.Key = nil
	in.Claude.Key = strp("sk-claude-only-9988")
	_, err := s.Create(in)
	require.NoError(t, err)

	other, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	err = providers.NewStore(app, other, events.NewWriter(app)).VerifyAll()
	require.Error(t, err)
	require.Contains(t, err.Error(), "claude 端点")
}

func TestVerifyAllPassesOnEmptyTable(t *testing.T) {
	_, s, _ := newStore(t)
	require.NoError(t, s.VerifyAll())
}

// 精确匹配优先于 host 匹配：同一平台的 /api/anthropic 与 /api/coding
// 是不同产品线（M1.5 spec §6.4）。
func TestMatchBaseURLPrefersExactProvider(t *testing.T) {
	_, s, _ := newStore(t)

	coding := zhipuInput()
	coding.Name = "智谱 · Coding Plan"
	coding.Claude.BaseURL = "https://open.bigmodel.cn/api/coding/paas/v4"
	codingRec, err := s.Create(coding)
	require.NoError(t, err)

	anthropic := zhipuInput()
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
	_, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
	require.NoError(t, err)

	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/some-other-path")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.False(t, m.Exact)
	require.Equal(t, rec.Id, m.ProviderID)
}

func TestMatchBaseURLScansClaudeEndpointOnly(t *testing.T) {
	_, s, _ := newStore(t)
	in := minimalInput("智谱 GLM")
	in.OpenAI = providers.EndpointInput{
		BaseURL:   "https://openai-only.example/v1",
		AuthField: providers.DefaultOpenAIAuthField,
		Models:    []string{"gpt-5.2"}, DefaultModel: "gpt-5.2",
	}
	r, err := s.Create(in)
	require.NoError(t, err)

	// claude 端点：精确命中
	m, err := s.MatchBaseURL("https://open.bigmodel.cn/api/anthropic/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchProvider, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, r.Id, m.ProviderID)

	// openai 端点的地址**不参与**反查
	m, err = s.MatchBaseURL("https://openai-only.example/v1")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
}

// 库里没有但内置预设里有 → 第二档。
func TestMatchBaseURLFallsBackToPresetClaudeEndpoint(t *testing.T) {
	_, s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://api.moonshot.cn/anthropic/")
	require.NoError(t, err)
	require.Equal(t, providers.MatchPreset, m.Kind)
	require.True(t, m.Exact)
	require.Equal(t, "kimi", m.PresetID)
}

func TestMatchBaseURLNoMatch(t *testing.T) {
	_, s, _ := newStore(t)
	m, err := s.MatchBaseURL("https://某个没人听说过的中转.test/v1")
	require.NoError(t, err)
	require.Equal(t, providers.MatchNone, m.Kind)
	require.Empty(t, m.ProviderID)
	require.Empty(t, m.PresetID)
}

func TestDeleteRefusesWhenBound(t *testing.T) {
	app, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
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
	app, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
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
	_, s, _ := newStore(t)
	rec, err := s.Create(zhipuInput())
	require.NoError(t, err)
	require.NoError(t, s.Delete(rec.Id))

	_, err = s.Get(rec.Id)
	require.ErrorIs(t, err, providers.ErrNotFound)
}
