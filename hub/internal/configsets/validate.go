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

	// M1.6：双端点（spec §4.3）
	ProblemEndpointMissing = "endpoint_missing"
)

// SettingsPath 是 settings.json 的受管相对路径。manifest 的根是 HOME。
const SettingsPath = ".claude/settings.json"

// tokenAuthToken 是承载 claude 端点 API key 的那个占位符的字面形态。
//
// **只管 claude 端点**（M1.6 spec §4.2）：这条校验找的是 settings.json 的 env
// 里键名对不对，而 openai 端点的 auth_field 是自由文本，没有「二选一选错了」
// 这种可判定的错误形态；.codex/config.toml 的结构也不是 env 对象。
const tokenAuthToken = "{{provider.claude.auth_token}}"

// Validate 检查草稿能不能发布。
//
// 机器变量的「已定义」由本方法自己查 variables 表——原来的 known 参数装的是
// 「已定义的凭据名」，凭据实体废止后它没有内容可装了（M1.6 spec §3.5）。
//
// 未定义引用必须在这里拒掉：让它进 Revision，agent 渲染时只能拒绝
// apply 该文件，而那时用户已经点过发布了（M1 spec §6.1）。
func (s *Service) Validate(setID string) ([]Problem, error) {
	files, err := s.Draft(setID)
	if err != nil {
		return nil, err
	}
	known, err := s.knownVars()
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
			// machine.* 是内置值；provider.* 的「已定义」由绑定与端点校验
			// 判定（M1.6 spec §4），不走这条路——否则每个绑了服务的配置集
			// 都会报一堆假的未定义引用。
			if ref.Kind != protocol.RefVar {
				continue
			}
			if !known[ref.Name] {
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

// knownVars 汇总全库已定义的机器变量名。
//
// 不按机器分：发布是全配置集一次的动作，而同一个配置集可能指派给多台机器。
// 「至少有一台机器定义了它」是发布期能给出的最宽松的判定，剩下的由 agent
// 渲染时的未定义引用兜底（M1 spec §6.1）。
func (s *Service) knownVars() (map[string]bool, error) {
	recs, err := s.app.FindAllRecords("variables")
	if err != nil {
		return nil, fmt.Errorf("configsets: 读取机器变量: %w", err)
	}
	out := make(map[string]bool, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = true
	}
	return out, nil
}
