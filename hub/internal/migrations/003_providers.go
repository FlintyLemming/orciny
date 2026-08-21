package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up003, down003, "003_providers.go")
}

// M1.5 的服务绑定（M1.5 spec §2）。追加式：不动 001 / 002。
//
// providers 与 credentials 是同一类东西——被配置集引用的资源，不是配置本身。
// key 不另存：服务配置里的 API key 就是一条 credential，复用加密落库、
// 末四位显示、启动自检与引用计数防误删（spec §2.1）。
func up003(app core.App) error {
	creds, err := app.FindCollectionByNameOrId("credentials")
	if err != nil {
		return err
	}

	providers := core.NewBaseCollection("providers")
	providers.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 200},
		// preset 为空即「自定义平台」。可编辑的预设表被 YAGNI 掉了（spec §2.3），
		// 自定义平台由这条空 preset 完全覆盖。
		&core.TextField{Name: "preset", Max: 64},
		&core.TextField{Name: "base_url", Required: true, Max: 2000},
		&core.SelectField{Name: "auth_field", MaxSelect: 1, Values: []string{
			"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY",
		}},
		// CascadeDelete 必须是 false：删凭据不该悄悄带走服务配置。
		// 真正的保护在 credentials.Store：被 Provider 引用的凭据直接不许删（spec §5.3）。
		&core.RelationField{Name: "credential", Required: true, CollectionId: creds.Id,
			MaxSelect: 1, CascadeDelete: false},
		&core.JSONField{Name: "models", MaxSize: 8192},
		&core.JSONField{Name: "defaults", MaxSize: 2048},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	providers.AddIndex("idx_providers_name", true, "name", "")
	// 「这条凭据被哪些服务配置引用」是删除保护的热路径，给它索引。
	providers.AddIndex("idx_providers_credential", false, "credential", "")
	if err := app.Save(providers); err != nil {
		return err
	}

	// --- revisions.binding：引用进版本，值不进（spec §2.2） ---------
	revs, err := app.FindCollectionByNameOrId("revisions")
	if err != nil {
		return err
	}
	revs.Fields.Add(&core.JSONField{Name: "binding", MaxSize: 4096})
	if err := app.Save(revs); err != nil {
		return err
	}

	// --- config_sets.draft_binding / head_provider -------------------
	sets, err := app.FindCollectionByNameOrId("config_sets")
	if err != nil {
		return err
	}
	sets.Fields.Add(
		&core.JSONField{Name: "draft_binding", MaxSize: 4096},
		// head_provider 是冗余字段，唯一目的是让「改了 Provider X，谁要重注入」
		// 变成一次索引查询而不是 JSON 字段扫描 + 三次 join（spec §2.2）。
		// 唯一写入点在 revisions.PublishFiles，与 head 同写。
		&core.RelationField{Name: "head_provider", CollectionId: providers.Id,
			MaxSelect: 1, CascadeDelete: false},
	)
	sets.AddIndex("idx_config_sets_head_provider", false, "head_provider", "")
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- drift_events：绑定漂移的标记与反查素材（spec §6.1） ----------
	drifts, err := app.FindCollectionByNameOrId("drift_events")
	if err != nil {
		return err
	}
	drifts.Fields.Add(
		&core.BoolField{Name: "binding_drift"},
		&core.TextField{Name: "binding_url", Max: 2000},
	)
	return app.Save(drifts)
}

func down003(app core.App) error {
	for name, fields := range map[string][]string{
		"drift_events": {"binding_drift", "binding_url"},
		"config_sets":  {"draft_binding", "head_provider"},
		"revisions":    {"binding"},
	} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		for _, f := range fields {
			c.Fields.RemoveByName(f)
		}
		if err := app.Save(c); err != nil {
			return err
		}
	}
	c, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil
	}
	return app.Delete(c)
}
