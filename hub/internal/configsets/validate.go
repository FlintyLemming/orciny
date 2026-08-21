package configsets

import (
	"fmt"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Problem 是一条发布期校验失败。UI 逐条展示；Warning 为真的只展示不阻断。
type Problem struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Detail  string `json:"detail"`
	Warning bool   `json:"warning,omitempty"`
	Fix     *Fix   `json:"fix,omitempty"`
}

// Fix 描述一处「一键修复」。目前只有 auth_field 键名换绑用它。
type Fix struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

const FixReplaceEnvKey = "replace_env_key"

// 校验类别
const (
	ProblemAlwaysExcluded = "always_excluded"
	ProblemTooLarge       = "too_large"
	ProblemUndefinedRef   = "undefined_ref"
	ProblemBadPath        = "bad_path"
	ProblemMissingBlob    = "missing_blob"

	// M1.5：服务绑定（spec §7）
	ProblemBindingMissing    = "binding_missing"
	ProblemBindingUnused     = "binding_unused"
	ProblemAuthFieldMismatch = "auth_field_mismatch"
)

// SettingsPath 是 settings.json 的受管相对路径。manifest 的根是 HOME。
const SettingsPath = ".claude/settings.json"

// tokenAuthToken 是承载 API key 的那个占位符的字面形态。
const tokenAuthToken = "{{provider.auth_token}}"

// Validate 检查草稿能不能发布。known 是当前存在的凭据与变量，
// 键形如 "cred.foo" / "var.bar"；machine.* 是内置值，永远算已定义。
//
// 未定义引用必须在这里拒掉：让它进 Revision，agent 渲染时只能拒绝
// apply 该文件，而那时用户已经点过发布了（spec §6.1）。
func (s *Service) Validate(setID string, known map[string]bool) ([]Problem, error) {
	files, err := s.Draft(setID)
	if err != nil {
		return nil, err
	}

	var problems []Problem
	for _, f := range files {
		if err := manifest.SafeRelPath(f.Path); err != nil {
			problems = append(problems, Problem{Path: f.Path, Kind: ProblemBadPath,
				Detail: err.Error()})
			continue
		}
		if manifest.IsAlwaysExcluded(f.Path) {
			problems = append(problems, Problem{Path: f.Path, Kind: ProblemAlwaysExcluded,
				Detail: "该路径属于恒排除清单，不可纳管"})
			continue
		}
		if f.Size > protocol.MaxFileSize {
			problems = append(problems, Problem{Path: f.Path, Kind: ProblemTooLarge,
				Detail: fmt.Sprintf("%d 字节，超过单文件上限 512 KiB", f.Size)})
			continue
		}

		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			problems = append(problems, Problem{Path: f.Path, Kind: ProblemMissingBlob,
				Detail: "内容不在库里：" + f.Hash})
			continue
		}
		refs, err := protocol.Refs(content)
		if err != nil {
			problems = append(problems, Problem{Path: f.Path, Kind: ProblemUndefinedRef,
				Detail: err.Error()})
			continue
		}
		for _, ref := range refs {
			// machine.* 是内置值；provider.* 的「已定义」由三条绑定校验
			// 判定（M1.5 spec §7），不走这条路——否则每个绑了服务的配置集
			// 都会报一堆假的未定义引用。
			if ref.Kind == protocol.RefMachine || ref.Kind == protocol.RefProvider {
				continue
			}
			if !known[ref.String()] {
				problems = append(problems, Problem{Path: f.Path, Kind: ProblemUndefinedRef,
					Detail: "未定义的引用 " + ref.String()})
			}
		}
	}

	bp, err := s.validateBinding(setID, files)
	if err != nil {
		return nil, err
	}
	return append(problems, bp...), nil
}
