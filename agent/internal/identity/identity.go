// Package identity 管理 agent 的机器身份：自己的 Ed25519 密钥对，
// 以及钉扎的 hub 公钥（spec §4.3 / §7.1）。
//
// 私钥永不上网。指纹是公钥的函数，因此「删掉这个目录」等价于「这是一台
// 新机器」——密钥丢了本来就该重新建立信任。
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

const (
	// DirName 是 agent 目录下存放密钥的子目录名：~/.orciny/identity/
	DirName = "identity"
	// KeyFileName 是 agent 私钥文件名。
	KeyFileName = "agent.key"
	// HubKeyFile 是钉扎的 hub 公钥文件名。
	HubKeyFile = "hub.pub"

	pemBlockType = "PRIVATE KEY"
)

var (
	// ErrNoIdentity 表示本机还没有密钥——通常意味着尚未 enroll。
	ErrNoIdentity = errors.New("identity: 本机尚无 agent 密钥，请先执行 orciny-agent enroll")
	// ErrNoHubKey 表示还没钉扎 hub 公钥。
	ErrNoHubKey = errors.New("identity: 尚未钉扎 hub 公钥，请先执行 orciny-agent enroll")
)

// Identity 是本机的密钥对。
type Identity struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// LoadOrCreate 读取已有密钥；没有则生成一对并落盘（0600）。
// dir 是 identity 子目录的完整路径。
func LoadOrCreate(dir string) (*Identity, error) {
	id, err := Load(dir)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNoIdentity) {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: 生成密钥: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("identity: 序列化私钥: %w", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: pemBlockType, Bytes: der})
	if err := atomicfile.Write(filepath.Join(dir, KeyFileName), encoded, 0o600); err != nil {
		return nil, fmt.Errorf("identity: 写入私钥: %w", err)
	}
	return &Identity{Priv: priv, Pub: pub}, nil
}

// Load 只读取，不生成。密钥不存在时返回 ErrNoIdentity。
func Load(dir string) (*Identity, error) {
	path := filepath.Join(dir, KeyFileName)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoIdentity
	}
	if err != nil {
		return nil, fmt.Errorf("identity: 读取 %s: %w", path, err)
	}

	block, _ := pem.Decode(b)
	if block == nil || block.Type != pemBlockType {
		return nil, fmt.Errorf("identity: %s 不是合法的 PEM 私钥块", path)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: %s 的 PKCS#8 解析失败", path)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("identity: %s 期望 Ed25519 私钥，实际是 %T", path, key)
	}
	return &Identity{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
}

// Fingerprint 返回本机指纹，与 hub 侧 machines.fingerprint 一致。
func (i *Identity) Fingerprint() string { return protocol.Fingerprint(i.Pub) }

// PublicKeyBase64 返回 enroll 请求里要带的公钥表示。
func (i *Identity) PublicKeyBase64() string { return protocol.EncodePublicKey(i.Pub) }

// Sign 用 agent 私钥签名。
func (i *Identity) Sign(msg []byte) []byte { return ed25519.Sign(i.Priv, msg) }

// PinHubKey 把 hub 公钥钉扎到本地（TOFU，spec §4.4）。
// 是否允许覆盖由调用方决定——本层只负责写。
func PinHubKey(dir string, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("identity: hub 公钥长度应为 %d 字节，实际 %d", ed25519.PublicKeySize, len(pub))
	}
	data := []byte(protocol.EncodePublicKey(pub) + "\n")
	if err := atomicfile.Write(filepath.Join(dir, HubKeyFile), data, 0o600); err != nil {
		return fmt.Errorf("identity: 写入 hub 公钥: %w", err)
	}
	return nil
}

// LoadHubKey 读取钉扎的 hub 公钥。
//
// 解析失败一律是硬错误：hub.pub 被改动意味着要么遭中间人，要么 hub 换了
// 密钥，两种情况都需要人来判断（spec §7.2）。
func LoadHubKey(dir string) (ed25519.PublicKey, error) {
	path := filepath.Join(dir, HubKeyFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoHubKey
	}
	if err != nil {
		return nil, fmt.Errorf("identity: 读取 %s: %w", path, err)
	}
	pub, err := protocol.DecodePublicKey(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("identity: %s 内容无法解析为 hub 公钥: %w", path, err)
	}
	return pub, nil
}
