package revisions

import (
	"sort"

	"github.com/FlintyLemming/orciny/protocol"
)

// FileChange 是清单层面的一条差异。内容层面的 unified diff 另算
// （drift 包，spec §8.2）。
type FileChange struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"` // added / removed / modified
	FromHash string `json:"from_hash"`
	ToHash   string `json:"to_hash"`
}

// Diff 比两份清单。草稿与 revision 同形状，因此「草稿 vs head」与
// 「v3 vs v1」走的是同一个函数（spec §4.2）。
func Diff(from, to []protocol.FileEntry) []FileChange {
	index := func(fs []protocol.FileEntry) map[string]protocol.FileEntry {
		m := make(map[string]protocol.FileEntry, len(fs))
		for _, f := range fs {
			m[f.Path] = f
		}
		return m
	}
	a, b := index(from), index(to)

	var out []FileChange
	for path, nf := range b {
		of, ok := a[path]
		if !ok {
			out = append(out, FileChange{Path: path, Kind: "added", ToHash: nf.Hash})
			continue
		}
		// mode 变了也算改动：它进 checksum，也决定落盘权限。
		if of.Hash != nf.Hash || of.Mode != nf.Mode {
			out = append(out, FileChange{Path: path, Kind: "modified", FromHash: of.Hash, ToHash: nf.Hash})
		}
	}
	for path, of := range a {
		if _, ok := b[path]; !ok {
			out = append(out, FileChange{Path: path, Kind: "removed", FromHash: of.Hash})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
