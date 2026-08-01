package syncer

import (
	"context"

	"github.com/FlintyLemming/orciny/agent/internal/applier"
	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/secrets"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/agent/internal/watcher"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Report 上报漂移，按累计 256 KiB 分批，最后一批置 Final（spec §5.4）。
//
// Full 标记贯穿每一批：hub 用它区分「增量上报」与「survey 的全量对账」，
// 后者要把没出现在报告里的旧漂移标记为已解决。
func (s *Syncer) Report(items []protocol.DriftItem, full bool) error {
	if len(items) == 0 {
		return nil
	}
	var (
		batch []protocol.DriftItem
		size  int
	)
	flush := func(final bool) error {
		err := s.d.Send(protocol.KindDriftReport, protocol.DriftReport{
			Items: batch, Final: final, Full: full,
		})
		batch, size = nil, 0
		return err
	}
	for _, it := range items {
		if size+len(it.Content) > protocol.MaxBatchSize && len(batch) > 0 {
			if err := flush(false); err != nil {
				return err
			}
		}
		batch = append(batch, it)
		size += len(it.Content)
	}
	return flush(true)
}

// StartWatcher 起文件监视与定时对账。ctx 取消时干净退出。
func (s *Syncer) StartWatcher(ctx context.Context) error {
	w, err := s.ensureWatcher()
	if err != nil {
		return err
	}
	return w.Run(ctx)
}

// ReconcileNow 立即做一次全量对账并上报（CLI 的 sync / drift 用）。
func (s *Syncer) ReconcileNow() error {
	w, err := s.ensureWatcher()
	if err != nil {
		return err
	}
	s.mu.Lock()
	st, sec := s.st, s.sec
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	m, err := s.manifestOf(st)
	if err != nil {
		return err
	}
	if err := w.Reload(st, m, sec); err != nil {
		return err
	}
	items, err := w.Scan(true)
	if err != nil {
		return err
	}
	return s.Report(items, true)
}

// ensureWatcher 懒创建 watcher。Scan / Reload 不依赖 Run，
// 因此 CLI 与单测的 ReconcileNow 不必先起主循环。
func (s *Syncer) ensureWatcher() (*watcher.Watcher, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher != nil {
		return s.watcher, nil
	}
	w, err := watcher.New(watcher.Options{
		Dir:         s.d.Dir,
		ManagedHome: s.d.ManagedHome,
		Clock:       s.d.Clock,
		Reconcile:   s.d.ReconcileInterval,
		Logger:      s.log,
		Report:      s.Report,
	})
	if err != nil {
		return nil, err
	}
	s.watcher = w
	if s.st != nil {
		if m, err := s.manifestOf(s.st); err == nil {
			_ = w.Reload(s.st, m, s.sec)
		}
	}
	return w, nil
}

// onDriftCommand 处理 hub 下达的恢复 / 忽略（spec §8.4）。
func (s *Syncer) onDriftCommand(cmd protocol.DriftCommand) {
	switch cmd.Op {
	case protocol.OpIgnore:
		// 忽略只改本地视图，不碰文件。持久化靠下一份快照带下来的
		// IgnorePaths——真相在 hub 的 ignore_rules 里。
		s.mu.Lock()
		if s.st != nil {
			s.st.Ignored = append(append([]string{}, s.st.Ignored...), cmd.Paths...)
			_ = state.Save(s.d.Dir, s.st)
		}
		st, sec := s.st, s.sec
		w := s.watcher
		s.mu.Unlock()
		if w != nil && st != nil {
			if m, err := s.manifestOf(st); err == nil {
				_ = w.Reload(st, m, sec)
			}
		}

	case protocol.OpRestore:
		s.restore(cmd.Paths)

	default:
		s.log.Warn("未知的漂移指令", "op", cmd.Op)
	}
}

// restore 按 state.json 基线重写这些路径。
//
// 走的是完整的 apply 流程（快照 + 原子写 + 失败回滚）：恢复和 apply 一样
// 会写用户文件，不该有第二条更宽松的路径（spec §8.4）。
func (s *Syncer) restore(paths []string) {
	s.mu.Lock()
	st := s.st
	s.mu.Unlock()
	if st == nil {
		s.log.Warn("本机没有基线，无法恢复")
		return
	}

	sec, err := secrets.Load(s.d.Dir)
	if err != nil {
		s.log.Error("读取凭据缓存失败", "error", err)
		return
	}

	// 用一份「只含这些路径」的伪快照驱动 applier，复用它的全部保障。
	sub := protocol.ConfigSnapshot{
		ConfigSetID: st.ConfigSet, RevisionID: st.Revision, Seq: st.Seq,
	}
	content := map[string][]byte{}
	cache := blobcache.New(s.d.Dir)
	subState := &state.State{Files: map[string]state.FileState{}, Health: st.Health}

	for _, p := range paths {
		fs, inBaseline := st.Files[p]
		if !inBaseline {
			// 基线里没有 = 这是本地新增的文件，恢复即删除。
			// 交给 applier 的 delete 动作：它会先快照再删，可回滚。
			subState.Files[p] = state.FileState{Rendered: "无", Mode: 0o644}
			continue
		}
		raw, err := cache.Get(fs.Blob)
		if err != nil {
			s.log.Warn("本地缺少基线内容，跳过恢复", "path", p, "hash", fs.Blob)
			continue
		}
		content[fs.Blob] = raw
		sub.Files = append(sub.Files, protocol.FileEntry{
			Path: p, Hash: fs.Blob, Size: fs.Size, Mode: fs.Mode, Keys: fs.Keys,
		})
		// 基线状态里故意填一个对不上的 rendered，逼 applier 走 overwrite
		// ——磁盘上此刻是用户改过的内容，必须被盖掉。
		subState.Files[p] = state.FileState{Blob: fs.Blob, Rendered: "陈旧", Mode: fs.Mode, Keys: fs.Keys}
	}
	sub.Checksum = protocol.Checksum(sub.Files)

	plan, err := applier.BuildPlan(sub, subState, content, sec.Lookup)
	if err != nil {
		s.log.Error("生成恢复计划失败", "error", err)
		return
	}
	ack, _ := s.app.Apply(sub, plan, subState)
	ack.RevisionID = st.Revision

	// 恢复不改变 state 的版本信息，只需把这几条的 rendered 拉回基线值。
	s.mu.Lock()
	for _, step := range plan.Steps {
		if step.Action == protocol.ActionDelete {
			delete(s.st.Files, step.Rel)
			continue
		}
		if fs, ok := s.st.Files[step.Rel]; ok {
			fs.Rendered = step.Rendered
			s.st.Files[step.Rel] = fs
		}
	}
	_ = state.Save(s.d.Dir, s.st)
	s.sec = sec
	st2, sec2, w := s.st, s.sec, s.watcher
	s.mu.Unlock()

	if w != nil && st2 != nil {
		if m, err := s.manifestOf(st2); err == nil {
			_ = w.Reload(st2, m, sec2)
		}
	}
	if err := s.d.Send(protocol.KindApplyAck, ack); err != nil {
		s.log.Warn("上报恢复回执失败", "error", err)
	}
}

// manifestOf 取当前生效的 manifest。它随快照冻结在 state 里
// ——历史版本要能被解释（spec §4.1）。
func (s *Syncer) manifestOf(st *state.State) (manifest.Manifest, error) {
	if len(st.Manifest) == 0 {
		return manifest.Default(), nil
	}
	return manifest.Parse(st.Manifest)
}

// reloadWatcher 在基线变化后把 watcher 的视图同步过来。
func (s *Syncer) reloadWatcherLocked() {
	if s.watcher == nil || s.st == nil {
		return
	}
	if m, err := s.manifestOf(s.st); err == nil {
		_ = s.watcher.Reload(s.st, m, s.sec)
	}
}
