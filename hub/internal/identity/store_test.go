package identity_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/identity"
	"github.com/FlintyLemming/orciny/protocol"
)

func TestLoadGeneratesKeyWhenMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	s := identity.NewStore(p)

	require.NoError(t, s.Load())
	require.FileExists(t, p)
	require.Len(t, s.PublicKey(), ed25519.PublicKeySize)
}

func TestGeneratedKeyFileIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位")
	}
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	require.NoError(t, identity.NewStore(p).Load())

	st, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "hub 私钥必须是 0600")
}

func TestLoadIsStableAcrossRestarts(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)

	first := identity.NewStore(p)
	require.NoError(t, first.Load())

	second := identity.NewStore(p)
	require.NoError(t, second.Load())

	require.Equal(t, first.PublicKey(), second.PublicKey(),
		"重启后必须是同一把钥匙，否则全机队要重新 enroll")
	require.Equal(t, first.Fingerprint(), second.Fingerprint())
}

func TestLoadIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	s := identity.NewStore(p)
	require.NoError(t, s.Load())
	pub := s.PublicKey()
	require.NoError(t, s.Load())
	require.Equal(t, pub, s.PublicKey())
}

func TestSignIsVerifiableWithPublicKey(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())

	msg := protocol.HubSigPayload(make([]byte, protocol.NonceSize), make([]byte, protocol.NonceSize))
	require.True(t, ed25519.Verify(s.PublicKey(), msg, s.Sign(msg)))
}

func TestFingerprintMatchesProtocolDerivation(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())
	require.Equal(t, protocol.Fingerprint(s.PublicKey()), s.Fingerprint())
}

func TestPublicKeyBase64RoundTrips(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.NoError(t, s.Load())

	back, err := protocol.DecodePublicKey(s.PublicKeyBase64())
	require.NoError(t, err)
	require.Equal(t, s.PublicKey(), back)
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), identity.KeyFileName)
	require.NoError(t, os.WriteFile(p, []byte("这不是 PEM"), 0o600))

	err := identity.NewStore(p).Load()
	require.Error(t, err, "损坏的密钥文件必须报错而不是静默重新生成——"+
		"静默生成会让全机队突然连不上，且没人知道为什么")
	require.NotContains(t, err.Error(), "这不是 PEM", "错误信息不得回显文件内容")
}

func TestAccessorsBeforeLoadDoNotPanic(t *testing.T) {
	s := identity.NewStore(filepath.Join(t.TempDir(), identity.KeyFileName))
	require.Nil(t, s.PublicKey())
	require.Empty(t, s.Fingerprint())
	require.Nil(t, s.Sign([]byte("x")))
}
