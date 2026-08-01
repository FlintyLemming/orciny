package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/syncer"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

// SyncOptions 是 SyncOnce 的入参。
type SyncOptions struct {
	Dir              string
	HandshakeTimeout time.Duration
	ReadTimeout      time.Duration
	// ApplyTimeout 是等 hub 回快照并完成 apply 的上限。0 → 10s。
	ApplyTimeout time.Duration
	Logger       *slog.Logger
}

// SyncReport 是一次立即同步的结果摘要，供 CLI 打印。
type SyncReport struct {
	HubURL   string
	Revision string
	Seq      uint32
	// Actions 是动作名 → 条数（skip / create / overwrite / delete / merge）。
	Actions  map[string]int
	Steps    []SyncStep
	OK       bool
	Error    string
	Writes   int
	Duration time.Duration
}

// SyncStep 是计划里的一步，供 CLI 按路径列出。
type SyncStep struct {
	Action string
	Path   string
}

// SyncOnce 连一条临时连接，拉取当前指派并 apply，然后退出。
//
// 它会短暂顶掉常驻 agent 的连接（同指纹新连接优先，见 M0 spec §6.4a）；
// 常驻进程随后自动重连。
func SyncOnce(ctx context.Context, o SyncOptions) (*SyncReport, error) {
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 70 * time.Second
	}
	if o.ApplyTimeout == 0 {
		o.ApplyTimeout = 10 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	cfg, err := LoadConfig(o.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("尚未 enroll：%s 下没有 %s，请先执行 orciny-agent enroll --hub ... --token ...",
				o.Dir, ConfigFileName)
		}
		return nil, err
	}
	managedHome, err := cfg.ManagedHomeDir()
	if err != nil {
		return nil, err
	}

	type result struct {
		ack  protocol.ApplyAck
		plan applier.Plan
	}
	done := make(chan result, 1)

	var sess *Session
	sync, err := syncer.New(syncer.Deps{
		Dir:         o.Dir,
		ManagedHome: managedHome,
		Clock:       clock.System(),
		Logger:      o.Logger,
		Send: func(kind protocol.Kind, payload any) error {
			if sess == nil {
				return errors.New("agent: 连接尚未建立")
			}
			return sess.Send(kind, payload)
		},
		OnApplied: func(ack protocol.ApplyAck, plan applier.Plan) {
			select {
			case done <- result{ack: ack, plan: plan}:
			default:
			}
		},
	})
	if err != nil {
		return nil, err
	}

	sess, err = Connect(ctx, ConnectOptions{
		Dir:              o.Dir,
		HubURL:           cfg.HubURL,
		HandshakeTimeout: o.HandshakeTimeout,
		ReadTimeout:      o.ReadTimeout,
		Logger:           o.Logger,
		OnMessage:        sync.Handle,
	})
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	if err := sync.SyncNow(); err != nil {
		return nil, err
	}

	timer := time.NewTimer(o.ApplyTimeout)
	defer timer.Stop()

	select {
	case r := <-done:
		return buildSyncReport(cfg.HubURL, r.ack, r.plan), nil
	case <-timer.C:
		// 超时前若本地已有状态，至少把当前版本信息带回去——
		// hub 无指派、survey 模式、或网络卡住时 OnApplied 都不会触发。
		rep := &SyncReport{HubURL: cfg.HubURL, OK: false, Error: "等待 apply 结果超时", Actions: map[string]int{}}
		if st := sync.State(); st != nil {
			rep.Revision = st.Revision
			rep.Seq = st.Seq
		}
		return rep, fmt.Errorf("等待 apply 结果超时（%s）", o.ApplyTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-sess.Done():
		if err := sess.Err(); err != nil {
			return nil, fmt.Errorf("连接在同步完成前断开: %w", err)
		}
		return nil, errors.New("连接在同步完成前断开")
	}
}

func buildSyncReport(hub string, ack protocol.ApplyAck, plan applier.Plan) *SyncReport {
	rep := &SyncReport{
		HubURL:   hub,
		Revision: ack.RevisionID,
		OK:       ack.OK,
		Error:    ack.Error,
		Writes:   plan.Writes(),
		Duration: time.Duration(ack.DurationMs) * time.Millisecond,
		Actions:  map[string]int{},
	}
	for _, s := range plan.Steps {
		name := actionName(s.Action)
		rep.Actions[name]++
		rep.Steps = append(rep.Steps, SyncStep{Action: name, Path: s.Rel})
	}
	// Skip 列表也算进统计（它们不在 Steps 里）。
	for _, sk := range plan.Skip {
		rep.Actions["skip"]++
		rep.Steps = append(rep.Steps, SyncStep{Action: "skip", Path: sk.Rel})
	}
	// Seq 不在 ApplyAck 里；CLI 打印时从本地 state 补也可，这里留 0。
	return rep
}

func actionName(a uint8) string {
	switch a {
	case protocol.ActionSkip:
		return "skip"
	case protocol.ActionCreate:
		return "create"
	case protocol.ActionOverwrite:
		return "overwrite"
	case protocol.ActionDelete:
		return "delete"
	case protocol.ActionMerge:
		return "merge"
	default:
		return fmt.Sprintf("action(%d)", a)
	}
}

// formatSyncReport 把报告打成用户可读的几行。
func formatSyncReport(r *SyncReport) string {
	var b []byte
	w := func(format string, args ...any) {
		b = append(b, fmt.Sprintf(format, args...)...)
	}
	w("已连接 hub（%s）\n", r.HubURL)
	if r.Revision != "" {
		w("拉取到版本")
		if r.Seq > 0 {
			w(" v%d", r.Seq)
		}
		w("（%s）\n", shortID(r.Revision))
	}
	if len(r.Steps) > 0 {
		w("计划：\n")
		for _, s := range r.Steps {
			w("  %-10s %s\n", s.Action, s.Path)
		}
	}
	total := 0
	for _, n := range r.Actions {
		total += n
	}
	if r.OK {
		w("应用完成：%d 条，%d 处改动", total, r.Writes)
		if r.Duration > 0 {
			w("，耗时 %s", r.Duration.Round(time.Millisecond))
		}
		w("\n")
	} else if r.Error != "" {
		w("应用失败：%s\n", r.Error)
	}
	return string(b)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}
