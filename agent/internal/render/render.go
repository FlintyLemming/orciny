// Package render 是 agent 侧的渲染与还原（spec §6）。
//
// 渲染：Revision 里的占位符 → 磁盘上的真实值（本文件）。
// 还原：磁盘上的真实值 → 上报用的占位符（restore.go，子计划 11）。
//
// 词法与转义规则在 protocol，两侧必须逐位一致；这里只负责「值从哪来」。
package render

import (
	"fmt"

	"github.com/FlintyLemming/orciny/protocol"
)

// Render 把内容里的占位符换成真实值。
//
// 任一引用取不到值即整体失败，绝不落一个含字面 {{cred.x}} 的配置给
// Claude Code（spec §6.1）——那会让它拿一个假 key 去请求，
// 错误信息出现在离现场最远的地方。
func Render(content []byte, look func(protocol.Ref) (string, bool)) ([]byte, error) {
	segs, err := protocol.Parse(content)
	if err != nil {
		return nil, err
	}
	out, err := protocol.Render(segs, look)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	return out, nil
}
