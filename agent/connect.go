package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/probe"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/protocol"
)

// ConnectOptions 是 agent.Connect 的入参。
type ConnectOptions struct {
	Dir              string
	HubURL           string
	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration

	// Logger 为 nil 时走 slog.Default()。不传的后果是连接层的日志
	// （撤销通知、未知消息）用默认的文本格式打出来，与 agent 其余部分的
	// JSON 日志不一致。
	Logger *slog.Logger

	// OnMessage 接收握手完成之后的业务消息（M1 配置下发）。
	OnMessage func(protocol.Envelope)
}

// Session 是一条已建立的连接。这是 conn.Session 的别名，
// 让测试脚手架不必看到 internal 包。
type Session = conn.Session

// Connect 用本机身份连接 hub 并完成握手。
func Connect(ctx context.Context, o ConnectOptions) (*Session, error) {
	dir := filepath.Join(o.Dir, identity.DirName)
	id, err := identity.Load(dir)
	if err != nil {
		return nil, err
	}
	hubPub, err := identity.LoadHubKey(dir)
	if err != nil {
		return nil, err
	}

	info := probe.Collect(ctx, orciny.Version)
	// pause 是本地开关：握手时带上，让面板知道本机当前是否暂停配置管理。
	if st, err := state.Load(o.Dir); err == nil {
		info.LocalPaused = st.Paused
		info.ManagedHome = managedHomeFromConfig(o.Dir)
	}

	return conn.Dial(ctx, conn.Config{
		HubURL:           o.HubURL,
		Identity:         id,
		HubPub:           hubPub,
		AgentVersion:     orciny.Version,
		Info:             info,
		HandshakeTimeout: o.HandshakeTimeout,
		ReadTimeout:      o.ReadTimeout,
		Logger:           o.Logger,
		OnMessage:        o.OnMessage,
	})
}

// managedHomeFromConfig 读配置里的 managed_home；读不到就留空，不阻断握手。
func managedHomeFromConfig(dir string) string {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return ""
	}
	home, err := cfg.ManagedHomeDir()
	if err != nil {
		return ""
	}
	return home
}
