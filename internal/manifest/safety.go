package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafeRelPath 校验一条受管相对路径（spec §3.3）。
// manifest 展开与 apply 落盘两处都要调用它，任一不通过即整体拒绝。
func SafeRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("manifest: 路径为空")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("manifest: 拒绝绝对路径 %q", rel)
	}
	// Windows 盘符：即便当前只支持 linux/darwin，也不该让它悄悄通过。
	if len(rel) >= 2 && rel[1] == ':' {
		return fmt.Errorf("manifest: 拒绝带盘符的路径 %q", rel)
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return fmt.Errorf("manifest: 路径 %q 含 .. 段", rel)
		}
	}
	return nil
}

// ResolveUnder 把受管相对路径解析成绝对路径，并确认它确实在 root 之下。
//
// 两道判定：先做词法校验（SafeRelPath），再对已存在的部分做
// EvalSymlinks —— 只有前者挡不住「root 下有个符号链接指向外面」。
func ResolveUnder(root, rel string) (string, error) {
	if err := SafeRelPath(rel); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("manifest: 解析 root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}

	abs := filepath.Join(rootAbs, filepath.FromSlash(rel))
	if !within(rootAbs, abs) {
		return "", fmt.Errorf("manifest: %q 解析后跑出了 %s", rel, rootAbs)
	}

	// 目标可能还不存在（apply 的 create 动作）。此时对**存在的最深祖先**
	// 求真实路径，符号链接逃逸就是在这一层被抓住的。
	probe := abs
	for {
		resolved, err := filepath.EvalSymlinks(probe)
		if err == nil {
			suffix := strings.TrimPrefix(abs, probe)
			if !within(rootAbs, filepath.Join(resolved, suffix)) {
				return "", fmt.Errorf("manifest: %q 经符号链接跑出了 %s", rel, rootAbs)
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("manifest: 解析 %q: %w", rel, err)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return filepath.Join(rootAbs, filepath.FromSlash(rel)), nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
