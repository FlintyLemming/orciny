// Package handshake 实现 hub 侧的双向挑战-应答（spec §4.2）。
//
// 状态机是纯的：不碰网络、不碰时钟、不写库。它吃一个信封，吐一个要发出去的
// 信封和（可能的）结论。超时、断开、事件写入都留给 ws 包。
// 这样握手的全部分支都能在包内穷举，而不必去搭一个真连接。
package handshake

import (
	"crypto/ed25519"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/protocol"
)

// ErrNotFound 表示该指纹没有对应的机器记录。
var ErrNotFound = errors.New("handshake: 指纹未登记")

// Machine 是握手需要知道的全部机器信息。
type Machine struct {
	ID     string
	PubKey ed25519.PublicKey
}

// Store 按指纹查机器。
type Store interface {
	FindByFingerprint(fingerprint string) (*Machine, error)
}

// Signer 签名握手挑战，由 hub 的 identity.Store 实现。
type Signer interface {
	Sign(msg []byte) []byte
}

type appStore struct{ app core.App }

// NewAppStore 返回基于 PocketBase 的 Store 实现。
func NewAppStore(app core.App) Store { return appStore{app: app} }

func (s appStore) FindByFingerprint(fingerprint string) (*Machine, error) {
	rec, err := s.app.FindFirstRecordByData("machines", "fingerprint", fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("handshake: 查询机器: %w", err)
	}
	pub, err := protocol.DecodePublicKey(rec.GetString("pub_key"))
	if err != nil {
		// 库里的公钥坏了：这台机器无法验证，等同于未登记。
		return nil, fmt.Errorf("handshake: 机器 %s 的公钥无法解析: %w", rec.Id, err)
	}
	return &Machine{ID: rec.Id, PubKey: pub}, nil
}
