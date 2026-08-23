// Package variables 管机器变量（{{var.*}}）。
//
// 非秘密、不加密、按机器（M1 spec §4.1）。它当初与凭据同包，理由是
// 「两者共同构成占位符的取值来源」；凭据实体废止之后这个理由不成立，
// 而机器变量与秘密加密没有任何共享代码——留在一起只会让 secretbox
// 那个安全敏感的包混进不相干的东西（M1.6 spec §2.5）。
package variables

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

var ErrBadName = errors.New("variables: 变量名非法")

type Store struct {
	app core.App
}

func NewStore(app core.App) *Store { return &Store{app: app} }

func (s *Store) MachineVariables(machineID string) (map[string]string, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m}", "key", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("variables: 读取机器变量: %w", err)
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

func (s *Store) SetVariable(machineID, key, value string) error {
	if !validName(key) {
		return fmt.Errorf("%w: %q（只允许 [A-Za-z0-9_-]+）", ErrBadName, key)
	}
	r, err := s.find(machineID, key)
	if err != nil {
		return err
	}
	if r == nil {
		c, cerr := s.app.FindCollectionByNameOrId("variables")
		if cerr != nil {
			return fmt.Errorf("variables: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
		r.Set("key", key)
	}
	r.Set("value", value)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("variables: 保存变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteVariable(machineID, key string) error {
	r, err := s.find(machineID, key)
	if err != nil || r == nil {
		return err
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("variables: 删除变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) find(machineID, key string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m} && key = {:k}", "", 1, 0,
		map[string]any{"m": machineID, "k": key})
	if err != nil {
		return nil, fmt.Errorf("variables: 查询变量: %w", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}
