package agent

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny"
	"github.com/FlintyLemming/orciny/agent/internal/conn"
	"github.com/FlintyLemming/orciny/agent/internal/identity"
	"github.com/FlintyLemming/orciny/internal/clock"
)

// RunOptions 是 agent.Run 的入参。
type RunOptions struct {
	Dir              string
	HandshakeTimeout time.Duration // 0 → 10s
	ReadTimeout      time.Duration // 0 → 70s
	Logger           *slog.Logger
}

// Run 前台运行 agent，直到 ctx 取消或遇到终局错误。
// 服务单元调用的就是它（经由 `orciny-agent run`）。
func Run(ctx context.Context, o RunOptions) error {
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 70 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	cfg, err := LoadConfig(o.Dir)
	if err != nil {
		return err
	}
	id, err := identity.Load(filepath.Join(o.Dir, identity.DirName))
	if err != nil {
		return err
	}

	o.Logger.Info("agent 启动",
		"version", orciny.Version,
		"hub", cfg.HubURL,
		"fingerprint", id.Fingerprint())

	writeStatus := func(state conn.State, cause error) {
		s := &Status{
			State:       state.String(),
			Since:       time.Now().UTC(),
			HubURL:      cfg.HubURL,
			Fingerprint: id.Fingerprint(),
			Version:     orciny.Version,
		}
		if cause != nil {
			s.LastError = cause.Error()
		}
		if err := SaveStatus(o.Dir, s); err != nil {
			o.Logger.Warn("写运行状态失败", "error", err)
		}
	}

	client := conn.NewClient(conn.ClientConfig{
		Dial: func(ctx context.Context) (*Session, error) {
			return Connect(ctx, ConnectOptions{
				Dir:              o.Dir,
				HubURL:           cfg.HubURL,
				HandshakeTimeout: o.HandshakeTimeout,
				ReadTimeout:      o.ReadTimeout,
				Logger:           o.Logger,
			})
		},
		Clock:   clock.System(),
		Backoff: conn.NewBackoff(time.Second, time.Minute, 0.2, newRand().Float64),
		Logger:  o.Logger,
		OnState: writeStatus,
	})

	return client.Run(ctx)
}

// newRand 用系统熵播种，保证同一时刻启动的多台 agent 抖动不同步。
func newRand() *rand.Rand {
	var seed [16]byte
	if _, err := crand.Read(seed[:]); err != nil {
		// 拿不到系统熵时退回时间播种：抖动的目的是打散，不是保密。
		return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0))
	}
	return rand.New(rand.NewPCG(
		binary.LittleEndian.Uint64(seed[0:8]),
		binary.LittleEndian.Uint64(seed[8:16]),
	))
}
