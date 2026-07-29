package agent

import (
	"context"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/agent/internal/probe"
	"github.com/FlintyLemming/orciny/protocol"
)

// ConnectOptions 是 agent.Connect 的入参。
type ConnectOptions struct {
	Dir              string
	HubURL           string
	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration
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

	hostname, goos, goarch := probe.Host()
	return conn.Dial(ctx, conn.Config{
		HubURL:       o.HubURL,
		Identity:     id,
		HubPub:       hubPub,
		AgentVersion: orciny.Version,
		Info: protocol.MachineInfo{
			Hostname:     hostname,
			OS:           goos,
			Arch:         goarch,
			AgentVersion: orciny.Version,
		},
		HandshakeTimeout: o.HandshakeTimeout,
		ReadTimeout:      o.ReadTimeout,
	})
}
