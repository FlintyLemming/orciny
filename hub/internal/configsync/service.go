// Package configsync 是下发编排：谁该收到通知、快照怎么组装、回执怎么落库。
//
// 形状是 M0 就定下的（产品 §3.3）：hub 只发信号，agent 主动拉。M1 保持它，
// 多一个理由——凭据轮换可以复用同一条 ConfigNotify，不产生新 Revision
// （spec §5.1）。
package configsync

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

// Sender 是「往某台机器发一条消息」的能力，由 *machines.Manager 实现。
type Sender interface {
	SendTo(machineID string, kind protocol.Kind, payload any) error
	Online(machineID string) bool
}

// DriftHandler 是 configsync 对收件箱的全部需求。由 *drift.Service 实现。
// 做成接口避免 configsync 与 drift 互相 import（drift 要 *configsync.Service 发指令）。
type DriftHandler interface {
	HandleReport(machineID string, rep protocol.DriftReport) error
	IgnorePaths(machineID string) ([]string, error)
	Supersede(machineID string, paths []string, revID string) error
	MarkRestored(machineID string, paths []string) error
}

type Deps struct {
	App    core.App
	Blobs  *blobs.Store
	Sets   *configsets.Service
	Revs   *revisions.Service
	Creds  *credentials.Store
	Events *events.Writer
	Sender Sender
	Logger *slog.Logger
	// Importer 处理采集结果。configsync 只做转交——把它做成接口而不是
	// 直接依赖 importer 包，是因为 importer 也要 Sender，直接互相 import 会成环。
	Importer interface {
		HandleResult(machineID string, r protocol.CollectResult) error
	}
	// Drift 处理漂移上报。两步装配：先建 configsync（此字段空），再建 drift，
	// 最后 SetDrift（drift 构造时需要 *configsync.Service）。
	Drift DriftHandler
}

type Service struct {
	d   Deps
	log *slog.Logger
}

// SetDrift 在装配阶段把收件箱接上。必须在任何 agent 消息到达之前调用。
func (s *Service) SetDrift(d DriftHandler) { s.d.Drift = d }

// SendTo 把任意协议消息发给指定机器。drift 发 DriftCommand 走这里，
// 避免 drift 直接依赖 machines 包。
func (s *Service) SendTo(machineID string, kind protocol.Kind, payload any) error {
	if s.d.Sender == nil {
		return fmt.Errorf("configsync: sender 未配置")
	}
	return s.d.Sender.SendTo(machineID, kind, payload)
}

func NewService(d Deps) *Service {
	log := d.Logger
	if log == nil {
		if d.App != nil {
			log = d.App.Logger()
		} else {
			log = slog.Default()
		}
	}
	return &Service{d: d, log: log}
}

// Snapshot 组装一台机器当前该有的完整快照。
func (s *Service) Snapshot(machineID string) (protocol.ConfigSnapshot, error) {
	var snap protocol.ConfigSnapshot

	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		return snap, err
	}
	setID := assign.GetString("config_set")

	head, err := s.d.Revs.Head(setID)
	if err != nil {
		return snap, err
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return snap, err
	}

	var refs configsets.Refs
	if err := head.UnmarshalJSONField("refs", &refs); err != nil {
		return snap, fmt.Errorf("configsync: 解析 refs: %w", err)
	}
	// 只发这个 Revision 实际引用到的凭据（spec §5.3）：最小权限，
	// 也是一条实际的防线——一台被攻陷的机器不该因为连着 hub
	// 就拿到所有订阅的 key。
	creds, err := s.d.Creds.Values(refs.Creds)
	if err != nil {
		return snap, err
	}
	allVars, err := s.d.Creds.MachineVariables(machineID)
	if err != nil {
		return snap, err
	}
	vars := make(map[string]string, len(refs.Vars))
	for _, name := range refs.Vars {
		if v, ok := allVars[name]; ok {
			vars[name] = v
		}
	}

	ignore, err := s.ignorePaths(machineID)
	if err != nil {
		return snap, err
	}

	mode := protocol.ModeApply
	if assign.GetString("mode") == configsets.ModeSurvey {
		mode = protocol.ModeSurvey
	}

	return protocol.ConfigSnapshot{
		ConfigSetID: setID,
		RevisionID:  head.Id,
		Seq:         uint32(head.GetInt("seq")),
		Manifest:    []byte(head.GetString("manifest")),
		Files:       files,
		Checksum:    head.GetString("checksum"),
		Credentials: creds,
		Variables:   vars,
		IgnorePaths: ignore,
		Mode:        mode,
	}, nil
}

// ignorePaths 汇总全局规则与该机器的规则（spec §8.4）。
// 真相在 drift.IgnorePaths；未装配 drift 时退回本地查询（单测用）。
func (s *Service) ignorePaths(machineID string) ([]string, error) {
	if s.d.Drift != nil {
		return s.d.Drift.IgnorePaths(machineID)
	}
	recs, err := s.d.App.FindRecordsByFilter("ignore_rules",
		"machine = '' || machine = {:m}", "path", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("configsync: 读取忽略规则: %w", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("path"))
	}
	return out, nil
}

// —— ws.AgentMessages 实现 ——

func (s *Service) Pull(machineID string, p protocol.ConfigPull) {
	snap, err := s.Snapshot(machineID)
	if err != nil {
		if errors.Is(err, configsets.ErrNoAssignment) {
			s.log.Debug("机器未指派配置集，忽略拉取", "machine", machineID)
			return
		}
		s.log.Warn("组装快照失败", "machine", machineID, "error", err)
		return
	}
	s.log.Info("下发快照", "machine", machineID,
		"revision", snap.RevisionID, "have", p.Have, "files", len(snap.Files))
	if err := s.d.Sender.SendTo(machineID, protocol.KindConfigSnapshot, snap); err != nil {
		s.log.Warn("下发快照失败", "machine", machineID, "error", err)
	}
}

// BlobRequest 一条消息回一个 blob（spec §5.4）：512 KiB 的单文件上限
// 保证它不会越过 1 MiB 的 WS 帧上限。
func (s *Service) BlobRequest(machineID string, r protocol.BlobRequest) {
	for _, h := range r.Hashes {
		content, err := s.d.Blobs.Get(h)
		if err != nil {
			s.log.Warn("agent 索取的 blob 不存在", "machine", machineID, "hash", h)
			_ = s.d.Sender.SendTo(machineID, protocol.KindBlobData,
				protocol.BlobData{Hash: h, Missing: true})
			continue
		}
		if err := s.d.Sender.SendTo(machineID, protocol.KindBlobData,
			protocol.BlobData{Hash: h, Content: content}); err != nil {
			s.log.Warn("发送 blob 失败", "machine", machineID, "hash", h, "error", err)
			return
		}
	}
}

// DriftReport 转交给收件箱。未装配时只记日志——开发期单测可能不接 drift。
func (s *Service) DriftReport(machineID string, rep protocol.DriftReport) {
	if s.d.Drift == nil {
		s.log.Debug("未装配漂移服务，忽略上报", "machine", machineID)
		return
	}
	if err := s.d.Drift.HandleReport(machineID, rep); err != nil {
		s.log.Warn("处理漂移上报失败", "machine", machineID, "error", err)
	}
}

func (s *Service) CollectResult(machineID string, r protocol.CollectResult) {
	if s.d.Importer == nil {
		s.log.Debug("未装配导入服务，忽略采集结果", "machine", machineID)
		return
	}
	if err := s.d.Importer.HandleResult(machineID, r); err != nil {
		s.log.Warn("处理采集结果失败", "machine", machineID, "error", err)
	}
}
