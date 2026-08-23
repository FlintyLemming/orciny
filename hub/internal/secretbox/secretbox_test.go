package secretbox_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/FlintyLemming/orciny/hub/internal/secretbox"
)

// 常量的值是**存量数据的兼容契约**（spec §2.5）：改了它们，
// 从备份恢复的库就再也解不开了。锁死。
func TestConstantsAreUnchanged(t *testing.T) {
	require.Equal(t, "secret.key", secretbox.KeyFileName)
	require.Equal(t, "ORCINY_SECRET_KEY", secretbox.EnvKeyName)
	require.Equal(t, 8, secretbox.MinValueLen)
}

func TestLoadMasterKeyGeneratesFileOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	k1, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Len(t, k1, 32)

	info, err := os.Stat(filepath.Join(dir, secretbox.KeyFileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "主密钥文件必须 0600")

	k2, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, k1, k2, "第二次加载必须拿到同一把钥匙")
}

func TestLoadMasterKeyPrefersEnv(t *testing.T) {
	dir := t.TempDir()
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	t.Setenv(secretbox.EnvKeyName, base64.StdEncoding.EncodeToString(want))

	got, err := secretbox.LoadMasterKey(dir)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoFileExists(t, filepath.Join(dir, secretbox.KeyFileName),
		"环境变量给了钥匙就不该再生成文件")
}

func TestLoadMasterKeyRejectsBadEnv(t *testing.T) {
	t.Setenv(secretbox.EnvKeyName, "not-base64!!")
	_, err := secretbox.LoadMasterKey(t.TempDir())
	require.Error(t, err)

	t.Setenv(secretbox.EnvKeyName, base64.StdEncoding.EncodeToString([]byte("short")))
	_, err = secretbox.LoadMasterKey(t.TempDir())
	require.Error(t, err, "长度不对的钥匙必须拒绝")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	ct, err := secretbox.Encrypt(key, "sk-zhipu-abcdef123456")
	require.NoError(t, err)
	require.NotContains(t, ct, "sk-zhipu", "密文里不许出现明文片段")

	pt, err := secretbox.Decrypt(key, ct)
	require.NoError(t, err)
	require.Equal(t, "sk-zhipu-abcdef123456", pt)
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	key, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	a, err := secretbox.Encrypt(key, "same-value")
	require.NoError(t, err)
	b, err := secretbox.Encrypt(key, "same-value")
	require.NoError(t, err)
	require.NotEqual(t, a, b, "每次加密都要用新 nonce")
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	k1, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)
	k2, err := secretbox.LoadMasterKey(t.TempDir())
	require.NoError(t, err)

	ct, err := secretbox.Encrypt(k1, "sk-zhipu-abcdef123456")
	require.NoError(t, err)
	_, err = secretbox.Decrypt(k2, ct)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sk-zhipu",
		"错误信息会进日志，不许带上任何密文或明文片段")
}

func TestLast4(t *testing.T) {
	require.Equal(t, "3456", secretbox.Last4("sk-zhipu-abcdef123456"))
	require.Equal(t, "abc", secretbox.Last4("abc"), "短于四位时原样返回")
	require.Equal(t, "", secretbox.Last4(""))
}
