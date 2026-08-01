package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// maxBlobSize 与 protocol.MaxFileSize 同值（512 KiB，spec §5.4）。
// 这里写字面量而不是 import protocol，是为了让 01 与 02 能真正并行开工；
// 02 完成后不需要回头改这里——迁移一旦跑过就不该再动。
const maxBlobSize = 512 << 10

func init() {
	m.Register(up002, down002, "002_configsets.go")
}

// M1 的八个 collection（spec §4.1）。追加式：不动 001。
// API rule 一律 nil —— 仅 superuser 可访问，与 M0 一致。
func up002(app core.App) error {
	machines, err := app.FindCollectionByNameOrId("machines")
	if err != nil {
		return err
	}

	// --- blobs：内容寻址存储 ---------------------------------------
	// content 是 file 字段，落在 pb_data/storage，不进 SQLite 行。
	blobs := core.NewBaseCollection("blobs")
	blobs.Fields.Add(
		&core.TextField{Name: "hash", Required: true, Max: 64},
		&core.NumberField{Name: "size"},
		&core.FileField{Name: "content", MaxSelect: 1, MaxSize: maxBlobSize},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	blobs.AddIndex("idx_blobs_hash", true, "hash", "")
	if err := app.Save(blobs); err != nil {
		return err
	}

	// --- config_sets -----------------------------------------------
	sets := core.NewBaseCollection("config_sets")
	sets.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 200},
		&core.TextField{Name: "note", Max: 2000},
		&core.JSONField{Name: "manifest", MaxSize: 65536},
		&core.BoolField{Name: "paused"},
		// head 指向已发布的最新版本。导入中的草稿态为空，因此不 Required。
		&core.TextField{Name: "head", Max: 64},
		&core.JSONField{Name: "draft", MaxSize: 2 << 20},
		&core.JSONField{Name: "draft_refs", MaxSize: 65536},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	sets.AddIndex("idx_config_sets_name", true, "name", "")
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- revisions：不可变 ------------------------------------------
	revs := core.NewBaseCollection("revisions")
	revs.Fields.Add(
		&core.RelationField{Name: "config_set", Required: true, CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.NumberField{Name: "seq", Required: true},
		&core.JSONField{Name: "files", MaxSize: 2 << 20},
		// manifest 随版本冻结，否则历史版本无法解释（spec §4.1）。
		&core.JSONField{Name: "manifest", MaxSize: 65536},
		&core.TextField{Name: "checksum", Max: 64},
		&core.JSONField{Name: "refs", MaxSize: 65536},
		&core.TextField{Name: "note", Max: 2000},
		&core.SelectField{Name: "source", MaxSelect: 1, Values: []string{"publish", "adopt", "rollback", "import"}},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	revs.AddIndex("idx_revisions_set_seq", true, "config_set, seq", "")
	revs.AddIndex("idx_revisions_set", false, "config_set", "")
	if err := app.Save(revs); err != nil {
		return err
	}

	// head 只能在 revisions 建好之后才能改成 relation。留 text 也可以，
	// 但那样前端 expand 不到版本号，UI 每次都要多一次请求。
	sets.Fields.RemoveByName("head")
	sets.Fields.Add(&core.RelationField{Name: "head", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false})
	if err := app.Save(sets); err != nil {
		return err
	}

	// --- assignments：一机一配置集 ----------------------------------
	assigns := core.NewBaseCollection("assignments")
	assigns.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "config_set", Required: true, CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.SelectField{Name: "mode", MaxSelect: 1, Values: []string{"apply", "survey"}},
		&core.SelectField{Name: "state", MaxSelect: 1, Values: []string{
			"pending", "applying", "aligned", "failed", "degraded", "paused",
		}},
		&core.RelationField{Name: "applied_revision", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.DateField{Name: "applied_at"},
		&core.TextField{Name: "last_error", Max: 4000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	assigns.AddIndex("idx_assignments_machine", true, "machine", "")
	assigns.AddIndex("idx_assignments_set", false, "config_set", "")
	if err := app.Save(assigns); err != nil {
		return err
	}

	// --- credentials -------------------------------------------------
	creds := core.NewBaseCollection("credentials")
	creds.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 64},
		&core.TextField{Name: "cipher_value", Required: true, Max: 8192},
		&core.TextField{Name: "last4", Max: 8},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	creds.AddIndex("idx_credentials_name", true, "name", "")
	if err := app.Save(creds); err != nil {
		return err
	}

	// --- variables：机器变量，非秘密 ---------------------------------
	vars := core.NewBaseCollection("variables")
	vars.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "key", Required: true, Max: 64},
		&core.TextField{Name: "value", Max: 4000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	vars.AddIndex("idx_variables_machine_key", true, "machine, key", "")
	if err := app.Save(vars); err != nil {
		return err
	}

	// --- drift_events：收件箱 ----------------------------------------
	drifts := core.NewBaseCollection("drift_events")
	drifts.Fields.Add(
		&core.RelationField{Name: "machine", Required: true, CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "config_set", CollectionId: sets.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.SelectField{Name: "kind", MaxSelect: 1, Values: []string{"added", "modified", "deleted"}},
		&core.TextField{Name: "base_hash", Max: 64},
		&core.RelationField{Name: "current_blob", CollectionId: blobs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.NumberField{Name: "mode"},
		&core.TextField{Name: "diff", Max: 1 << 20},
		&core.BoolField{Name: "restore_partial"},
		&core.BoolField{Name: "truncated"},
		&core.SelectField{Name: "state", MaxSelect: 1, Values: []string{
			"open", "adopted", "restored", "ignored", "superseded",
		}},
		&core.RelationField{Name: "resolved_revision", CollectionId: revs.Id, MaxSelect: 1, CascadeDelete: false},
		&core.DateField{Name: "resolved_at"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// 部分唯一索引（spec §4.1）：同一路径同一时刻只能有一条待处理漂移，
	// 重复检测到就更新那条，不新建。已解决的记录不占位。
	drifts.AddIndex("idx_drift_open_path", true, "machine, path", "state = 'open'")
	drifts.AddIndex("idx_drift_set_state", false, "config_set, state", "")
	if err := app.Save(drifts); err != nil {
		return err
	}

	// --- ignore_rules -------------------------------------------------
	ignores := core.NewBaseCollection("ignore_rules")
	ignores.Fields.Add(
		// machine 可空 = 全局规则。
		&core.RelationField{Name: "machine", CollectionId: machines.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "path", Required: true, Max: 1024},
		&core.TextField{Name: "note", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	ignores.AddIndex("idx_ignore_rules_machine_path", false, "machine, path", "")
	return app.Save(ignores)
}

func down002(app core.App) error {
	// 逆序删：先删有外键指向别人的。
	for _, name := range []string{
		"ignore_rules", "drift_events", "variables", "credentials",
		"assignments", "revisions", "config_sets", "blobs",
	} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
