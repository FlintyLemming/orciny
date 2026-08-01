package manifest_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/internal/manifest"
)

func TestDefaultManifestCoversClaudeCode(t *testing.T) {
	m := manifest.Default()
	require.NoError(t, m.Validate())

	var paths []string
	for _, inc := range m.Include {
		paths = append(paths, inc.Path)
	}
	// 路径根是 HOME，不是 ~/.claude —— 因为 ~/.claude.json 在 .claude/ 之外
	// （spec §3.1）。
	require.Equal(t, []string{
		".claude/settings.json",
		".claude/CLAUDE.md",
		".claude/keybindings.json",
		".claude/agents/**",
		".claude/commands/**",
		".claude/skills/**",
		".claude.json",
	}, paths)

	last := m.Include[len(m.Include)-1]
	require.Equal(t, manifest.ModeKeys, last.Mode)
	require.Equal(t, []string{"mcpServers"}, last.Keys)
	require.Equal(t, []string{"**/.DS_Store"}, m.Exclude)
}

func TestParseRoundTrip(t *testing.T) {
	b, err := manifest.Default().JSON()
	require.NoError(t, err)
	got, err := manifest.Parse(b)
	require.NoError(t, err)
	require.Equal(t, manifest.Default(), got)

	// JSON() 必须稳定：同一份 manifest 两次序列化字节相同，
	// 否则冻结进 revision 的字节会无谓地变化。
	b2, err := got.JSON()
	require.NoError(t, err)
	require.Equal(t, b, b2)
}

func TestValidateRejectsBadManifest(t *testing.T) {
	for name, m := range map[string]manifest.Manifest{
		"版本不对":      {Version: 2, Include: []manifest.Include{{Path: "a", Mode: manifest.ModeFile}}},
		"空 include": {Version: 1},
		"未知 mode":   {Version: 1, Include: []manifest.Include{{Path: "a", Mode: "weird"}}},
		"keys 无键":   {Version: 1, Include: []manifest.Include{{Path: "a", Mode: manifest.ModeKeys}}},
		"绝对路径":      {Version: 1, Include: []manifest.Include{{Path: "/etc/passwd", Mode: manifest.ModeFile}}},
		"含 ..":      {Version: 1, Include: []manifest.Include{{Path: "../x", Mode: manifest.ModeFile}}},
		"恒排除路径":     {Version: 1, Include: []manifest.Include{{Path: ".claude/.credentials.json", Mode: manifest.ModeFile}}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, m.Validate())
		})
	}
}

func TestAlwaysExcludedList(t *testing.T) {
	// 这份清单是产品 §4.3 的「恒定不可去除」，改动需要发版。
	require.Equal(t, []string{
		".claude/projects/**",
		".claude/todos/**",
		".claude/shell-snapshots/**",
		".claude/statsig/**",
		".claude/.credentials.json",
		"**/.git/**",
		"**/node_modules/**",
	}, manifest.AlwaysExcluded())
}

func TestIsAlwaysExcluded(t *testing.T) {
	for _, p := range []string{
		".claude/projects/abc/session.jsonl",
		".claude/todos/x.json",
		".claude/shell-snapshots/snap",
		".claude/statsig/cache",
		".claude/.credentials.json",
		".claude/skills/foo/.git/config",
		".claude/skills/foo/node_modules/x/index.js",
	} {
		require.True(t, manifest.IsAlwaysExcluded(p), "%s 必须被恒排除", p)
	}
	for _, p := range []string{
		".claude/settings.json",
		".claude/skills/projects-helper/SKILL.md", // 名字里带 projects 不算
		".claude.json",
	} {
		require.False(t, manifest.IsAlwaysExcluded(p), "%s 不该被恒排除", p)
	}
}

// 任何 include 都拉不回恒排除的路径（spec §3.2）。
func TestMatchPrecedence(t *testing.T) {
	m := manifest.Manifest{
		Version: 1,
		Include: []manifest.Include{
			{Path: ".claude/**", Mode: manifest.ModeTree},
		},
		Exclude: []string{".claude/scratch/**"},
	}
	_, ok := m.Match(".claude/skills/a/SKILL.md")
	require.True(t, ok)

	_, ok = m.Match(".claude/scratch/tmp.md")
	require.False(t, ok, "manifest.exclude 生效")

	_, ok = m.Match(".claude/projects/x.jsonl")
	require.False(t, ok, "恒排除优先于 include")

	_, ok = m.Match(".claude/.credentials.json")
	require.False(t, ok, "恒排除优先于 include")
}

func TestMatchReturnsTheInclude(t *testing.T) {
	m := manifest.Default()
	inc, ok := m.Match(".claude.json")
	require.True(t, ok)
	require.Equal(t, manifest.ModeKeys, inc.Mode)
	require.Equal(t, []string{"mcpServers"}, inc.Keys)

	inc, ok = m.Match(".claude/skills/foo/bar/SKILL.md")
	require.True(t, ok, "** 必须跨层匹配")
	require.Equal(t, manifest.ModeTree, inc.Mode)
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{".claude/skills/**", ".claude/skills/a/b/c.md", true},
		{".claude/skills/**", ".claude/skills", false},
		{".claude/settings.json", ".claude/settings.json", true},
		{".claude/settings.json", ".claude/settings.jsonx", false},
		{"**/.DS_Store", ".claude/skills/.DS_Store", true},
		{"**/.DS_Store", ".DS_Store", true},
		{"**/node_modules/**", "a/node_modules/b/c", true},
		{"**/node_modules/**", "a/node_modules", false},
		{".claude/*.json", ".claude/settings.json", true},
		{".claude/*.json", ".claude/a/b.json", false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, manifest.MatchGlob(c.pattern, c.path),
			"MatchGlob(%q, %q)", c.pattern, c.path)
	}
}

func TestJSONShape(t *testing.T) {
	b, err := manifest.Default().JSON()
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	require.EqualValues(t, 1, raw["version"])
	require.Contains(t, raw, "include")
	require.Contains(t, raw, "exclude")
}
