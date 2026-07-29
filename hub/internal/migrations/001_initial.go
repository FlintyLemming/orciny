// Package migrations 以声明式方式定义 hub 的 collection。
// 只增不改：后续里程碑新增文件，不动已有文件（spec §8）。
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up001, down001, "001_initial.go")
}

func up001(app core.App) error {
	// --- machines ---------------------------------------------------
	machines := core.NewBaseCollection("machines")
	machines.Fields.Add(
		&core.TextField{Name: "name", Max: 200},
		&core.TextField{Name: "fingerprint", Required: true, Max: 64},
		&core.TextField{Name: "pub_key", Required: true, Max: 128},
		&core.TextField{Name: "hostname", Max: 255},
		&core.TextField{Name: "os", Max: 32},
		&core.TextField{Name: "arch", Max: 32},
		&core.TextField{Name: "agent_version", Max: 32},
		&core.JSONField{Name: "tool_versions", MaxSize: 4096},
		// paused 在 M0 没有切换入口，但字段先就位（spec §6.1 / §16）
		&core.SelectField{
			Name:      "status",
			Required:  true,
			MaxSelect: 1,
			Values:    []string{"online", "offline", "paused"},
		},
		&core.DateField{Name: "last_seen"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	machines.AddIndex("idx_machines_fingerprint", true, "fingerprint", "")
	// API rule 全部保持 nil —— 仅 superuser 可访问（spec §5.3）
	if err := app.Save(machines); err != nil {
		return err
	}

	// --- enroll_tokens ----------------------------------------------
	tokens := core.NewBaseCollection("enroll_tokens")
	tokens.Fields.Add(
		&core.TextField{Name: "token_hash", Required: true, Max: 64},
		&core.DateField{Name: "expires_at", Required: true},
		&core.DateField{Name: "used_at"},
		&core.RelationField{
			Name:          "machine",
			CollectionId:  machines.Id,
			MaxSelect:     1,
			CascadeDelete: false,
		},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	tokens.AddIndex("idx_enroll_tokens_hash", true, "token_hash", "")
	if err := app.Save(tokens); err != nil {
		return err
	}

	// --- events ------------------------------------------------------
	events := core.NewBaseCollection("events")
	events.Fields.Add(
		&core.TextField{Name: "kind", Required: true, Max: 64},
		&core.RelationField{
			Name:         "machine",
			CollectionId: machines.Id,
			MaxSelect:    1,
			// 机器被删除后事件必须留下（machine.removed 正是此时写的），
			// 因此不级联删除，字段留空即可。
			CascadeDelete: false,
		},
		&core.JSONField{Name: "detail", MaxSize: 8192},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	events.AddIndex("idx_events_created", false, "created", "")
	events.AddIndex("idx_events_machine", false, "machine", "")
	return app.Save(events)
}

func down001(app core.App) error {
	for _, name := range []string{"events", "enroll_tokens", "machines"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue // 已不存在，视为已回滚
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
