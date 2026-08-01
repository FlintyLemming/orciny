package credentials

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// 机器变量：非秘密、不加密。秘密请用凭据（spec §4.1）。
//
// 与凭据同包，是因为两者共同构成「占位符的取值来源」，
// configsync 组装 ConfigSnapshot 时一起取。

func (s *Store) MachineVariables(machineID string) (map[string]string, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m}", "key", 0, 0, map[string]any{"m": machineID})
	if err != nil {
		return nil, fmt.Errorf("credentials: 读取机器变量: %w", err)
	}
	out := make(map[string]string, len(recs))
	for _, r := range recs {
		out[r.GetString("key")] = r.GetString("value")
	}
	return out, nil
}

func (s *Store) SetVariable(machineID, key, value string) error {
	if !validName(key) {
		return fmt.Errorf("%w: 变量名 %q（只允许 [A-Za-z0-9_-]+）", ErrBadName, key)
	}
	r, err := s.findVariable(machineID, key)
	if err != nil {
		return err
	}
	if r == nil {
		c, cerr := s.app.FindCollectionByNameOrId("variables")
		if cerr != nil {
			return fmt.Errorf("credentials: 找不到 collection: %w", cerr)
		}
		r = core.NewRecord(c)
		r.Set("machine", machineID)
		r.Set("key", key)
	}
	r.Set("value", value)
	if err := s.app.Save(r); err != nil {
		return fmt.Errorf("credentials: 保存变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteVariable(machineID, key string) error {
	r, err := s.findVariable(machineID, key)
	if err != nil || r == nil {
		return err
	}
	if err := s.app.Delete(r); err != nil {
		return fmt.Errorf("credentials: 删除变量 %s: %w", key, err)
	}
	return nil
}

func (s *Store) findVariable(machineID, key string) (*core.Record, error) {
	recs, err := s.app.FindRecordsByFilter("variables",
		"machine = {:m} && key = {:k}", "", 1, 0,
		map[string]any{"m": machineID, "k": key})
	if err != nil {
		return nil, fmt.Errorf("credentials: 查询变量: %w", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return recs[0], nil
}
