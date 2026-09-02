package migrations

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up007, down007, "007_machine_overrides.go")
}

// Up007 供测试直接调用，断言它幂等且不碰存量 ignore_rules。
func Up007(app core.App) error { return up007(app) }

// Down007 供测试断言「它确实拒绝回滚」。
func Down007(app core.App) error { return down007(app) }

// up007 建 machine_overrides 并给 drift_events.state 加 overridden
// （M1.8 spec §3）。**不迁移存量 ignore_rules**：它们的语义没变
// （路径退管），也没有信息可以升级成覆盖层。
func up007(app core.App) error {
	if err := createMachineOverrides(app); err != nil {
		return err
	}
	return addOverriddenState(app)
}

func createMachineOverrides(app core.App) error {
	if _, err := app.FindCollectionByNameOrId("machine_overrides"); err == nil {
		return nil // 已建过，幂等返回
	}

	machines, err := app.FindCollectionByNameOrId("machines")
	if err != nil {
		return fmt.Errorf("007: 找不到 machines: %w", err)
	}
	blobs, err := app.FindCollectionByNameOrId("blobs")
	if err != nil {
		return fmt.Errorf("007: 找不到 blobs: %w", err)
	}
	revs, err := app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return fmt.Errorf("007: 找不到 revisions: %w", err)
	}
	drifts, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return fmt.Errorf("007: 找不到 drift_events: %w", err)
	}

	// 一条记录 = 一个差异点，不是一个文件（spec §2.1）。
	overrides := core.NewBaseCollection("machine_overrides")
	overrides.Fields.Add(
		&core.RelationField{Name: "machine", Required: true,
			CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.SelectField{Name: "kind", MaxSelect: 1, Required: true,
			Values: []string{"json_key", "text"}},

		// json_key：sjson 键路径（已转义）。text 恒为空串。
		&core.TextField{Name: "selector", Max: 512},

		// json_key 的两侧：原始 JSON 片段。空串 = 该键在这一侧不存在。
		&core.TextField{Name: "base_value", Max: 65536},
		&core.TextField{Name: "mine_value", Max: 65536},

		// text 的两侧：全文 blob。
		&core.RelationField{Name: "base_blob", CollectionId: blobs.Id, MaxSelect: 1},
		&core.RelationField{Name: "mine_blob", CollectionId: blobs.Id, MaxSelect: 1},

		// 需要用户看一眼的状态。空 = 一切正常（spec §4.5）。
		&core.SelectField{Name: "attention", MaxSelect: 1, Values: []string{
			"hub_changed", "merge_conflict", "path_gone", "unmergeable",
		}},
		&core.TextField{Name: "shadowed_value", Max: 65536},
		&core.RelationField{Name: "shadowed_blob", CollectionId: blobs.Id, MaxSelect: 1},
		&core.RelationField{Name: "shadowed_rev", CollectionId: revs.Id, MaxSelect: 1},

		&core.RelationField{Name: "origin_drift", CollectionId: drifts.Id, MaxSelect: 1},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// (machine, path, selector) 唯一：同一处差异点只有一条记录。
	// text 的 selector 恒为空串，因此一个路径上只能有一条 text 覆盖层。
	overrides.AddIndex("idx_overrides_machine_path_sel", true, "machine, path, selector", "")
	overrides.AddIndex("idx_overrides_attention", false, "attention", "attention != ''")
	if err := app.Save(overrides); err != nil {
		return fmt.Errorf("007: 建 machine_overrides: %w", err)
	}
	return nil
}

// addOverriddenState 给 drift_events.state 追加 overridden（spec §2.2）。
// ignored 保留原义（路径退管），两者在收件箱的筛选器里是两个不同的去向。
func addOverriddenState(app core.App) error {
	c, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return fmt.Errorf("007: 找不到 drift_events: %w", err)
	}
	state, ok := c.Fields.GetByName("state").(*core.SelectField)
	if !ok {
		return errors.New("007: drift_events.state 不是 select 字段")
	}
	for _, v := range state.Values {
		if v == "overridden" {
			return nil // 幂等
		}
	}
	state.Values = append(state.Values, "overridden")
	// 按同名覆盖回去，确保改动被序列化进 collection。
	c.Fields.Add(state)
	if err := app.Save(c); err != nil {
		return fmt.Errorf("007: 给 drift_events.state 加 overridden: %w", err)
	}
	return nil
}

// 没有 down 迁移，与 006 同理：删掉集合等于删掉用户的覆盖层，
// 而这是**用户数据**（spec §3）。
func down007(_ core.App) error {
	return errors.New("007: 不支持回滚，删集合等于删用户的覆盖层")
}
