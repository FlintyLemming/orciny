package configsets

import (
	"fmt"

	"github.com/FlintyLemming/orciny/internal/manifest"
	"github.com/FlintyLemming/orciny/protocol"
)

// Problem 是一条发布期校验失败。UI 逐条展示并阻止发布。
type Problem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// 校验类别
const (
	ProblemAlwaysExcluded = "always_excluded"
	ProblemTooLarge       = "too_large"
	ProblemUndefinedRef   = "undefined_ref"
	ProblemBadPath        = "bad_path"
	ProblemMissingBlob    = "missing_blob"
)

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
			problems = append(problems, Problem{f.Path, ProblemBadPath, err.Error()})
			continue
		}
		if manifest.IsAlwaysExcluded(f.Path) {
			problems = append(problems, Problem{f.Path, ProblemAlwaysExcluded,
				"该路径属于恒排除清单，不可纳管"})
			continue
		}
		if f.Size > protocol.MaxFileSize {
			problems = append(problems, Problem{f.Path, ProblemTooLarge,
				fmt.Sprintf("%d 字节，超过单文件上限 512 KiB", f.Size)})
			continue
		}

		content, err := s.blobs.Get(f.Hash)
		if err != nil {
			problems = append(problems, Problem{f.Path, ProblemMissingBlob,
				"内容不在库里：" + f.Hash})
			continue
		}
		refs, err := protocol.Refs(content)
		if err != nil {
			problems = append(problems, Problem{f.Path, ProblemUndefinedRef, err.Error()})
			continue
		}
		for _, ref := range refs {
			if ref.Kind == protocol.RefMachine {
				continue
			}
			if !known[ref.String()] {
				problems = append(problems, Problem{f.Path, ProblemUndefinedRef,
					"未定义的引用 " + ref.String()})
			}
		}
	}
	return problems, nil
}
