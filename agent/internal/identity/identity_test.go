package identity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestLoadOrCreateGeneratesKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)

	id, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	require.Len(t, id.Pub, ed25519.PublicKeySize)
	require.FileExists(t, filepath.Join(dir, identity.KeyFileName))
}

func TestAgentKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	dir := filepath.Join(t.TempDir(), identity.DirName)
	_, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	st, err := os.Stat(filepath.Join(dir, identity.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)

	a, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	b, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	require.Equal(t, a.Pub, b.Pub, "已有密钥时不得重新生成——那等于把这台机器变成新机器")
	require.Equal(t, a.Fingerprint(), b.Fingerprint())
}

func TestLoadWithoutKeyReportsErrNoIdentity(t *testing.T) {
	_, err := identity.Load(filepath.Join(t.TempDir(), identity.DirName))
	require.ErrorIs(t, err, identity.ErrNoIdentity)
}

func TestFingerprintMatchesProtocolDerivation(t *testing.T) {
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)
	require.Equal(t, protocol.Fingerprint(id.Pub), id.Fingerprint())
	require.Len(t, id.Fingerprint(), 32)
}

func TestSignIsVerifiable(t *testing.T) {
	id, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), identity.DirName))
	require.NoError(t, err)

	msg := protocol.AgentSigPayload(make([]byte, protocol.NonceSize), make([]byte, protocol.NonceSize))
	require.True(t, ed25519.Verify(id.Pub, msg, id.Sign(msg)))
}

func TestPinAndLoadHubKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	require.NoError(t, identity.PinHubKey(dir, pub))
	got, err := identity.LoadHubKey(dir)
	require.NoError(t, err)
	require.Equal(t, pub, got)
}

func TestHubKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	dir := filepath.Join(t.TempDir(), identity.DirName)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, identity.PinHubKey(dir, pub))

	st, err := os.Stat(filepath.Join(dir, identity.HubKeyFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestLoadHubKeyMissing(t *testing.T) {
	_, err := identity.LoadHubKey(filepath.Join(t.TempDir(), identity.DirName))
	require.ErrorIs(t, err, identity.ErrNoHubKey)
}

func TestLoadHubKeyRejectsGarbage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, identity.HubKeyFile), []byte("不是密钥"), 0o600))

	_, err := identity.LoadHubKey(dir)
	require.Error(t, err)
	// 被篡改的 hub.pub 会导致 agent 进入 Compromised（spec §7.2），
	// 因此这里必须是硬错误，且能被 errors.Is 识别为 ErrHubKeyUnusable。
	require.ErrorIs(t, err, identity.ErrHubKeyUnusable)
}

// 钉扎是一次性的：PinHubKey 覆盖已有文件的行为要明确。
// 覆盖由调用方（enroll 流程）决定，本层只负责写。
func TestPinHubKeyOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), identity.DirName)
	first, _, _ := ed25519.GenerateKey(rand.Reader)
	second, _, _ := ed25519.GenerateKey(rand.Reader)

	require.NoError(t, identity.PinHubKey(dir, first))
	require.NoError(t, identity.PinHubKey(dir, second))

	got, err := identity.LoadHubKey(dir)
	require.NoError(t, err)
	require.Equal(t, second, got)
}
