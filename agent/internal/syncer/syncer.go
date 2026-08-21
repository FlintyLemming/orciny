// Package syncer 是 agent 侧配置闭环的编排者。
//
// 单独成包的理由：ConfigNotify → Pull → 补 blob → apply → Ack 是一台状态机，
// 且要跨消息保存「还在等哪些 hash」。挂在 conn 里会让连接层长出业务；
// 挂在 applier 里会让它依赖网络。
package syncer

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/watcher"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/protocol"
)

type Deps struct {
	Dir               string // agent 目录（~/.orciny）
	ManagedHome       string
	Clock             clock.Clock
	Logger            *slog.Logger
	FS                applier.FS
	Send              func(kind protocol.Kind, payload any) error
	MachineName       string        // 面板上的备注名，供 {{machine.name}}
	ReconcileInterval time.Duration // 0 → watcher 默认 5m
	// OnApplied 在一次 apply（含失败回滚）完成后回调。CLI 的 sync 靠它
	// 拿到 plan 与回执；常驻进程不设。survey 模式不触发（没有 ApplyAck）。
	OnApplied func(ack protocol.ApplyAck, plan applier.Plan)
}

type Syncer struct {
	d     Deps
	log   *slog.Logger
	cache *blobcache.Cache
	app   *applier.Applier

	mu      sync.Mutex
	st      *state.State
	sec     *secrets.File
	watcher *watcher.Watcher
	pending *protocol.ConfigSnapshot // 正在等 blob 的快照
	waiting map[string]bool          // 还没到的 hash
	content map[string][]byte        // 本次已凑齐的内容
}

func New(d Deps) (*Syncer, error) {
	if d.Clock == nil {
		d.Clock = clock.System()
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.FS == nil {
		d.FS = applier.OSFS()
	}
	if d.ManagedHome == "" {
		return nil, fmt.Errorf("syncer: ManagedHome 不能为空")
	}

	s := &Syncer{
		d:     d,
		log:   d.Logger,
		cache: blobcache.New(d.Dir),
		app: applier.New(applier.Options{
			Dir: d.Dir, ManagedHome: d.ManagedHome,
			Clock: d.Clock, Logger: d.Logger, FS: d.FS,
		}),
	}
	// state.json 缺失或损坏都不是错误：spec §7.6 的自愈路径靠它触发。
	if st, err := state.Load(d.Dir); err == nil {
		s.st = st
	}
	return s, nil
}

// State 返回当前本地状态的副本视图（CLI 与 watcher 用）。
func (s *Syncer) State() *state.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Handle 分派一条来自 hub 的消息。
func (s *Syncer) Handle(env protocol.Envelope) {
	switch env.Kind {
	case protocol.KindConfigNotify:
		n, err := protocol.DecodePayload[protocol.ConfigNotify](env)
		if err != nil {
			s.log.Warn("解析 ConfigNotify 失败", "error", err)
			return
		}
		s.log.Info("收到配置变更通知",
			"config_set", n.ConfigSetID, "revision", n.RevisionID, "reason", n.Reason)
		s.pull()

	case protocol.KindConfigSnapshot:
		snap, err := protocol.DecodePayload[protocol.ConfigSnapshot](env)
		if err != nil {
			s.log.Warn("解析 ConfigSnapshot 失败", "error", err)
			return
		}
		s.onSnapshot(snap)

	case protocol.KindBlobData:
		bd, err := protocol.DecodePayload[protocol.BlobData](env)
		if err != nil {
			s.log.Warn("解析 BlobData 失败", "error", err)
			return
		}
		s.onBlob(bd)

	case protocol.KindCollectRequest:
		req, err := protocol.DecodePayload[protocol.CollectRequest](env)
		if err != nil {
			s.log.Warn("解析 CollectRequest 失败", "error", err)
			return
		}
		s.collect(req)

	case protocol.KindDriftCommand:
		cmd, err := protocol.DecodePayload[protocol.DriftCommand](env)
		if err != nil {
			s.log.Warn("解析 DriftCommand 失败", "error", err)
			return
		}
		s.onDriftCommand(cmd)
	}
}

// SyncNow 主动拉一次（CLI 的 orciny-agent sync）。
func (s *Syncer) SyncNow() error { return s.pull() }

func (s *Syncer) pull() error {
	have := ""
	s.mu.Lock()
	if s.st != nil {
		have = s.st.Revision
	}
	s.mu.Unlock()
	return s.d.Send(protocol.KindConfigPull, protocol.ConfigPull{Have: have})
}

func (s *Syncer) onSnapshot(snap protocol.ConfigSnapshot) {
	// 凭据与变量先落盘再说：还原、离线自愈、对账都依赖它，
	// 而且它与本次 apply 是否成功无关（spec §6.2）。
	if err := s.saveSecrets(snap); err != nil {
		s.log.Warn("保存凭据缓存失败", "error", err)
	}

	var need []string
	for _, f := range snap.Files {
		if !s.cache.Has(f.Hash) {
			need = append(need, f.Hash)
		}
	}
	need = s.cache.Missing(need) // 顺带去重

	s.mu.Lock()
	s.pending = &snap
	s.content = map[string][]byte{}
	s.waiting = map[string]bool{}
	for _, h := range need {
		s.waiting[h] = true
	}
	ready := len(s.waiting) == 0
	s.mu.Unlock()

	if ready {
		s.applyPending()
		return
	}
	if err := s.d.Send(protocol.KindBlobRequest, protocol.BlobRequest{Hashes: need}); err != nil {
		s.log.Warn("索取内容失败", "error", err)
	}
}

func (s *Syncer) onBlob(bd protocol.BlobData) {
	s.mu.Lock()
	if s.pending == nil || !s.waiting[bd.Hash] {
		s.mu.Unlock()
		return // 迟到的或不相干的，忽略
	}
	if bd.Missing {
		rev := s.pending.RevisionID
		s.pending, s.waiting, s.content = nil, nil, nil
		s.mu.Unlock()
		// hub 都找不到内容，本次彻底做不成——明确报回去，
		// 绝不带着半份清单去写盘（spec §7.4「宁可不动」）。
		s.log.Error("hub 找不到所需内容，已中止本次 apply", "hash", bd.Hash)
		ack := protocol.ApplyAck{
			RevisionID: rev,
			OK:         false,
			Error:      fmt.Sprintf("hub 缺少内容 %s，未做任何改动", bd.Hash),
		}
		_ = s.d.Send(protocol.KindApplyAck, ack)
		if s.d.OnApplied != nil {
			s.d.OnApplied(ack, applier.Plan{})
		}
		return
	}
	s.mu.Unlock()

	// 落盘前自己再算一遍 hash：这是 agent 少数能自我保护的地方。
	if err := s.cache.PutHash(bd.Hash, bd.Content); err != nil {
		s.log.Error("收到的内容与 hash 不符，已丢弃", "hash", bd.Hash, "error", err)
		return
	}

	s.mu.Lock()
	delete(s.waiting, bd.Hash)
	s.content[bd.Hash] = bd.Content
	ready := len(s.waiting) == 0
	s.mu.Unlock()

	if ready {
		s.applyPending()
	}
}

func (s *Syncer) applyPending() {
	s.mu.Lock()
	if s.pending == nil {
		s.mu.Unlock()
		return
	}
	snap := *s.pending
	st := s.st
	s.pending, s.waiting = nil, nil
	s.mu.Unlock()

	content, err := s.contentFor(snap)
	if err != nil {
		s.log.Error("凑齐内容失败", "error", err)
		// survey 没有 ApplyAck 通道可报；apply 才回执失败。
		if snap.Mode != protocol.ModeSurvey {
			ack := protocol.ApplyAck{RevisionID: snap.RevisionID, OK: false, Error: err.Error()}
			_ = s.d.Send(protocol.KindApplyAck, ack)
			if s.d.OnApplied != nil {
				s.d.OnApplied(ack, applier.Plan{})
			}
		}
		return
	}

	// survey 模式不写盘（spec §7.6）：只做一次全量对账，把所有差异作为
	// 漂移上报，由用户在收件箱里逐条决定收编还是恢复。
	if snap.Mode == protocol.ModeSurvey {
		s.survey(snap, content)
		return
	}

	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	plan, err := applier.BuildPlan(snap, st, content, sec.Lookup)
	if err != nil {
		s.log.Error("生成 apply 计划失败", "error", err)
		ack := protocol.ApplyAck{RevisionID: snap.RevisionID, OK: false, Error: err.Error()}
		_ = s.d.Send(protocol.KindApplyAck, ack)
		if s.d.OnApplied != nil {
			s.d.OnApplied(ack, applier.Plan{})
		}
		return
	}

	ack, next := s.app.Apply(snap, plan, st)
	for _, sk := range plan.Skip {
		ack.Results = append(ack.Results, protocol.ApplyResult{
			Path: sk.Rel, Action: protocol.ActionSkip, Error: sk.Reason,
		})
	}
	// Manifest 随成功的快照冻结：watcher 与恢复都要用它展开受管范围（spec §4.1）。
	if ack.OK && len(snap.Manifest) > 0 {
		next.Manifest = json.RawMessage(append([]byte(nil), snap.Manifest...))
	}
	if err := state.Save(s.d.Dir, next); err != nil {
		s.log.Error("写 state.json 失败", "error", err)
	}
	s.mu.Lock()
	s.st = next
	s.sec = sec
	s.reloadWatcherLocked()
	s.mu.Unlock()

	s.log.Info("apply 完成", "revision", snap.RevisionID,
		"ok", ack.OK, "writes", plan.Writes(), "rolled_back", ack.RolledBack)
	if err := s.d.Send(protocol.KindApplyAck, ack); err != nil {
		s.log.Warn("上报回执失败", "error", err)
	}
	if s.d.OnApplied != nil {
		s.d.OnApplied(ack, plan)
	}
}

// survey 用快照清单当基线做一次全量对账。
//
// 基线不能取 state.json ——本机可能根本没有（第一次指派、或者状态丢失）。
// 因此用快照造一份「虚拟 state」：Blob 填清单里的 hash，Rendered 留空。
// Rendered 留空是要紧的：填一个假值会让对账误判「一致」而漏报。
// Scan 在 Rendered 为空时会回退到 blob 渲染结果做比对，因此内容真的一致
// 时仍然安静。
//
// Health 写成 ok：degraded 解除后 hub 会下发 survey 快照，靠这条路径
// 顺手清掉本机的 degraded 标记，不需要额外的协议消息。
func (s *Syncer) survey(snap protocol.ConfigSnapshot, content map[string][]byte) {
	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	virtual := &state.State{
		ConfigSet: snap.ConfigSetID,
		Revision:  snap.RevisionID,
		Seq:       snap.Seq,
		Checksum:  snap.Checksum,
		Mode:      "survey",
		Health:    state.HealthOK,
		Files:     map[string]state.FileState{},
		Ignored:   snap.IgnorePaths,
		Manifest:  append(json.RawMessage(nil), snap.Manifest...),
		AppliedAt: s.d.Clock.Now().UTC(),
	}
	for _, f := range snap.Files {
		virtual.Files[f.Path] = state.FileState{
			Blob: f.Hash, Mode: f.Mode, Size: f.Size, Keys: f.Keys,
			// Rendered 故意留空：磁盘上从未写过这份内容。
		}
	}
	// 内容也进本地缓存：收编 / 恢复 / 转 apply 时都要用得到，
	// 而且它们可能发生在 hub 离线的时候。
	for hash, raw := range content {
		if err := s.cache.PutHash(hash, raw); err != nil {
			s.log.Warn("缓存基线内容失败", "hash", hash, "error", err)
		}
	}

	if err := state.Save(s.d.Dir, virtual); err != nil {
		s.log.Error("写 state.json 失败", "error", err)
	}
	s.mu.Lock()
	s.st = virtual
	s.sec = sec
	s.mu.Unlock()

	w, err := s.ensureWatcher()
	if err != nil {
		s.log.Error("创建 watcher 失败", "error", err)
		return
	}
	m, err := s.manifestOf(virtual)
	if err != nil {
		s.log.Error("解析 manifest 失败", "error", err)
		return
	}
	if err := w.Reload(virtual, m, sec); err != nil {
		s.log.Error("重载对账基线失败", "error", err)
		return
	}
	items, err := w.Scan(true)
	if err != nil {
		s.log.Error("全量对账失败", "error", err)
		return
	}
	s.log.Info("survey 全量对账完成", "revision", snap.RevisionID, "drifts", len(items))
	if err := s.Report(items, true); err != nil {
		s.log.Warn("上报对账结果失败", "error", err)
	}
}

// contentFor 从本次收到的内容与本地缓存里凑齐清单需要的全部 blob。
func (s *Syncer) contentFor(snap protocol.ConfigSnapshot) (map[string][]byte, error) {
	s.mu.Lock()
	got := s.content
	s.mu.Unlock()

	out := make(map[string][]byte, len(snap.Files))
	for _, f := range snap.Files {
		if b, ok := got[f.Hash]; ok {
			out[f.Hash] = b
			continue
		}
		b, err := s.cache.Get(f.Hash)
		if err != nil {
			return nil, fmt.Errorf("syncer: 缺少 %s 的内容（hash %s）", f.Path, f.Hash)
		}
		out[f.Hash] = b
	}
	return out, nil
}

// saveSecrets 把快照带来的凭据、变量与内置的 machine.* 一起落盘。
func (s *Syncer) saveSecrets(snap protocol.ConfigSnapshot) error {
	hostname, _ := os.Hostname()
	name := s.d.MachineName
	if name == "" {
		name = hostname
	}
	f := &secrets.File{
		Creds:    snap.Credentials,
		Vars:     snap.Variables,
		Provider: snap.Provider,
		Machine: map[string]string{
			"name":     name,
			"hostname": hostname,
			"os":       runtime.GOOS,
			"arch":     runtime.GOARCH,
		},
	}
	if f.Creds == nil {
		f.Creds = map[string]string{}
	}
	if f.Vars == nil {
		f.Vars = map[string]string{}
	}
	if f.Provider == nil {
		f.Provider = map[string]string{}
	}
	if err := secrets.Save(s.d.Dir, f); err != nil {
		return err
	}
	s.mu.Lock()
	s.sec = f
	s.mu.Unlock()
	return nil
}
