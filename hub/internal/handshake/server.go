package handshake

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"

	"github.com/blang/semver/v4"

	"github.com/FlintyLemming/orciny/protocol"
)

// Server 持有握手的不变部分，按连接派生 Session。
type Server struct {
	store      Store
	signer     Signer
	minVersion semver.Version
	rnd        io.Reader
}

func NewServer(store Store, signer Signer, minVersion semver.Version, rnd io.Reader) *Server {
	return &Server{store: store, signer: signer, minVersion: minVersion, rnd: rnd}
}

// Outcome 是握手的结论。非 nil 即表示握手已结束——OK 为真则连接可用，
// 为假则调用方应先把 reply 发出去（让 agent 知道原因），再关闭连接。
type Outcome struct {
	OK          bool
	MachineID   string
	Fingerprint string
	Code        uint8
	Reason      string
}

type state uint8

const (
	stateAwaitHello state = iota
	stateAwaitAuth
	stateDone
)

// Session 是一次握手的状态。非并发安全——每个连接一个，由该连接的读循环独占。
type Session struct {
	srv *Server

	state       state
	fingerprint string
	machine     *Machine
	clientNonce []byte
	serverNonce []byte
}

func (s *Server) NewSession() *Session {
	return &Session{srv: s, state: stateAwaitHello}
}

// Done 报告握手是否已结束（无论成败）。
func (ss *Session) Done() bool { return ss.state == stateDone }

// Handle 处理一条入站信封。
//
// 返回的 error 表示**协议违规**（顺序错乱、字段长度不对）——调用方应直接
// 断开，不必回复：对面不是我们的 agent，多说无益。
// 返回的 Outcome 表示握手有了结论，此时 reply 一定非空。
func (ss *Session) Handle(env protocol.Envelope) ([]byte, *Outcome, error) {
	switch ss.state {
	case stateAwaitHello:
		return ss.handleHello(env)
	case stateAwaitAuth:
		return ss.handleAuth(env)
	default:
		return nil, nil, errors.New("handshake: 握手已结束，不应再收到握手消息")
	}
}

func (ss *Session) handleHello(env protocol.Envelope) ([]byte, *Outcome, error) {
	if env.Kind != protocol.KindHello {
		return nil, nil, fmt.Errorf("handshake: 期望 hello，收到 %s", env.Kind)
	}
	hello, err := protocol.DecodePayload[protocol.Hello](env)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 解析 hello: %w", err)
	}
	if len(hello.ClientNonce) != protocol.NonceSize {
		return nil, nil, fmt.Errorf("handshake: clientNonce 长度应为 %d，实际 %d",
			protocol.NonceSize, len(hello.ClientNonce))
	}

	// 版本门槛在查库之前：低版本 agent 不该消耗一次数据库查询。
	v, err := semver.Parse(hello.AgentVersion)
	if err != nil || v.LT(ss.srv.minVersion) {
		return ss.reject(hello.Fingerprint, protocol.CodeVersionTooOld,
			fmt.Sprintf("agent 版本过旧，需 >= %s", ss.srv.minVersion))
	}

	machine, err := ss.srv.store.FindByFingerprint(hello.Fingerprint)
	if err != nil {
		return ss.reject(hello.Fingerprint, protocol.CodeUnknownFingerprint,
			"该指纹未登记，请重新执行 orciny-agent enroll")
	}

	serverNonce, err := protocol.NewNonce(ss.srv.rnd)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 生成 serverNonce: %w", err)
	}

	ss.fingerprint = hello.Fingerprint
	ss.machine = machine
	ss.clientNonce = hello.ClientNonce
	ss.serverNonce = serverNonce
	ss.state = stateAwaitAuth

	reply, err := protocol.Encode(protocol.KindChallenge, nil, protocol.Challenge{
		ServerNonce: serverNonce,
		HubSig:      ss.srv.signer.Sign(protocol.HubSigPayload(hello.ClientNonce, serverNonce)),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码 challenge: %w", err)
	}
	return reply, nil, nil
}

func (ss *Session) handleAuth(env protocol.Envelope) ([]byte, *Outcome, error) {
	if env.Kind != protocol.KindAuth {
		return nil, nil, fmt.Errorf("handshake: 期望 auth，收到 %s", env.Kind)
	}
	auth, err := protocol.DecodePayload[protocol.Auth](env)
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 解析 auth: %w", err)
	}

	payload := protocol.AgentSigPayload(ss.serverNonce, ss.clientNonce)
	if !ed25519.Verify(ss.machine.PubKey, payload, auth.AgentSig) {
		return ss.reject(ss.fingerprint, protocol.CodeBadSignature, "签名验证失败")
	}

	ss.state = stateDone
	reply, err := protocol.Encode(protocol.KindAuthResult, nil, protocol.AuthResult{OK: true})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码 auth_result: %w", err)
	}
	return reply, &Outcome{
		OK:          true,
		MachineID:   ss.machine.ID,
		Fingerprint: ss.fingerprint,
	}, nil
}

func (ss *Session) reject(fingerprint string, code uint8, reason string) ([]byte, *Outcome, error) {
	ss.state = stateDone
	reply, err := protocol.Encode(protocol.KindAuthResult, nil, protocol.AuthResult{
		OK:     false,
		Reason: reason,
		Code:   code,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("handshake: 编码拒绝响应: %w", err)
	}
	return reply, &Outcome{
		OK:          false,
		Fingerprint: fingerprint,
		Code:        code,
		Reason:      reason,
	}, nil
}
