package configsets_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
