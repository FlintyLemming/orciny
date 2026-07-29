// Package identity 管理 hub 的长期 Ed25519 密钥。
//
// 这把钥匙是整个机队信任根的一半：agent 在 enroll 时钉扎它的公钥，
// 此后每次握手都用它验证「对面确实是我认识的那个 hub」（spec §4.1）。
//
// 它放在 PocketBase 的数据目录里，因此自动被 PocketBase 的备份机制覆盖。
// 反过来说：恢复备份时若丢了这个文件，所有 agent 都会因签名验证失败而
// 拒绝连接，需要全部重新 enroll——这一条必须写进运维文档（spec §4.5）。
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

// KeyFileName 是密钥在数据目录下的文件名。
const KeyFileName = "orciny_hub_key.pem"

const pemBlockType = "PRIVATE KEY"

// Store 持有 hub 的密钥。零值不可用，用 NewStore 构造，用前先 Load。
type Store struct {
	path string

	mu   sync.RWMutex
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func NewStore(path string) *Store { return &Store{path: path} }

// Load 加载密钥；文件不存在则生成并落盘。可重复调用。
//
// 文件存在但读不懂时**报错而不是重新生成**：静默生成会让全机队在某次
// 磁盘故障后集体掉线，且现场没有任何线索。
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.priv != nil {
		return nil
	}

	b, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		priv, err := parsePEM(b)
		if err != nil {
			// 注意：不把文件内容放进错误信息。
			return fmt.Errorf("解析 hub 密钥 %s: %w", s.path, err)
		}
		s.priv = priv
	case errors.Is(err, os.ErrNotExist):
		_, priv, genErr := ed25519.GenerateKey(rand.Reader)
		if genErr != nil {
			return fmt.Errorf("生成 hub 密钥: %w", genErr)
		}
		der, mErr := x509.MarshalPKCS8PrivateKey(priv)
		if mErr != nil {
			return fmt.Errorf("序列化 hub 密钥: %w", mErr)
		}
		encoded := pem.EncodeToMemory(&pem.Block{Type: pemBlockType, Bytes: der})
		if wErr := atomicfile.Write(s.path, encoded, 0o600); wErr != nil {
			return fmt.Errorf("写入 hub 密钥: %w", wErr)
		}
		s.priv = priv
	default:
		return fmt.Errorf("读取 hub 密钥 %s: %w", s.path, err)
	}

	s.pub = s.priv.Public().(ed25519.PublicKey)
	return nil
}

func parsePEM(b []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemBlockType {
		return nil, errors.New("不是合法的 PEM 私钥块")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("PKCS#8 解析失败")
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("期望 Ed25519 私钥，实际是 %T", key)
	}
	return priv, nil
}

// PublicKey 返回公钥；Load 之前返回 nil。
func (s *Store) PublicKey() ed25519.PublicKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pub
}

// PublicKeyBase64 返回 wire/DB 统一编码的公钥。
func (s *Store) PublicKeyBase64() string {
	pub := s.PublicKey()
	if pub == nil {
		return ""
	}
	return protocol.EncodePublicKey(pub)
}

// Fingerprint 返回 hub 公钥指纹，用于设置页展示与 --hub-key 带外校验。
func (s *Store) Fingerprint() string {
	pub := s.PublicKey()
	if pub == nil {
		return ""
	}
	return protocol.Fingerprint(pub)
}

// Sign 用 hub 私钥签名。Load 之前返回 nil。
func (s *Store) Sign(msg []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.priv == nil {
		return nil
	}
	return ed25519.Sign(s.priv, msg)
}
