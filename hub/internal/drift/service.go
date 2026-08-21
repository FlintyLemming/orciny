package drift

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/blobs"
	"github.com/FlintyLemming/orciny/hub/internal/configsets"
	"github.com/FlintyLemming/orciny/hub/internal/configsync"
	"github.com/FlintyLemming/orciny/hub/internal/credentials"
	"github.com/FlintyLemming/orciny/hub/internal/events"
	"github.com/FlintyLemming/orciny/hub/internal/providers"
	"github.com/FlintyLemming/orciny/hub/internal/revisions"
	"github.com/FlintyLemming/orciny/protocol"
)

// Deps 是 drift.Service 的全部外部依赖。
type Deps struct {
	App    core.App
	Blobs  *blobs.Store
	Sets   *configsets.Service
	Revs   *revisions.Service
	Events *events.Writer
	Sync   *configsync.Service // 子计划 14 发 DriftCommand 用；HandleReport 暂不需要
	// Providers 供绑定漂移的反查（M1.5 spec §6.3）。
	Providers *providers.Store
	// Creds 供把机器上手写的 key 抽成凭据（M1.5 spec §6.3 第二档）。
	Creds  *credentials.Store
	Logger *slog.Logger
}

// Service 是收件箱：漂移落库与后续的收编 / 恢复 / 忽略。
type Service struct {
	d   Deps
	log *slog.Logger

	// fullSeen 跨批累积本轮 Full 对账已见路径。Final 到达时取出并清空。
	// pendingRestore 记录刚下过 restore 指令的路径，ApplyAck 时
	// MarkRestored 只认这些路径，避免普通 apply 误标 restored。
	mu             sync.Mutex
	fullSeen       map[string][]string
	pendingRestore map[string]map[string]bool // machineID → paths
}

// NewService 构造 drift 服务。
func NewService(d Deps) *Service {
	log := d.Logger
	if log == nil {
		if d.App != nil {
			log = d.App.Logger()
		} else {
			log = slog.Default()
		}
	}
	return &Service{
		d:              d,
		log:            log,
		fullSeen:       map[string][]string{},
		pendingRestore: map[string]map[string]bool{},
	}
}

// HandleReport 落一批漂移上报（spec §8.2）。
//
// 同一路径同一时刻只能有一条待处理漂移，由 (machine, path) where state='open'
// 的部分唯一索引强制。重复检测到就更新那条，不新建（spec §4.1）。
func (s *Service) HandleReport(machineID string, rep protocol.DriftReport) error {
	assign, err := s.d.Sets.Assignment(machineID)
	if err != nil {
		// 没有配置集就无从收编。这不是错误——机器可能刚被解除指派。
		s.log.Debug("机器未指派配置集，忽略漂移上报", "machine", machineID)
		return nil
	}
	setID := assign.GetString("config_set")

	baseFiles, err := s.baselineFiles(setID)
	if err != nil {
		return err
	}

	seen := make([]string, 0, len(rep.Items))
	for _, it := range rep.Items {
		if err := s.upsert(machineID, setID, it, baseFiles); err != nil {
			s.log.Warn("落库漂移失败", "machine", machineID, "path", it.Path, "error", err)
			continue
		}
		seen = append(seen, it.Path)
	}

	// Full 的最后一批到齐后，把没出现在报告里的 open 漂移关掉——
	// 说明用户已经手工把它们改回去了。增量报告不做这件事：它只知道
	// 自己看见的那几条。
	if rep.Full {
		all := s.accumulateFull(machineID, seen)
		if rep.Final {
			s.clearFull(machineID)
			if err := s.closeVanished(machineID, all); err != nil {
				return err
			}
		}
	}

	if len(seen) > 0 {
		if err := s.d.Events.Write(events.KindDriftReported, machineID, map[string]any{
			"count": len(seen), "full": rep.Full,
		}); err != nil {
			s.log.Warn("写 drift.reported 事件失败", "error", err)
		}
	}
	return nil
}

// IgnorePaths 汇总全局规则与该机器的规则（spec §8.4）。
func (s *Service) IgnorePaths(machineID string) ([]string, error) {
	recs, err := s.d.App.FindRecordsByFilter("ignore_rules",
		"machine = '' || machine = {:m}", "path", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("drift: 读取忽略规则: %w", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.GetString("path"))
	}
	return out, nil
}

func (s *Service) upsert(machineID, setID string, it protocol.DriftItem, baseFiles map[string]string) error {
	col, err := s.d.App.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return fmt.Errorf("drift: 找不到 collection: %w", err)
	}

	recs, err := s.d.App.FindRecordsByFilter("drift_events",
		"machine = {:m} && path = {:p} && state = 'open'", "", 1, 0,
		map[string]any{"m": machineID, "p": it.Path})
	if err != nil {
		return fmt.Errorf("drift: 查询已有 open 漂移: %w", err)
	}

	var rec *core.Record
	if len(recs) > 0 {
		rec = recs[0]
	} else {
		rec = core.NewRecord(col)
		rec.Set("machine", machineID)
		rec.Set("path", it.Path)
		rec.Set("state", "open")
	}

	rec.Set("config_set", setID)
	rec.Set("kind", kindName(it.Kind))
	rec.Set("mode", it.Mode)
	rec.Set("restore_partial", it.RestorePartial)
	rec.Set("truncated", it.Truncated)

	baseHash := it.BaseHash
	if baseHash == "" {
		baseHash = baseFiles[it.Path]
	}
	if it.Kind == protocol.DriftAdded {
		baseHash = ""
	}
	rec.Set("base_hash", baseHash)

	var baseContent, curContent []byte
	if h := baseFiles[it.Path]; h != "" {
		if b, err := s.d.Blobs.Get(h); err == nil {
			baseContent = b
		}
	}

	if it.Truncated {
		rec.Set("current_blob", "")
		rec.Set("diff", "该文件含未能安全脱敏的凭据，请在 Web 上手工处理")
	} else if it.Kind == protocol.DriftDeleted {
		rec.Set("current_blob", "")
		rec.Set("diff", UnifiedDiff(it.Path, baseContent, nil))
	} else {
		curContent = it.Content
		if len(it.Content) > 0 {
			blobID, err := s.putBlob(it.Content)
			if err != nil {
				return err
			}
			rec.Set("current_blob", blobID)
		} else {
			rec.Set("current_blob", "")
		}
		rec.Set("diff", UnifiedDiff(it.Path, baseContent, curContent))
	}

	// 绑定漂移的识别与反查素材（M1.5 spec §6.1 / §6.3）。
	// 每次上报都重算：用户把那一行改回去之后标记必须跟着消失，
	// 否则「收编」会一直被置灰。
	bindingURL, isBindingDrift := "", false
	if !it.Truncated && it.Kind != protocol.DriftDeleted {
		bindingURL, isBindingDrift = DetectBindingDrift(baseContent, curContent)
	}
	rec.Set("binding_drift", isBindingDrift)
	rec.Set("binding_url", bindingURL)

	if err := s.d.App.Save(rec); err != nil {
		return fmt.Errorf("drift: 保存 %s: %w", it.Path, err)
	}
	return nil
}

// putBlob 写入内容并返回 blob 记录 id（RelationField 要 id 不要 hash）。
func (s *Service) putBlob(content []byte) (string, error) {
	hash, err := s.d.Blobs.Put(content)
	if err != nil {
		return "", err
	}
	r, err := s.d.App.FindFirstRecordByData("blobs", "hash", hash)
	if err != nil || r == nil {
		return "", fmt.Errorf("drift: 写入后找不到 blob %s: %w", hash, err)
	}
	return r.Id, nil
}

func (s *Service) baselineFiles(setID string) (map[string]string, error) {
	head, err := s.d.Revs.Head(setID)
	if err != nil {
		// 尚未发布：没有基线可比，按空基线处理（全部算 added）。
		return map[string]string{}, nil
	}
	files, err := s.d.Revs.Files(head.Id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(files))
	for _, f := range files {
		out[f.Path] = f.Hash
	}
	return out, nil
}

func (s *Service) closeVanished(machineID string, seen []string) error {
	keep := make(map[string]bool, len(seen))
	for _, p := range seen {
		keep[p] = true
	}
	recs, err := s.d.App.FindRecordsByFilter("drift_events",
		"machine = {:m} && state = 'open'", "", 0, 0,
		map[string]any{"m": machineID})
	if err != nil {
		return fmt.Errorf("drift: 查询 open 漂移: %w", err)
	}
	for _, rec := range recs {
		if keep[rec.GetString("path")] {
			continue
		}
		// 全量对账里消失 = 用户已手工改回基线，不再待处理。
		rec.Set("state", "superseded")
		if err := s.d.App.Save(rec); err != nil {
			return fmt.Errorf("drift: 关闭已消失漂移 %s: %w", rec.GetString("path"), err)
		}
	}
	return nil
}

func (s *Service) accumulateFull(machineID string, seen []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fullSeen[machineID] = append(s.fullSeen[machineID], seen...)
	out := make([]string, len(s.fullSeen[machineID]))
	copy(out, s.fullSeen[machineID])
	return out
}

func (s *Service) clearFull(machineID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fullSeen, machineID)
}

func kindName(k uint8) string {
	switch k {
	case protocol.DriftAdded:
		return "added"
	case protocol.DriftDeleted:
		return "deleted"
	default:
		return "modified"
	}
}
