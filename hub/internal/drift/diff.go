// Package drift 是收件箱：漂移落库、diff 计算、收编 / 恢复 / 忽略。
//
// diff 由 hub 算而不是 agent（spec §8.2）：agent 更瘦，算法只有一份实现，
// 而且收编因此退化成纯 hub 侧操作——用已有的 blob 组装新 Revision 就完事。
package drift

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

// MaxDiffBytes 是存进 drift_events.diff 的上限。
// 它会被前端直接渲染，不设上限的话一个大文件就能让收件箱卡住。
const MaxDiffBytes = 256 << 10

// UnifiedDiff 算一份 unified diff。内容相同时返回空串。
func UnifiedDiff(path string, base, cur []byte) string {
	if string(base) == string(cur) {
		return ""
	}
	if !isText(base) || !isText(cur) {
		return fmt.Sprintf("（二进制内容，不显示差异：%s，%d → %d 字节）",
			path, len(base), len(cur))
	}

	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(base)),
		B:        difflib.SplitLines(string(cur)),
		FromFile: path + "（基线）",
		ToFile:   path + "（本机）",
		Context:  3,
	})
	if err != nil {
		return fmt.Sprintf("（计算差异失败：%v）", err)
	}
	if len(out) > MaxDiffBytes {
		return out[:MaxDiffBytes] + "\n…（差异过大，已截断）\n"
	}
	return out
}

// isText 判断内容是否适合按文本 diff。受管的都是配置文件，
// 出现二进制多半意味着有人往 skills 目录里放了别的东西。
func isText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	if !utf8.Valid(b) {
		return false
	}
	return !strings.ContainsRune(string(b), 0)
}
