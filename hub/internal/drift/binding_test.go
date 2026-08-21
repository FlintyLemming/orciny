package drift_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/drift"
)

const bindingBase = `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`

func TestDetectBindingDriftFindsLiteralURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.moonshot.cn/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	url, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://api.moonshot.cn/anthropic", url)
}

// 基线里没有 provider.base_url → 这条漂移与绑定无关。
func TestDetectBindingDriftIgnoresUnboundBaseline(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`
	_, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.False(t, ok)
}

// 那一行没被改（还是占位符），改的是别处 → 不是绑定漂移。
func TestDetectBindingDriftIgnoresOtherChanges(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "{{provider.base_url}}",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}",
    "MY_VAR": "1"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 改成了一段不像 URL 的东西 → 不乱报，走普通漂移。
func TestDetectBindingDriftIgnoresNonURL(t *testing.T) {
	cur := `{
  "env": {
    "ANTHROPIC_BASE_URL": "待填",
    "ANTHROPIC_AUTH_TOKEN": "{{provider.auth_token}}",
    "ANTHROPIC_MODEL": "{{provider.model}}"
  }
}`
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), []byte(cur))
	require.False(t, ok)
}

// 紧凑写法（无缩进、单行）也要认得。
func TestDetectBindingDriftHandlesCompactJSON(t *testing.T) {
	base := `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`
	cur := `{"env":{"ANTHROPIC_BASE_URL":"https://zenmux.ai/api/anthropic"}}`
	url, ok := drift.DetectBindingDrift([]byte(base), []byte(cur))
	require.True(t, ok)
	require.Equal(t, "https://zenmux.ai/api/anthropic", url)
}

func TestDetectBindingDriftHandlesDeletedContent(t *testing.T) {
	_, ok := drift.DetectBindingDrift([]byte(bindingBase), nil)
	require.False(t, ok)
}
