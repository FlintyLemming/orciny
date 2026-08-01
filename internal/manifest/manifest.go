// Package manifest 定义受管范围的 schema 与判定规则（spec §3）。
//
// 放在顶层 internal/ 而非 agent/internal/：hub 发布期要用同一套 glob 与
// 恒排除规则做校验，而 hub 碰不到 agent/internal/*。定位同 internal/clock
// ——中立于 hub 与 agent，不构成两者之间的依赖。
//
// 路径根一律是 **HOME**，不是 ~/.claude（spec §3.1）：~/.claude.json 在
// .claude/ 之外，用 ../.claude.json 表达既丑又与「禁止 ..」的路径安全规则
// 正面冲突。顺带的收益是 v2 引入 OpenCode / Codex 时直接加 .opencode/**。
package manifest

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

type Mode string

const (
	// ModeFile 单个文件整体受管；漂移比对口径是渲染后全文 hash。
	ModeFile Mode = "file"
	// ModeTree glob 匹配的整棵子树受管，**含新增文件**——这是「在某台机器上
	// 新写一个 skill 能被收编」的支点（spec §3.1）。
	ModeTree Mode = "tree"
	// ModeKeys 只重写 JSON 顶层的指定键，其余键原样保留。
	ModeKeys Mode = "keys"
)

type Include struct {
	Path string   `json:"path"`
	Mode Mode     `json:"mode"`
	Keys []string `json:"keys,omitempty"`
}

type Manifest struct {
	Version int       `json:"version"`
	Include []Include `json:"include"`
	Exclude []string  `json:"exclude,omitempty"`
}

// Default 是导入向导用的出厂 manifest（spec §3.1）。
func Default() Manifest {
	return Manifest{
		Version: 1,
		Include: []Include{
			{Path: ".claude/settings.json", Mode: ModeFile},
			{Path: ".claude/CLAUDE.md", Mode: ModeFile},
			{Path: ".claude/keybindings.json", Mode: ModeFile},
			{Path: ".claude/agents/**", Mode: ModeTree},
			{Path: ".claude/commands/**", Mode: ModeTree},
			{Path: ".claude/skills/**", Mode: ModeTree},
			{Path: ".claude.json", Mode: ModeKeys, Keys: []string{"mcpServers"}},
		},
		Exclude: []string{"**/.DS_Store"},
	}
}

// alwaysExcluded 是恒排除清单（spec §3.2，产品 §4.3 的「恒定不可去除」）。
//
// 它不在 manifest 里，因为「不可去除」不能只是 UI 上的一句话——放进一个
// 用户可编辑的字段就等于可去除。增删这份清单需要发版。
var alwaysExcluded = []string{
	".claude/projects/**",        // 会话历史
	".claude/todos/**",           //
	".claude/shell-snapshots/**", //
	".claude/statsig/**",         //
	".claude/.credentials.json",  // OAuth 登录态，机器私有
	"**/.git/**",                 //
	"**/node_modules/**",         //
}

func AlwaysExcluded() []string {
	out := make([]string, len(alwaysExcluded))
	copy(out, alwaysExcluded)
	return out
}

func IsAlwaysExcluded(rel string) bool {
	for _, p := range alwaysExcluded {
		if MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

func Parse(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: 解析失败: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// JSON 稳定序列化。冻结进 revision 的就是这份字节。
func (m Manifest) JSON() ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("manifest: 序列化失败: %w", err)
	}
	return b, nil
}

func (m Manifest) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("manifest: version 只支持 1，收到 %d", m.Version)
	}
	if len(m.Include) == 0 {
		return fmt.Errorf("manifest: include 不能为空")
	}
	for _, inc := range m.Include {
		if err := SafeRelPath(strings.TrimSuffix(inc.Path, "/**")); err != nil {
			return fmt.Errorf("manifest: include %q: %w", inc.Path, err)
		}
		switch inc.Mode {
		case ModeFile, ModeTree:
		case ModeKeys:
			if len(inc.Keys) == 0 {
				return fmt.Errorf("manifest: include %q 是 keys 模式但没给 keys", inc.Path)
			}
		default:
			return fmt.Errorf("manifest: include %q 的 mode %q 未知", inc.Path, inc.Mode)
		}
		if IsAlwaysExcluded(inc.Path) {
			return fmt.Errorf("manifest: include %q 属于恒排除路径，不可纳管", inc.Path)
		}
	}
	for _, ex := range m.Exclude {
		if strings.Contains(ex, "..") {
			return fmt.Errorf("manifest: exclude %q 含 ..", ex)
		}
	}
	return nil
}

// Match 报告 rel 命中哪条 include。
//
// 判定顺序恒为：恒排除 > manifest.exclude > manifest.include（spec §3.2）。
// 任何 include 都无法把恒排除路径拉回来。
func (m Manifest) Match(rel string) (Include, bool) {
	if IsAlwaysExcluded(rel) {
		return Include{}, false
	}
	for _, ex := range m.Exclude {
		if MatchGlob(ex, rel) {
			return Include{}, false
		}
	}
	for _, inc := range m.Include {
		if MatchGlob(inc.Path, rel) {
			return inc, true
		}
	}
	return Include{}, false
}

// MatchGlob 支持 * （单层）与 ** （跨层）。
//
// 不用 path.Match：它的 * 会跨过 /，而 ".claude/*.json" 必须只匹配一层。
func MatchGlob(pattern, p string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegments(pat, seg []string) bool {
	if len(pat) == 0 {
		return len(seg) == 0
	}
	if pat[0] == "**" {
		// 末尾的 ** 至少吃掉一段：".claude/skills/**" 不匹配 ".claude/skills" 本身。
		// 前缀/中间的 ** 可以吃 0 段："**/.DS_Store" 要匹配根下的 ".DS_Store"。
		if len(pat) == 1 {
			return len(seg) >= 1
		}
		for i := 0; i <= len(seg); i++ {
			if matchSegments(pat[1:], seg[i:]) {
				return true
			}
		}
		return false
	}
	if len(seg) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], seg[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], seg[1:])
}
