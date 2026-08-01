package applier

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/FlintyLemming/orciny/internal/atomicfile"
	"github.com/FlintyLemming/orciny/protocol"
)

// SnapshotDirName 是快照根目录名，SnapshotKeep 是保留份数（spec §7.4）。
const (
	SnapshotDirName = "snapshots"
	SnapshotKeep    = 5
)

// snapshotManifest 记录每条路径的原始权限，回滚时按它还原。
type snapshotManifest struct {
	Revision string            `json:"revision"`
	Paths    map[string]uint32 `json:"paths"`  // rel → 原权限；不存在的路径值为 0
	Absent   []string          `json:"absent"` // apply 前本就不存在，回滚时要删掉
}

// takeSnapshot 把所有将被写/删的路径的当前内容原样存一份。
//
// 「原样」包含真实凭据值——快照是给回滚用的，必须能一字不差地还原现场。
// 因此目录 0700、文件 0600。
func (a *Applier) takeSnapshot(revision string, p Plan) (string, *snapshotManifest, error) {
	dir := filepath.Join(a.o.Dir, SnapshotDirName,
		fmt.Sprintf("%d-%s", a.o.Clock.Now().Unix(), shortID(revision)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("applier: 建快照目录: %w", err)
	}

	man := &snapshotManifest{Revision: revision, Paths: map[string]uint32{}}
	for _, s := range p.Steps {
		if s.Action == protocol.ActionSkip {
			continue
		}
		abs := filepath.Join(a.o.ManagedHome, filepath.FromSlash(s.Rel))
		info, err := a.fs.Stat(abs)
		if err != nil {
			man.Absent = append(man.Absent, s.Rel)
			continue
		}
		content, err := a.fs.Read(abs)
		if err != nil {
			return "", nil, fmt.Errorf("applier: 快照 %s: %w", s.Rel, err)
		}
		if err := atomicfile.Write(filepath.Join(dir, filepath.FromSlash(s.Rel)), content, 0o600); err != nil {
			return "", nil, fmt.Errorf("applier: 写快照 %s: %w", s.Rel, err)
		}
		man.Paths[s.Rel] = uint32(info.Mode().Perm())
	}

	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("applier: 序列化快照清单: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(dir, "manifest.json"), b, 0o600); err != nil {
		return "", nil, fmt.Errorf("applier: 写快照清单: %w", err)
	}
	a.pruneSnapshots()
	return dir, man, nil
}

// pruneSnapshots 只保留最近 SnapshotKeep 份。目录名以 unix 时间戳打头，
// 字典序即时间序。
func (a *Applier) pruneSnapshots() {
	root := filepath.Join(a.o.Dir, SnapshotDirName)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= SnapshotKeep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-SnapshotKeep] {
		if err := os.RemoveAll(filepath.Join(root, n)); err != nil {
			a.log.Warn("清理旧快照失败", "dir", n, "error", err)
		}
	}
}

func shortID(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	if s == "" {
		return "none"
	}
	return s
}
