package configsets_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestValidateAcceptsCleanDraft(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	seedVariable(t, app, "k", "v")
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{var.k}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestValidateReportsUndefinedRef(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	seedVariable(t, app, "ws", "main")
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{var.gone}}","W":"{{var.ws}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "undefined_ref", problems[0].Kind)
	require.Contains(t, problems[0].Detail, "var.gone")
}

// machine.* 是内置值，永远算已定义。
func TestValidateAcceptsMachineRefsWithoutKnownSet(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md",
		[]byte("本机是 {{machine.hostname}}"), 0o644, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestValidateReportsOversizeEntry(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	// 绕过 SetDraftFile 的前置检查，直接塞一条超限清单项，
	// 模拟「导入采集时漏判」这类历史数据。
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{
		{Path: ".claude/big", Hash: "aa", Size: protocol.MaxFileSize + 1, Mode: 0o644},
	}))
	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "too_large", problems[0].Kind)
}

func TestValidateReportsAlwaysExcluded(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraft(set.Id, []protocol.FileEntry{
		{Path: ".claude/.credentials.json", Hash: "aa", Size: 10, Mode: 0o600},
		{Path: ".claude/projects/x.jsonl", Hash: "bb", Size: 10, Mode: 0o644},
	}))
	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	for _, p := range problems {
		require.Equal(t, "always_excluded", p.Kind)
	}
}

const settingsWithToken = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
	`"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"}}`

const settingsWithAPIKey = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",` +
	`"ANTHROPIC_API_KEY":"{{provider.claude.auth_token}}"}}`

// findProblem 返回第一条指定类别的校验结果，没有则返回 nil。
func findProblem(ps []configsets.Problem, kind string) *configsets.Problem {
	for i := range ps {
		if ps[i].Kind == kind {
			return &ps[i]
		}
	}
	return nil
}

func TestValidateBindingMissingIsError(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithToken), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	hit := findProblem(problems, configsets.ProblemBindingMissing)
	require.NotNil(t, hit, "必须报 binding_missing")
	require.False(t, hit.Warning, "这是错误，不是警告")
	require.Equal(t, configsets.SettingsPath, hit.Path)
}

func TestValidateBindingUnusedIsWarning(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, "CLAUDE.md", []byte("没有任何占位符"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	hit := findProblem(problems, configsets.ProblemBindingUnused)
	require.NotNil(t, hit)
	require.True(t, hit.Warning, "绑了但没用不该阻断发布")
}

func TestValidateAuthFieldMismatchGivesFix(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	// Provider 用 AUTH_TOKEN，文件里写的是 API_KEY。
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithAPIKey), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	hit := findProblem(problems, configsets.ProblemAuthFieldMismatch)
	require.NotNil(t, hit)
	require.False(t, hit.Warning)
	require.NotNil(t, hit.Fix)
	require.Equal(t, configsets.FixReplaceEnvKey, hit.Fix.Kind)
	require.Equal(t, "ANTHROPIC_API_KEY", hit.Fix.From)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", hit.Fix.To)
}

func TestValidateAuthFieldMatchIsQuiet(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithToken), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
		require.NotEqual(t, configsets.ProblemBindingMissing, p.Kind)
		require.NotEqual(t, configsets.ProblemBindingUnused, p.Kind)
	}
}

func TestFixAuthFieldRewritesDraft(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id,
		&providers.Binding{Provider: seedProvider(t, app, "智谱 GLM · 个人")}))
	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath,
		[]byte(settingsWithAPIKey), 0o600, nil)
	require.NoError(t, err)

	require.NoError(t, s.FixAuthField(set.Id))

	draft, err := s.Draft(set.Id)
	require.NoError(t, err)
	var content []byte
	for _, f := range draft {
		if f.Path == configsets.SettingsPath {
			content, err = blobs.New(app).Get(f.Hash)
			require.NoError(t, err)
		}
	}
	require.Contains(t, string(content), `"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"`)
	require.NotContains(t, string(content), "ANTHROPIC_API_KEY")
	// base_url 那行不能被动到——修复只改一个键名。
	require.Contains(t, string(content), `"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}"`)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
	}
}

// known 参数删掉之后，机器变量的「已定义」由 Service 自己查 variables 表。
// 未定义的 {{var.*}} 仍然要报——ProblemUndefinedRef 保留，只是现在只服务于它。
func TestValidateStillReportsUndefinedVar(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md",
		[]byte("工作区：{{var.workspace}}\n"), 0o644, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, configsets.ProblemUndefinedRef, problems[0].Kind)
	require.Contains(t, problems[0].Detail, "var.workspace")

	// 给某台机器定义了这个变量之后就不再报。
	seedVariable(t, app, "workspace", "main")
	problems, err = s.Validate(set.Id)
	require.NoError(t, err)
	require.Empty(t, problems)
}

// {{cred.*}} 现在是语法错误，由 protocol 的词法在 Refs 阶段就拒掉。
func TestValidateReportsCredAsSyntaxError(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	plantDraft(t, app, set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{cred.zhipu}}"}}`), 0o600)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, configsets.ProblemUndefinedRef, problems[0].Kind)
	require.Contains(t, problems[0].Detail, "未知前缀")
}

func TestRefsFieldHasNoCreds(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	seedVariable(t, app, "workspace", "main")
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json", []byte(`{"env":{
		"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
		"X":"{{var.workspace}}"
	}}`), 0o600, nil)
	require.NoError(t, err)

	refs, err := s.DraftRefs(set.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"workspace"}, refs.Vars)
	require.Equal(t, []string{"claude.base_url"}, refs.ProviderKeys)
}

// 旧的字面量不再被认作「承载 key 的那一行」——它现在压根过不了词法。
func TestAuthFieldMismatchIgnoresOldUnqualifiedToken(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人")

	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))
	plantDraft(t, app, set.Id, configsets.SettingsPath,
		[]byte(`{"env":{"ANTHROPIC_API_KEY":"{{provider.auth_token}}"}}`), 0o600)

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	// 报的是语法错误，不是 auth_field_mismatch
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
	}
}

// 引用了 openai 端点，但绑定的 provider 只配了 claude → 阻断级错误。
func TestValidateEndpointMissingIsBlocking(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人") // 只配 claude

	_, err = s.SetDraftFile(set.Id, configsets.SettingsPath, []byte(`{"env":{
		"ANTHROPIC_BASE_URL":"{{provider.claude.base_url}}",
		"ANTHROPIC_AUTH_TOKEN":"{{provider.claude.auth_token}}"
	}}`), 0o600, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".codex/config.toml",
		[]byte("base_url = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	p := findProblem(problems, configsets.ProblemEndpointMissing)
	require.NotNil(t, p, "必须报 endpoint_missing")
	require.False(t, p.Warning, "阻断级，不是警告")
	require.Equal(t, ".codex/config.toml", p.Path)
	require.Contains(t, p.Detail, "OpenAI 端点")
}

// 两个端点都配了就不报。
func TestValidateNoEndpointMissingWhenBothConfigured(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProviderWith(t, app, "智谱 GLM · 个人",
		"https://open.bigmodel.cn/api/paas/v4")

	_, err = s.SetDraftFile(set.Id, ".codex/config.toml",
		[]byte("base_url = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemEndpointMissing, p.Kind)
	}
}

// 每个缺失的端点只报一条，路径取字典序第一个引用它的文件——
// 一个配置集里同一个端点被十个文件引用时，报十条只是噪音。
func TestEndpointMissingIsReportedOncePerEndpoint(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	provID := seedProvider(t, app, "智谱 GLM · 个人")

	_, err = s.SetDraftFile(set.Id, ".codex/a.toml",
		[]byte("u = \"{{provider.openai.base_url}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".codex/b.toml",
		[]byte("k = \"{{provider.openai.api_key}}\"\n"), 0o644, nil)
	require.NoError(t, err)
	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{Provider: provID}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)
	n := 0
	for _, p := range problems {
		if p.Kind == configsets.ProblemEndpointMissing {
			n++
			require.Equal(t, ".codex/a.toml", p.Path)
		}
	}
	require.Equal(t, 1, n)
}

// plantDraft 直接写 config_sets.draft，绕过 SetDraft 的引用提取。
//
// 存量库里就有这样的记录：blob 里躺着 M1.6 之前写下的 {{cred.*}} 与
// {{provider.base_url}}，它们**不清洗**（spec §6.2），下次发布时被词法拒掉
// 才是预期行为。要测「拒掉」这件事，就得先能把它们塞进去。
func plantDraft(t *testing.T, app core.App, setID, path string, body []byte, mode uint32) {
	t.Helper()
	hash, err := blobs.New(app).Put(body)
	require.NoError(t, err)
	rec, err := app.FindRecordById("config_sets", setID)
	require.NoError(t, err)
	rec.Set("draft", []protocol.FileEntry{{
		Path: path, Hash: hash, Size: uint32(len(body)), Mode: mode,
	}})
	require.NoError(t, app.Save(rec))
}
