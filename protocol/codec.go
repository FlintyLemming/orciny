package protocol

import (
	"errors"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

// Envelope 是 wire 上唯一的顶层结构（spec §3.1）。
//
// 与 beszel 的两结构体方案不同：这里加一种消息只需要加一个 Kind 和一个
// payload 类型，信封本身永远不动。
type Envelope struct {
	Kind Kind `cbor:"0,keyasint"`
	// ID 用于请求-响应配对；通知类消息为 nil。M0 全部消息都是单向通知，
	// 字段先在 wire 上占位，M1 的 ConfigPull 会用到。
	ID *uint32 `cbor:"1,keyasint,omitempty"`
	// Data 延迟解码：先按 Kind 分派，再解成对应类型，不必先解成 map。
	Data cbor.RawMessage `cbor:"2,keyasint,omitempty,omitzero"`
}

// ErrNoPayload 表示信封没带 payload，但调用方想解一个出来。
var ErrNoPayload = errors.New("protocol: 信封不含 payload")

var (
	encMode cbor.EncMode
	decMode cbor.DecMode
)

func init() {
	var err error
	encMode, err = cbor.EncOptions{}.EncMode()
	if err != nil {
		panic("protocol: 初始化 CBOR 编码模式失败: " + err.Error())
	}
	// DupMapKeyEnforcedAPF：重复键直接报错，堵掉「同一字段出现两次、
	// 两端各取一个」这类解析歧义。
	// MaxArrayElements / MaxMapPairs 收紧，避免畸形输入撑爆内存。
	decMode, err = cbor.DecOptions{
		DupMapKey:        cbor.DupMapKeyEnforcedAPF,
		MaxArrayElements: 1024,
		MaxMapPairs:      1024,
	}.DecMode()
	if err != nil {
		panic("protocol: 初始化 CBOR 解码模式失败: " + err.Error())
	}
}

// Encode 把一条消息打成 wire 字节。payload 为 nil 时不产生 Data 字段。
func Encode(kind Kind, id *uint32, payload any) ([]byte, error) {
	env := Envelope{Kind: kind, ID: id}
	if payload != nil {
		raw, err := encMode.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("protocol: 编码 %s 的 payload: %w", kind, err)
		}
		env.Data = raw
	}
	b, err := encMode.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("protocol: 编码信封: %w", err)
	}
	return b, nil
}

// Decode 解出信封。未知 Kind 不算错误——调用方用 Kind.IsKnown() 决定怎么办。
func Decode(b []byte) (Envelope, error) {
	var env Envelope
	if err := decMode.Unmarshal(b, &env); err != nil {
		return Envelope{}, fmt.Errorf("protocol: 解码信封: %w", err)
	}
	return env, nil
}

// DecodePayload 把信封里的 Data 解成 T。
func DecodePayload[T any](env Envelope) (T, error) {
	var out T
	if len(env.Data) == 0 {
		return out, ErrNoPayload
	}
	if err := decMode.Unmarshal(env.Data, &out); err != nil {
		return out, fmt.Errorf("protocol: 解码 %s 的 payload: %w", env.Kind, err)
	}
	return out, nil
}
