package importer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/importer"
)

func rules(fs []importer.Finding) map[string]importer.Finding {
	out := map[string]importer.Finding{}
	for _, f := range fs {
		out[f.Location] = f
	}
	return out
}

func TestScanStructuredEnvInSettings(t *testing.T) {
	got := importer.Scan(".claude/settings.json", []byte(`{
		"model": "opus",
		"env": {
			"ANTHROPIC_AUTH_TOKEN": "sk-ant-abcdefghijklmnop",
			"HTTP_PROXY": "http://127.0.0.1:7890"
		}
	}`))

	byLoc := rules(got)
	f, ok := byLoc["env.ANTHROPIC_AUTH_TOKEN"]
	require.True(t, ok, "settings.json 的 env 是最高优先级的结构化位置")
	require.Equal(t, "structured", f.Rule)
	require.Equal(t, "ANTHROPIC_AUTH_TOKEN", f.Key)
	require.Equal(t, "anthropic_auth_token", f.Suggested)
	require.Equal(t, "sk-a…mnop", f.Masked)

	// env 里的每一项都要报出来，哪怕看起来不像密钥——误报可接受，漏报不可
	require.Contains(t, byLoc, "env.HTTP_PROXY")
}

func TestScanMcpServersEnv(t *testing.T) {
	got := importer.Scan(".claude.json", []byte(`{
		"mcpServers": {
			"github": {
				"command": "npx",
				"env": {"GITHUB_TOKEN": "ghp_abcdefghijklmnopqrst"}
			}
		}
	}`))
	byLoc := rules(got)
	f, ok := byLoc["mcpServers.github.env.GITHUB_TOKEN"]
	require.True(t, ok)
	require.Equal(t, "structured", f.Rule)
	require.Equal(t, "github_token", f.Suggested)
}

func TestScanKeyNamePattern(t *testing.T) {
	got := importer.Scan(".claude/settings.json", []byte(`{
		"someApiKey": "短的也要报",
		"harmless": "普通值"
	}`))
	byLoc := rules(got)
	require.Contains(t, byLoc, "someApiKey")
	require.Equal(t, "key_name", byLoc["someApiKey"].Rule)
	require.NotContains(t, byLoc, "harmless")
}

func TestScanValuePrefixes(t *testing.T) {
	for _, v := range []string{
		"sk-abcdefghijklmn", "sk-ant-abcdefghijkl", "ghp_abcdefghijklmnop",
		"gho_abcdefghijklmnop", "github_pat_abcdefghij", "xoxb-1234-5678-abcd",
		"AKIAIOSFODNN7EXAMPLE", "AIzaSyA-abcdefghijklmnop", "glpat-abcdefghijklmn",
	} {
		got := importer.Scan(".claude/CLAUDE.md", []byte("我的 key 是 "+v+" 别外传"))
		require.NotEmpty(t, got, "%q 必须被全文兜底抓到", v)
		require.Equal(t, "value_prefix", got[0].Rule)
	}
}

func TestScanHighEntropyValue(t *testing.T) {
	// 长度 ≥ 32 且熵 ≥ 3.5
	got := importer.Scan(".claude/settings.json",
		[]byte(`{"opaque":"aZ9x2Kq7Lm4Pw8Rt5Yv1Bn6Cd3Ef0Gh2Jk"}`))
	require.NotEmpty(t, got)
	require.Equal(t, "value_entropy", got[0].Rule)
}

// 低熵的长串不该报：重复字符、纯路径。
func TestScanIgnoresLowEntropyLongValues(t *testing.T) {
	for _, v := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"00000000000000000000000000000000",
	} {
		got := importer.Scan(".claude/settings.json", []byte(`{"v":"`+v+`"}`))
		require.Empty(t, got, "%q 熵太低，不该报", v)
	}
}

func TestScanFullTextFallback(t *testing.T) {
	got := importer.Scan(".claude/CLAUDE.md",
		[]byte("# 备忘\n\n临时把 key 记在这里：sk-ant-api03-abcdefghij\n"))
	require.Len(t, got, 1)
	require.Equal(t, "value_prefix", got[0].Rule)
	require.Equal(t, ".claude/CLAUDE.md", got[0].Path)
}

func TestMask(t *testing.T) {
	require.Equal(t, "sk-a…mnop", importer.Mask("sk-ant-abcdefghijklmnop"))
	// 太短的值全部遮掉：前 4 后 4 会把它整个露出来
	require.Equal(t, "…", importer.Mask("short"))
	require.Equal(t, "…", importer.Mask(""))
}

func TestSuggestName(t *testing.T) {
	require.Equal(t, "anthropic_auth_token", importer.SuggestName("ANTHROPIC_AUTH_TOKEN"))
	require.Equal(t, "some_api_key", importer.SuggestName("someApiKey"))
	require.Equal(t, "github_token", importer.SuggestName("GITHUB-TOKEN"))
	require.Equal(t, "cred", importer.SuggestName(""), "空名字要有兜底")
}

// 掩码后的字符串里绝不能出现完整值——UI 会直接把它渲染出来。
func TestFindingsNeverCarryFullValue(t *testing.T) {
	secret := "sk-ant-abcdefghijklmnopqrstuvwx"
	got := importer.Scan(".claude/settings.json", []byte(`{"env":{"K":"`+secret+`"}}`))
	require.NotEmpty(t, got)
	for _, f := range got {
		require.NotContains(t, f.Masked, secret)
		require.NotContains(t, f.Location, secret)
		require.NotContains(t, f.Suggested, secret)
	}
}

// 已被抽成占位符的值不该再报敏感项——provider 前缀也一样。
func TestScanIgnoresProviderPlaceholders(t *testing.T) {
	content := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"{{provider.auth_token}}",` +
		`"ANTHROPIC_BASE_URL":"{{provider.base_url}}",` +
		`"ANTHROPIC_MODEL":"{{provider.model}}"}}`)
	require.Empty(t, importer.Scan(".claude/settings.json", content))
}
