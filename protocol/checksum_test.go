package protocol_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/protocol"
)

// 写死期望值：改了拼装顺序或分隔符而无人察觉，会让全部历史版本失去意义
// （spec §10.1 点名要这条）。
func TestChecksumIsPinned(t *testing.T) {
	files := []protocol.FileEntry{
		{Path: ".claude/settings.json", Hash: "aa", Size: 1, Mode: 0o600},
		{Path: ".claude/CLAUDE.md", Hash: "bb", Size: 2, Mode: 0o644},
	}
	// 手工按口径拼一遍：按 path 字典序，每条 path\x00hash\x00mode\n，
	// mode 为八进制无前导 0。
	want := sha256.Sum256([]byte(
		".claude/CLAUDE.md\x00bb\x00644\n" +
			".claude/settings.json\x00aa\x00600\n"))
	require.Equal(t, hex.EncodeToString(want[:]), protocol.Checksum(files))
}

func TestChecksumIgnoresInputOrder(t *testing.T) {
	a := []protocol.FileEntry{
		{Path: "b", Hash: "2", Mode: 0o644},
		{Path: "a", Hash: "1", Mode: 0o600},
	}
	b := []protocol.FileEntry{
		{Path: "a", Hash: "1", Mode: 0o600},
		{Path: "b", Hash: "2", Mode: 0o644},
	}
	require.Equal(t, protocol.Checksum(a), protocol.Checksum(b))
}

func TestChecksumChangesWithMode(t *testing.T) {
	a := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o600}}
	b := []protocol.FileEntry{{Path: "a", Hash: "1", Mode: 0o644}}
	require.NotEqual(t, protocol.Checksum(a), protocol.Checksum(b))
}

// Size 不进 checksum：它由 Hash 唯一决定，进去只是多一处可能不一致的地方。
func TestChecksumIgnoresSize(t *testing.T) {
	a := []protocol.FileEntry{{Path: "a", Hash: "1", Size: 10, Mode: 0o600}}
	b := []protocol.FileEntry{{Path: "a", Hash: "1", Size: 999, Mode: 0o600}}
	require.Equal(t, protocol.Checksum(a), protocol.Checksum(b))
}

func TestChecksumOfEmptyIsStable(t *testing.T) {
	require.Equal(t, protocol.Checksum(nil), protocol.Checksum([]protocol.FileEntry{}))
}
