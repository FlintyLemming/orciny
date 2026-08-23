package protocol_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

func TestM1KindsAreKnown(t *testing.T) {
	for k := protocol.Kind(10); k <= 19; k++ {
		require.True(t, k.IsKnown(), "Kind %d 必须被认识", uint8(k))
		require.NotContains(t, k.String(), "unknown", "Kind %d 必须有名字", uint8(k))
	}
	// 20–29 是 M2 的段，M1 不许侵占。
	require.False(t, protocol.Kind(20).IsKnown())
}

func TestConfigSnapshotRoundTrip(t *testing.T) {
	want := protocol.ConfigSnapshot{
		ConfigSetID: "set1",
		RevisionID:  "rev7",
		Seq:         7,
		Manifest:    []byte(`{"version":1}`),
		Files: []protocol.FileEntry{
			{Path: ".claude/settings.json", Hash: "aa", Size: 12, Mode: 0o600},
			{Path: ".claude.json", Hash: "bb", Size: 34, Mode: 0o644, Keys: []string{"mcpServers"}},
		},
		Checksum:    "cc",
		Credentials: map[string]string{"anthropic_key": "sk-test"},
		Variables:   map[string]string{"workspace": "main"},
		IgnorePaths: []string{".claude/skills/scratch/**"},
		Mode:        protocol.ModeSurvey,
	}
	b, err := protocol.Encode(protocol.KindConfigSnapshot, nil, want)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	require.Equal(t, protocol.KindConfigSnapshot, env.Kind)

	got, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestApplyAckRoundTrip(t *testing.T) {
	want := protocol.ApplyAck{
		RevisionID: "rev7",
		OK:         false,
		Results: []protocol.ApplyResult{
			{Path: ".claude/CLAUDE.md", Action: protocol.ActionOverwrite},
			{Path: ".claude/settings.json", Action: protocol.ActionCreate, Error: "permission denied"},
		},
		Error:      "写入失败",
		RolledBack: true,
		DurationMs: 42,
	}
	b, err := protocol.Encode(protocol.KindApplyAck, nil, want)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	got, err := protocol.DecodePayload[protocol.ApplyAck](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDriftReportRoundTrip(t *testing.T) {
	want := protocol.DriftReport{
		Items: []protocol.DriftItem{
			{Path: ".claude/CLAUDE.md", Kind: protocol.DriftModified, BaseHash: "aa",
				Content: []byte("# 新内容 {{var.k}}"), Mode: 0o644},
			{Path: ".claude/skills/foo/SKILL.md", Kind: protocol.DriftAdded, Content: []byte("x"), Mode: 0o644},
			{Path: ".claude/gone.md", Kind: protocol.DriftDeleted, BaseHash: "bb"},
			{Path: ".claude/huge.md", Kind: protocol.DriftModified, Truncated: true, RestorePartial: true},
		},
		Final: true,
		Full:  true,
	}
	b, err := protocol.Encode(protocol.KindDriftReport, nil, want)
	require.NoError(t, err)
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	got, err := protocol.DecodePayload[protocol.DriftReport](env)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// 加字段必须向后兼容：老版本结构解新版本字节，多出来的字段被忽略。
func TestMachineInfoNewFieldsAreBackwardCompatible(t *testing.T) {
	b, err := protocol.Encode(protocol.KindMachineInfo, nil, protocol.MachineInfo{
		Hostname:    "mac",
		LocalPaused: true,
		ManagedHome: "/Users/x",
	})
	require.NoError(t, err)

	// 只有 M0 五个字段的结构体
	type oldInfo struct {
		Hostname     string            `cbor:"0,keyasint"`
		OS           string            `cbor:"1,keyasint"`
		Arch         string            `cbor:"2,keyasint"`
		AgentVersion string            `cbor:"3,keyasint"`
		ToolVersions map[string]string `cbor:"4,keyasint,omitempty"`
	}
	env, err := protocol.Decode(b)
	require.NoError(t, err)
	old, err := protocol.DecodePayload[oldInfo](env)
	require.NoError(t, err)
	require.Equal(t, "mac", old.Hostname)
}

func TestSizeLimits(t *testing.T) {
	require.Equal(t, 512*1024, protocol.MaxFileSize)
	require.Equal(t, 256*1024, protocol.MaxBatchSize)
	require.Equal(t, 1024*1024, protocol.MaxPayload)
}

func TestConfigSnapshotCarriesProvider(t *testing.T) {
	in := protocol.ConfigSnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Seq: 3, Checksum: "abc",
		Provider: map[string]string{
			"base_url":   "https://open.bigmodel.cn/api/anthropic",
			"auth_token": "sk-zhipu-1234567890",
			"model":      "glm-5.1",
		},
	}
	b, err := protocol.Encode(protocol.KindConfigSnapshot, nil, in)
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	out, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
	require.NoError(t, err)
	require.Equal(t, in.Provider, out.Provider)
}

// legacySnapshot 是 M1 的形状，逐字符照抄 0..9，故意不含 10。
type legacySnapshot struct {
	ConfigSetID string               `cbor:"0,keyasint"`
	RevisionID  string               `cbor:"1,keyasint"`
	Seq         uint32               `cbor:"2,keyasint"`
	Manifest    []byte               `cbor:"3,keyasint"`
	Files       []protocol.FileEntry `cbor:"4,keyasint"`
	Checksum    string               `cbor:"5,keyasint"`
	Credentials map[string]string    `cbor:"6,keyasint,omitempty"`
	Variables   map[string]string    `cbor:"7,keyasint,omitempty"`
	IgnorePaths []string             `cbor:"8,keyasint,omitempty"`
	Mode        uint8                `cbor:"9,keyasint,omitempty"`
}

// 旧 agent 的 ConfigSnapshot 没有字段 10。wire 层必须兼容：
// 收到新字段静默忽略，而不是解码报错（spec §3.4）。
func TestOldSnapshotShapeIgnoresProviderField(t *testing.T) {
	b, err := protocol.Encode(protocol.KindConfigSnapshot, nil, protocol.ConfigSnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Checksum: "abc",
		Credentials: map[string]string{"k": "v"},
		Provider:    map[string]string{"base_url": "https://example.test"},
	})
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	old, err := protocol.DecodePayload[legacySnapshot](env)
	require.NoError(t, err, "旧形状必须能解出来")
	require.Equal(t, "cs1", old.ConfigSetID)
	require.Equal(t, map[string]string{"k": "v"}, old.Credentials)
}

// 反向：新 agent 收到旧 hub 的快照，Provider 为 nil 而不是报错。
func TestNewSnapshotShapeAcceptsMissingProvider(t *testing.T) {
	b, err := protocol.Encode(protocol.KindConfigSnapshot, nil, legacySnapshot{
		ConfigSetID: "cs1", RevisionID: "r1", Checksum: "abc",
	})
	require.NoError(t, err)

	env, err := protocol.Decode(b)
	require.NoError(t, err)
	snap, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
	require.NoError(t, err)
	require.Nil(t, snap.Provider)
}
