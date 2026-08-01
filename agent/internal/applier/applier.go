package applier

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/internal/clock"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// FS 是 applier 对文件系统的全部需求。做成接口只有一个理由：
// 「注入一个写失败」是唯一能可靠测到回滚路径的办法，而回滚正是
// 这个包存在的意义。
type FS interface {
	Write(path string, data []byte, perm os.FileMode) error
	Read(path string) ([]byte, error)
	Remove(path string) error
	Stat(path string) (os.FileInfo, error)
}

type osFS struct{}

func OSFS() FS { return osFS{} }

func (osFS) Write(path string, data []byte, perm os.FileMode) error {
	return atomicfile.Write(path, data, perm)
}
func (osFS) Read(path string) ([]byte, error)      { return os.ReadFile(path) }
func (osFS) Remove(path string) error              { return os.Remove(path) }
func (osFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

type Options struct {
	Dir         string // agent 目录（~/.orciny）
	ManagedHome string
	Clock       clock.Clock
	Logger      *slog.Logger
	FS          FS
}

type Applier struct {
	o   Options
	fs  FS
	log *slog.Logger
}

func New(o Options) *Applier {
	if o.Clock == nil {
		o.Clock = clock.System()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.FS == nil {
		o.FS = OSFS()
	}
	return &Applier{o: o, fs: o.FS, log: o.Logger}
}

// Apply 执行计划。返回回执与**下一份** state（调用方负责落盘）。
func (a *Applier) Apply(snap protocol.ConfigSnapshot, p Plan, st *state.State) (protocol.ApplyAck, *state.State) {
	return a.ApplyWithHook(snap, p, st, nil)
}

// ApplyWithHook 与 Apply 相同，额外在每一步写盘成功后调用 hook。
// hook 只有测试会传（用来在中途改变文件系统的行为）。
func (a *Applier) ApplyWithHook(
	snap protocol.ConfigSnapshot, p Plan, st *state.State,
	hook func(rel string, done int),
) (protocol.ApplyAck, *state.State) {
	start := a.o.Clock.Now()
	ack := protocol.ApplyAck{RevisionID: snap.RevisionID}

	next := cloneState(st)
	if next == nil {
		next = &state.State{Files: map[string]state.FileState{}, Health: state.HealthOK}
	}
	if next.Health == "" {
		next.Health = state.HealthOK
	}

	// degraded 是终态，只有人工解除才出来（spec §7.4 第 2 条）：
	// 一次写坏之后继续按新版本去写，只会把现场破坏得更彻底。
	if next.Health == state.HealthDegraded {
		ack.Error = "本机处于 degraded 状态，已停止自动 apply，请在 Web 上确认解除"
		return ack, next
	}
	if next.Paused {
		ack.Error = "本机已暂停配置管理（orciny-agent pause）"
		return ack, next
	}

	snapDir, man, err := a.takeSnapshot(snap.RevisionID, p)
	if err != nil {
		ack.Error = err.Error()
		return ack, next
	}

	// applied 记录本次真的改动过的路径，失败时按逆序还原。
	var applied []string
	for _, s := range p.Steps {
		res := protocol.ApplyResult{Path: s.Rel, Action: s.Action}
		if s.Action == protocol.ActionSkip {
			ack.Results = append(ack.Results, res)
			continue
		}

		abs, perr := manifest.ResolveUnder(a.o.ManagedHome, s.Rel)
		if perr != nil {
			res.Error = perr.Error()
			ack.Results = append(ack.Results, res)
			return a.fail(ack, next, snapDir, man, applied, start, perr)
		}

		var werr error
		switch s.Action {
		case protocol.ActionCreate, protocol.ActionOverwrite:
			werr = a.fs.Write(abs, s.Content, s.Mode)
		case protocol.ActionDelete:
			werr = a.fs.Remove(abs)
			if os.IsNotExist(werr) {
				werr = nil // 已经不在了，正是想要的结果
			}
		case protocol.ActionMerge:
			werr = a.merge(abs, s)
		}
		if werr != nil {
			res.Error = werr.Error()
			ack.Results = append(ack.Results, res)
			return a.fail(ack, next, snapDir, man, applied, start, werr)
		}

		applied = append(applied, s.Rel)
		ack.Results = append(ack.Results, res)
		if hook != nil {
			hook(s.Rel, len(applied))
		}

		if s.Action == protocol.ActionDelete {
			delete(next.Files, s.Rel)
			continue
		}
		next.Files[s.Rel] = state.FileState{
			Blob:     s.Blob,
			Rendered: a.renderedOnDisk(abs, s),
			Mode:     uint32(s.Mode),
			Size:     uint32(len(s.Content)),
			Keys:     s.Keys,
		}
	}

	ack.OK = true
	ack.DurationMs = uint32(a.o.Clock.Now().Sub(start) / time.Millisecond)
	next.ConfigSet = snap.ConfigSetID
	next.Revision = snap.RevisionID
	next.Seq = snap.Seq
	next.Checksum = snap.Checksum
	next.AppliedAt = a.o.Clock.Now().UTC()
	next.Mode = modeName(snap.Mode)
	next.Health = state.HealthOK
	next.Ignored = snap.IgnorePaths
	return ack, next
}

// fail 走回滚路径。逆序还原是必须的：后写的可能依赖先写的（同一目录），
// 逆序还原让中间态存在的时间最短。
func (a *Applier) fail(
	ack protocol.ApplyAck, next *state.State,
	snapDir string, man *snapshotManifest, applied []string,
	start time.Time, cause error,
) (protocol.ApplyAck, *state.State) {
	ack.OK = false
	ack.Error = cause.Error()
	ack.DurationMs = uint32(a.o.Clock.Now().Sub(start) / time.Millisecond)

	if err := a.rollback(snapDir, man, applied); err != nil {
		// 第二级处置：还原都失败了，进 degraded 并停止自动 apply。
		a.log.Error("回滚失败，进入 degraded", "error", err, "cause", cause)
		ack.RolledBack = false
		next.Health = state.HealthDegraded
		return ack, next
	}
	a.log.Warn("apply 失败，已回滚到 apply 前状态", "error", cause)
	ack.RolledBack = true
	return ack, next
}

// rollback 逆序还原 applied 里的每一条。
func (a *Applier) rollback(snapDir string, man *snapshotManifest, applied []string) error {
	absent := map[string]bool{}
	for _, rel := range man.Absent {
		absent[rel] = true
	}
	for i := len(applied) - 1; i >= 0; i-- {
		rel := applied[i]
		abs := filepath.Join(a.o.ManagedHome, filepath.FromSlash(rel))
		if absent[rel] {
			if err := a.fs.Remove(abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("删除 %s: %w", rel, err)
			}
			continue
		}
		content, err := a.fs.Read(filepath.Join(snapDir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("读快照 %s: %w", rel, err)
		}
		perm := os.FileMode(man.Paths[rel])
		if perm == 0 {
			perm = 0o644
		}
		if err := a.fs.Write(abs, content, perm); err != nil {
			return fmt.Errorf("还原 %s: %w", rel, err)
		}
	}
	return nil
}

// renderedOnDisk 取落盘后的实际内容 hash。
// merge 之后磁盘内容不等于 s.Content（还有未受管的键），必须重读。
func (a *Applier) renderedOnDisk(abs string, s Step) string {
	if s.Action != protocol.ActionMerge {
		return s.Rendered
	}
	b, err := a.fs.Read(abs)
	if err != nil {
		return ""
	}
	return blobcache.Hash(b)
}

func cloneState(st *state.State) *state.State {
	if st == nil {
		return nil
	}
	c := *st
	c.Files = make(map[string]state.FileState, len(st.Files))
	for k, v := range st.Files {
		c.Files[k] = v
	}
	return &c
}

func modeName(m uint8) string {
	if m == protocol.ModeSurvey {
		return "survey"
	}
	return "apply"
}
