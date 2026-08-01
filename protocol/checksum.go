package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Checksum 是一份文件清单的指纹（spec §7.2）。
//
// 口径（hub 与 agent 必须逐位一致，改动等于全部历史版本重新物化）：
// 对每个文件取 path\x00hash\x00mode\n —— mode 为八进制、无前导 0 ——
// 按 path 字典序拼接后整体 sha256，hex 输出。
//
// Size 不参与：它由 Hash 唯一决定，进去只是多一处可能不一致的地方。
// Keys 也不参与：keys 模式的 Hash 已经是受管键子树的规范化 hash。
func Checksum(files []FileEntry) string {
	sorted := make([]FileEntry, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var sb strings.Builder
	for _, f := range sorted {
		sb.WriteString(f.Path)
		sb.WriteByte(0)
		sb.WriteString(f.Hash)
		sb.WriteByte(0)
		sb.WriteString(strconv.FormatUint(uint64(f.Mode), 8))
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
