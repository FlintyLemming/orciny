// Package applier 把一份 ConfigSnapshot 落到磁盘上（spec §7.4）。
//
// 这是 Orciny **唯一会写用户文件**的地方，因此整个包的组织都围绕一条规矩：
// 宁可不动，不可写坏。具体化为三级处置——
//  1. 任一路径失败 → 逆序还原本次已改动的全部路径 → RolledBack
//  2. 还原本身也失败 → 进 degraded，此后不再自动 apply 任何版本
//  3. 两种情形都上报，由 hub 记事件、面板告警
package applier

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FlintyLemming/orciny/agent/internal/blobcache"
	"github.com/FlintyLemming/orciny/agent/internal/render"
	"github.com/FlintyLemming/orciny/agent/internal/state"
	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

type Step struct {
	Rel      string
	Action   uint8
	Blob     string      // 渲染前内容的 hash
	Rendered string      // 渲染后内容的 hash
	Mode     os.FileMode //
	Keys     []string    // 仅 merge
	Content  []byte      // 已渲染；skip / delete 时为 nil
}

type Plan struct {
	Steps []Step
	// Skip 是「本该管但这次不动」的路径与原因，会随 ApplyAck 上报。
	Skip []manifest.Skip
}

// Writes 返回真的会碰磁盘的步数。幂等重放时它应当是 0。
func (p Plan) Writes() int {
	n := 0
	for _, s := range p.Steps {
		if s.Action != protocol.ActionSkip {
			n++
		}
	}
	return n
}

// BuildPlan 比对快照与本机状态，给出要做的事。
//
// content 是 hash → 渲染前内容（由 blobcache 与刚收到的 BlobData 拼出来）。
// 缺任何一份就整体失败：宁可不 apply，也不能只写一半。
func BuildPlan(
	snap protocol.ConfigSnapshot,
	st *state.State,
	content map[string][]byte,
	look func(protocol.Ref) (string, bool),
) (Plan, error) {
	var p Plan
	wanted := map[string]bool{}

	for _, f := range snap.Files {
		if err := manifest.SafeRelPath(f.Path); err != nil {
			return Plan{}, fmt.Errorf("applier: 快照里的路径 %q 非法: %w", f.Path, err)
		}
		// 恒排除双侧各判一次：这一侧防的是「一个被篡改的 hub 骗 agent
		// 覆盖 .credentials.json」。这是 agent 唯一能自我保护的地方（spec §3.2）。
		if manifest.IsAlwaysExcluded(f.Path) {
			p.Skip = append(p.Skip, manifest.Skip{Rel: f.Path, Reason: protocol.SkipAlwaysExcluded})
			continue
		}
		if matchAny(snap.IgnorePaths, f.Path) {
			continue // 用户显式忽略的，不进 plan 也不上报
		}
		wanted[f.Path] = true

		raw, ok := content[f.Hash]
		if !ok {
			return Plan{}, fmt.Errorf("applier: 缺少 %s 的内容（hash %s）", f.Path, f.Hash)
		}
		out, err := render.Render(raw, look)
		if err != nil {
			// 未定义引用只废掉这一个文件，其余照常（spec §6.1）。
			p.Skip = append(p.Skip, manifest.Skip{Rel: f.Path, Reason: err.Error()})
			continue
		}

		step := Step{
			Rel:      f.Path,
			Blob:     f.Hash,
			Rendered: blobcache.Hash(out),
			Mode:     modeOf(f, raw),
			Keys:     f.Keys,
			Content:  out,
		}
		switch {
		case len(f.Keys) > 0:
			// keys 模式永远走 merge：是否需要改由合并结果决定，
			// 这里判不了——磁盘上还有 Claude Code 自己写的键。
			step.Action = protocol.ActionMerge
		case st != nil && st.Files[f.Path].Rendered == step.Rendered &&
			st.Files[f.Path].Mode == uint32(step.Mode):
			step.Action = protocol.ActionSkip
			step.Content = nil
		case st != nil && st.Files[f.Path].Rendered != "":
			step.Action = protocol.ActionOverwrite
		default:
			step.Action = protocol.ActionCreate
		}
		p.Steps = append(p.Steps, step)
	}

	// 上一版有、本版无 → delete。
	if st != nil {
		for rel, fs := range st.Files {
			if wanted[rel] || matchAny(snap.IgnorePaths, rel) {
				continue
			}
			p.Steps = append(p.Steps, Step{
				Rel: rel, Action: protocol.ActionDelete,
				Blob: fs.Blob, Rendered: fs.Rendered, Mode: os.FileMode(fs.Mode),
			})
		}
	}

	sort.Slice(p.Steps, func(i, j int) bool { return p.Steps[i].Rel < p.Steps[j].Rel })
	sort.Slice(p.Skip, func(i, j int) bool { return p.Skip[i].Rel < p.Skip[j].Rel })
	return p, nil
}

// modeOf 取权限位。快照给了就用快照的；没给（历史数据）就按
// 「含秘密引用则 0600，其余 0644」推导（M1 spec §7.4）。
//
// 「秘密引用」在 M1.6 之后指两个端点的 key（M1.6 spec §3.4）——
// 原来的判据是 {{cred.*}}，那个前缀已经废止。
func modeOf(f protocol.FileEntry, raw []byte) os.FileMode {
	if f.Mode != 0 {
		return os.FileMode(f.Mode)
	}
	if refs, err := protocol.Refs(raw); err == nil {
		for _, r := range refs {
			if r.Kind != protocol.RefProvider {
				continue
			}
			if r.Name == "claude.auth_token" || r.Name == "openai.api_key" {
				return 0o600
			}
		}
	}
	return 0o644
}

func matchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if manifest.MatchGlob(p, rel) || strings.EqualFold(p, rel) {
			return true
		}
	}
	return false
}
