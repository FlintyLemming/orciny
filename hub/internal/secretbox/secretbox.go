// Package secretbox 管秘密的对称加密与主密钥。
//
// 秘密以 AES-GCM 密文落库，明文只在两处出现：hub 内存里（组装 ConfigSnapshot
// 时）与 agent 的 ~/.orciny/secrets.json（M1 spec §6.2）。Revision 与 blob 里
// **永远只有占位符**。
//
// 这个包是从 credentials 拆出来的（M1.6 spec §2.5）：凭据实体没了，
// 但「加密落库、末四位回显、启动自检」这套复用降回代码层，由本包承担。
// KeyFileName 与 EnvKeyName 的值**不变**，存量密文继续解得开。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
)

// KeyFileName 是主密钥在数据目录下的文件名。
const KeyFileName = "secret.key"

// EnvKeyName 是主密钥的环境变量名，值为 32 字节的 base64。
const EnvKeyName = "ORCINY_SECRET_KEY"

// MinValueLen 是秘密值的长度下限（M1 spec §6.4）。
//
// 理由不是安全强度，是**还原的可靠性**：一个 6 字符的「密钥」在文件里
// 到处误匹配的风险，远大于它作为密钥的价值。provider 的 key 一样要被
// agent 还原，这条约束原样适用（M1.6 spec §2.5）。
const MinValueLen = 8

// ErrShortValue 供路由层映射成 400。
var ErrShortValue = errors.New("secretbox: 值过短")

const keySize = 32

// LoadMasterKey 按 M1 spec §6.6 的优先级取主密钥：
// 环境变量 → 数据目录里的 secret.key（不存在则生成，0600）。
func LoadMasterKey(dataDir string) ([]byte, error) {
	if v := os.Getenv(EnvKeyName); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("secretbox: %s 不是合法的 base64: %w", EnvKeyName, err)
		}
		if len(k) != keySize {
			return nil, fmt.Errorf("secretbox: %s 解出 %d 字节，应为 %d", EnvKeyName, len(k), keySize)
		}
		return k, nil
	}

	path := filepath.Join(dataDir, KeyFileName)
	b, err := os.ReadFile(path)
	if err == nil {
		k, derr := base64.StdEncoding.DecodeString(string(b))
		if derr != nil || len(k) != keySize {
			return nil, fmt.Errorf("secretbox: %s 内容损坏（应为 %d 字节的 base64）", path, keySize)
		}
		return k, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretbox: 读取主密钥: %w", err)
	}

	k := make([]byte, keySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("secretbox: 生成主密钥: %w", err)
	}
	if err := atomicfile.Write(path, []byte(base64.StdEncoding.EncodeToString(k)), 0o600); err != nil {
		return nil, fmt.Errorf("secretbox: 写入主密钥: %w", err)
	}
	return k, nil
}

// Encrypt 返回 base64(nonce ‖ ciphertext ‖ tag)。
func Encrypt(key []byte, plaintext string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: 生成 nonce: %w", err)
	}
	out := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func Decrypt(key []byte, cipherB64 string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return "", fmt.Errorf("secretbox: 密文不是合法的 base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("secretbox: 密文过短")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		// 不带上任何密文片段：错误信息会进日志。
		return "", fmt.Errorf("secretbox: 解密失败（主密钥不匹配或数据损坏）")
	}
	return string(pt), nil
}

// Last4 返回末四位，UI 回显用。短于四位时原样返回。
func Last4(v string) string {
	if len(v) <= 4 {
		return v
	}
	return v[len(v)-4:]
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secretbox: 主密钥应为 %d 字节，实际 %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: 构造 AES: %w", err)
	}
	return cipher.NewGCM(block)
}
