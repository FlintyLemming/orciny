package configsets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestValidateAcceptsCleanDraft(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{cred.k}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{"cred.k": true})
	require.NoError(t, err)
	require.Empty(t, problems)
}

func TestValidateReportsUndefinedRef(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/settings.json",
		[]byte(`{"env":{"K":"{{cred.gone}}","W":"{{var.ws}}"}}`), 0o600, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, map[string]bool{"var.ws": true})
	require.NoError(t, err)
	require.Len(t, problems, 1)
	require.Equal(t, "undefined_ref", problems[0].Kind)
	require.Contains(t, problems[0].Detail, "cred.gone")
}

// machine.* 是内置值，永远算已定义。
func TestValidateAcceptsMachineRefsWithoutKnownSet(t *testing.T) {
	_, s := newService(t)
	set, err := s.Create("s", "")
	require.NoError(t, err)
	_, err = s.SetDraftFile(set.Id, ".claude/CLAUDE.md",
		[]byte("本机是 {{machine.hostname}}"), 0o644, nil)
	require.NoError(t, err)

	problems, err := s.Validate(set.Id, nil)
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
	problems, err := s.Validate(set.Id, nil)
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
	problems, err := s.Validate(set.Id, nil)
	require.NoError(t, err)
	require.Len(t, problems, 2)
	for _, p := range problems {
		require.Equal(t, "always_excluded", p.Kind)
	}
}

const settingsWithToken = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
	`"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"}}`

const settingsWithAPIKey = `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
	`"ANTHROPIC_API_KEY":"{{provider.auth_token}}"}}`

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

	problems, err := s.Validate(set.Id, map[string]bool{})
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

	problems, err := s.Validate(set.Id, map[string]bool{})
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

	problems, err := s.Validate(set.Id, map[string]bool{})
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

	problems, err := s.Validate(set.Id, map[string]bool{})
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
	require.Contains(t, string(content), `"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}"`)
	require.NotContains(t, string(content), "ANTHROPIC_API_KEY")
	// base_url 那行不能被动到——修复只改一个键名。
	require.Contains(t, string(content), `"ANTHROPIC_BASE_URL":"{{provider.base_url}}"`)

	problems, err := s.Validate(set.Id, map[string]bool{})
	require.NoError(t, err)
	for _, p := range problems {
		require.NotEqual(t, configsets.ProblemAuthFieldMismatch, p.Kind)
	}
}
