package manifest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/protocol"
)

type Entry struct {
	Rel  string
	Abs  string
	Inc  Include
	Mode os.FileMode
}

type Skip struct {
	Rel    string
	Reason string // protocol.Skip* 之一
}

type Expansion struct {
	Files   []Entry // 按 Rel 升序
	Skipped []Skip  // 按 Rel 升序
}

// Expand 在真实文件系统上展开 manifest。root 是 managed home。
//
// 「不存在」不算跳过，只是没有：apply 的 create 动作由 revision 清单驱动，
// 不靠展开结果。
func (m Manifest) Expand(root string) (Expansion, error) {
	// 与 ResolveUnder 同一口径：macOS 上 t.TempDir() 是 /var/...，真身是
	// /private/var/...。WalkDir 走真身，Rel 必须也用真身，否则 tree 全空。
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Expansion{}, fmt.Errorf("manifest: 解析 root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}

	var exp Expansion
	seen := map[string]bool{}

	add := func(rel string, inc Include) {
		if seen[rel] {
			return
		}
		if IsAlwaysExcluded(rel) {
			seen[rel] = true
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipAlwaysExcluded})
			return
		}
		for _, ex := range m.Exclude {
			if MatchGlob(ex, rel) {
				seen[rel] = true
				return // 用户自己排除的，不必报给他看
			}
		}
		abs, err := ResolveUnder(rootAbs, rel)
		if err != nil {
			seen[rel] = true
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipUnreadable})
			return
		}
		// Lstat 而不是 Stat：符号链接本身就是要拒的东西，
		// 跟着它走反而会把 root 之外的内容读进来。
		info, err := os.Lstat(abs)
		if os.IsNotExist(err) {
			return
		}
		seen[rel] = true
		if err != nil {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipUnreadable})
			return
		}
		if !info.Mode().IsRegular() {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipNotRegular})
			return
		}
		if info.Size() > protocol.MaxFileSize {
			exp.Skipped = append(exp.Skipped, Skip{rel, protocol.SkipTooLarge})
			return
		}
		exp.Files = append(exp.Files, Entry{Rel: rel, Abs: abs, Inc: inc, Mode: info.Mode().Perm()})
	}

	for _, inc := range m.Include {
		switch inc.Mode {
		case ModeFile, ModeKeys:
			add(inc.Path, inc)

		case ModeTree:
			base := strings.TrimSuffix(inc.Path, "/**")
			absBase, err := ResolveUnder(rootAbs, base)
			if err != nil {
				return Expansion{}, fmt.Errorf("manifest: 展开 %s: %w", inc.Path, err)
			}
			werr := filepath.WalkDir(absBase, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil // 整棵子树不存在
					}
					return nil // 单个目录读不了：跳过，不让整次展开失败
				}
				if d.IsDir() {
					// 恒排除的目录整棵剪掉，省下遍历 node_modules 的开销。
					// 剪掉时记一条 Skip，否则调用方看不到「这里被恒排除了」。
					rel, rerr := filepath.Rel(rootAbs, p)
					if rerr == nil {
						relSlash := filepath.ToSlash(rel)
						if IsAlwaysExcluded(relSlash + "/x") {
							if !seen[relSlash] {
								seen[relSlash] = true
								exp.Skipped = append(exp.Skipped, Skip{relSlash, protocol.SkipAlwaysExcluded})
							}
							return fs.SkipDir
						}
					}
					return nil
				}
				rel, rerr := filepath.Rel(rootAbs, p)
				if rerr != nil {
					return nil
				}
				add(filepath.ToSlash(rel), inc)
				return nil
			})
			if werr != nil {
				return Expansion{}, fmt.Errorf("manifest: 遍历 %s: %w", inc.Path, werr)
			}
		}
	}

	sort.Slice(exp.Files, func(i, j int) bool { return exp.Files[i].Rel < exp.Files[j].Rel })
	sort.Slice(exp.Skipped, func(i, j int) bool { return exp.Skipped[i].Rel < exp.Skipped[j].Rel })
	return exp, nil
}
