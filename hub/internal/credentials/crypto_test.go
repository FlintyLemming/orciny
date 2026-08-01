package credentials_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/credentials"
)

func TestLoadMasterKeyGeneratesFileOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	k1, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Len(t, k1, 32)

	info, err := os.Stat(filepath.Join(dir, credentials.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "主密钥文件必须 0600")

	k2, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, k1, k2, "第二次加载必须拿到同一把钥匙")
}

func TestLoadMasterKeyPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	t.Setenv("ORCINY_SECRET_KEY", base64.StdEncoding.EncodeToString(want))

	got, err := credentials.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoFileExists(t, filepath.Join(dir, credentials.KeyFileName),
		"环境变量给了钥匙就不该再生成文件")
}

func TestLoadMasterKeyRejectsBadEnv(t *testing.T) {
	t.Setenv("ORCINY_SECRET_KEY", "not-base64!!")
	_, err := credentials.LoadMasterKey(t.TempDir())
	require.Error(t, err)

	t.Setenv("ORCINY_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte("太短")))
	_, err = credentials.LoadMasterKey(t.TempDir())
	require.ErrorContains(t, err, "32")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	for _, v := range []string{"", "sk-ant-abc123", "含中文的值", string(make([]byte, 4096))} {
		enc, err := credentials.Encrypt(key, v)
		require.NoError(t, err)
		if v != "" {
			// 空串跳过：strings.Contains(s, "") 恒为真，这条断言对它没有意义。
			require.NotContains(t, enc, v, "密文里不该出现明文")
		}

		dec, err := credentials.Decrypt(key, enc)
		require.NoError(t, err)
		require.Equal(t, v, dec)
	}
}

func TestEncryptUsesFreshNonce(t *testing.T) {
	key, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	a, err := credentials.Encrypt(key, "same")
	require.NoError(t, err)
	b, err := credentials.Encrypt(key, "same")
	require.NoError(t, err)
	require.NotEqual(t, a, b, "同一明文两次加密必须不同（nonce 每次新生成）")
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	k1, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	k2, err := credentials.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	enc, err := credentials.Encrypt(k1, "sk-secret")
	require.NoError(t, err)
	_, err = credentials.Decrypt(k2, enc)
	require.Error(t, err, "换了主密钥必须解不开，而不是解出垃圾")
}
