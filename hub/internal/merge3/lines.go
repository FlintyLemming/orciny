// Package merge3 是行级三方合并（M1.8 spec §5）。
//
// 自研而不引依赖：需要的只是最朴素的行级 diff3，算法本身一百多行，
// 而候选库要么捆着整个 git 实现，要么是多年未动的单人仓库。
// 项目已有 go-difflib，SequenceMatcher 正好提供所需的匹配块。
package merge3

import "strings"

// SplitLines 按行切分，**换行符留在行尾**。
//
// 不用 difflib.SplitLines：它会给最后一行强行补上 "\n"，
// 而这里的产物要写回用户的文件，补一个换行就是改了内容。
func SplitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.SplitAfter(string(b), "\n")
	// "a\n" 会切成 ["a\n", ""]，末尾那个空串是切分产物不是内容。
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Join 是 SplitLines 的逆。Join(SplitLines(x)) 与 x 逐字节相同。
func Join(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, ""))
}
