package drift_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/drift"
	"github.com/FlintyLemming/orciny/protocol"
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

// ---------- 落库标记 ----------

// assignWith 建一个含给定文件的配置集、发布并指派给 rig 的机器。
// 与 rig.assign 同形，只是文件由调用方给。
func (r *rig) assignWith(t *testing.T, files map[string]string) string {
	t.Helper()
	set, err := r.sets.Create("绑定配置", "")
	require.NoError(t, err)
	for path, content := range files {
		_, err = r.sets.SetDraftFile(set.Id, path, []byte(content), 0o600, nil)
		require.NoError(t, err)
	}
	rev, err := r.revs.Publish(set.Id, "v1", "publish")
	require.NoError(t, err)
	_, err = r.sets.Assign(r.machineID, set.Id, configsets.ModeApply)
	require.NoError(t, err)
	r.setID = set.Id
	r.revID = rev.Id
	return set.Id
}

func (r *rig) driftAt(t *testing.T, path string) *core.Record {
	t.Helper()
	rec, ok := r.driftsByPath(t)[path]
	require.True(t, ok, "没有 %s 的漂移", path)
	return rec
}

func TestHandleReportMarksBindingDrift(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Content: []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic"}}`),
		Mode:    0o600,
	})

	rec := r.driftAt(t, ".claude/settings.json")
	require.True(t, rec.GetBool("binding_drift"))
	require.Equal(t, "https://api.moonshot.cn/anthropic", rec.GetString("binding_url"))
}

// 普通漂移不该被误标——误标会把「收编」置灰，用户会以为工具坏了。
func TestHandleReportLeavesNormalDriftUnmarked(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{"CLAUDE.md": "原文"})

	r.report(t, protocol.DriftItem{
		Path: "CLAUDE.md", Kind: protocol.DriftModified,
		Content: []byte("改过的原文"), Mode: 0o644,
	})

	rec := r.driftAt(t, "CLAUDE.md")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 用户又把那一行改回来了 → 重复上报时标记要被清掉，不能粘住。
func TestHandleReportClearsBindingDriftWhenReverted(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"1"}}`,
	})

	report := func(content string) {
		r.report(t, protocol.DriftItem{
			Path: ".claude/settings.json", Kind: protocol.DriftModified,
			Content: []byte(content), Mode: 0o600,
		})
	}
	report(`{"env":{"ANTHROPIC_BASE_URL":"https://api.moonshot.cn/anthropic","X":"1"}}`)
	require.True(t, r.driftAt(t, ".claude/settings.json").GetBool("binding_drift"))

	report(`{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}","X":"2"}}`)
	rec := r.driftAt(t, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}

// 内容没上来（Truncated）时无从判断，一律不标。
func TestHandleReportDoesNotMarkTruncated(t *testing.T) {
	r := newRig(t)
	r.assignWith(t, map[string]string{
		".claude/settings.json": `{"env":{"ANTHROPIC_BASE_URL":"{{provider.base_url}}"}}`,
	})

	r.report(t, protocol.DriftItem{
		Path: ".claude/settings.json", Kind: protocol.DriftModified,
		Truncated: true, Mode: 0o600, // Content 故意为空
	})

	rec := r.driftAt(t, ".claude/settings.json")
	require.False(t, rec.GetBool("binding_drift"))
	require.Empty(t, rec.GetString("binding_url"))
}
