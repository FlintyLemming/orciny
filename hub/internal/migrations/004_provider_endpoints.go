package migrations

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(up004, down004, "004_provider_endpoints.go")
}

// M1.6 的双端点服务配置（M1.6 spec §2.1 / §6.1 的第 1、2 步）。
//
// **追加式**：本迁移只加字段、搬数据，一个旧字段都不删。破坏性的那一半
// （删 relation、删旧字段、删 credentials collection）在 005 里，
// 拆开是为了让中间每一步的测试都能是绿的（实现计划 00-overview「偏离一」）。
//
// 密文**直接搬、不解密**：同一把主密钥、同一套 AES-GCM，secretbox 只是换了
// 包名。这是「KeyFileName 与 EnvKeyName 都不变」换来的（spec §2.5）。
func up004(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return err
	}

	provs.Fields.Add(
		// 三处密文一律 Hidden：PocketBase 的列表查询与 realtime 都带不上它。
		// spec §5.2 说「密文永不回传前端」，JSON 字段没法只隐藏一个子键，
		// 所以密文不进端点 JSON，单独放顶层。
		&core.TextField{Name: "key_cipher", Max: 8192, Hidden: true},
		&core.TextField{Name: "key_last4", Max: 8},
		&core.TextField{Name: "claude_key_cipher", Max: 8192, Hidden: true},
		&core.TextField{Name: "openai_key_cipher", Max: 8192, Hidden: true},
		// 端点子结构。末四位在里面（要回传），密文不在里面。
		&core.JSONField{Name: "claude", MaxSize: 16384},
		&core.JSONField{Name: "openai", MaxSize: 16384},
	)

	// credential 与 base_url 转非必填：004 之后新建的 provider 既没有凭据可指，
	// 也不再写顶层 base_url（它搬进 claude 子结构了）。两个字段本身留到 005 才删，
	// 但「必填」这条约束必须现在就撤——否则 004 与 005 之间新建 provider 会被挡住。
	if rel, ok := provs.Fields.GetByName("credential").(*core.RelationField); ok {
		rel.Required = false
	}
	if tf, ok := provs.Fields.GetByName("base_url").(*core.TextField); ok {
		tf.Required = false
	}

	if err := app.Save(provs); err != nil {
		return err
	}
	return BackfillProviderEndpoints(app)
}

// BackfillProviderEndpoints 把每条 provider 的旧字段与它引用的凭据搬进新结构
// （spec §6.1 第 2 步）。导出是为了让迁移测试能在数据就位之后再跑一次。
//
// 幂等：已经有 claude.base_url 的记录直接跳过，重跑不会把用户后来改过的
// 端点数据覆盖回旧字段的值。
func BackfillProviderEndpoints(app core.App) error {
	recs, err := app.FindAllRecords("providers")
	if err != nil {
		return fmt.Errorf("004: 扫描服务配置: %w", err)
	}
	for _, r := range recs {
		var existing struct {
			BaseURL string `json:"base_url"`
		}
		_ = r.UnmarshalJSONField("claude", &existing)
		if existing.BaseURL != "" {
			continue // 搬过了
		}

		var models []string
		_ = r.UnmarshalJSONField("models", &models)
		if models == nil {
			models = []string{}
		}
		var defaults map[string]string
		_ = r.UnmarshalJSONField("defaults", &defaults)
		if defaults == nil {
			defaults = map[string]string{}
		}

		claude := map[string]any{
			"base_url":   r.GetString("base_url"),
			"auth_field": r.GetString("auth_field"),
			"models":     models,
			"defaults": map[string]string{
				"main":   defaults["main"],
				"opus":   defaults["opus"],
				"sonnet": defaults["sonnet"],
				"haiku":  defaults["haiku"],
			},
		}

		// 凭据的密文与末四位搬到**平台级**：003 的模型里一条 provider
		// 只有一把 key，它对两个端点都适用（spec §2.3 的常见情形）。
		if credID := r.GetString("credential"); credID != "" {
			if cred, cerr := app.FindRecordById("credentials", credID); cerr == nil && cred != nil {
				r.Set("key_cipher", cred.GetString("cipher_value"))
				r.Set("key_last4", cred.GetString("last4"))
				claude["key_last4"] = cred.GetString("last4")
			}
		}

		cb, err := json.Marshal(claude)
		if err != nil {
			return fmt.Errorf("004: 序列化 %s 的 claude 端点: %w", r.Id, err)
		}
		r.Set("claude", json.RawMessage(cb))
		// openai 端点没有来源，写一个「未配置」的空结构而不是留 null——
		// 前端拿到 null 与拿到 {base_url:""} 要走两条分支，少一条是一条。
		r.Set("openai", json.RawMessage(
			`{"base_url":"","auth_field":"OPENAI_API_KEY","models":[],"default_model":""}`))

		if err := app.Save(r); err != nil {
			return fmt.Errorf("004: 保存服务配置 %s: %w", r.Id, err)
		}
	}
	return nil
}

// down004 是真的可逆：本迁移只加字段。
// （破坏性的那一半在 005，它的 down 直接返回错误。）
func down004(app core.App) error {
	provs, err := app.FindCollectionByNameOrId("providers")
	if err != nil {
		return nil
	}
	for _, f := range []string{
		"key_cipher", "key_last4", "claude_key_cipher", "openai_key_cipher",
		"claude", "openai",
	} {
		provs.Fields.RemoveByName(f)
	}
	if rel, ok := provs.Fields.GetByName("credential").(*core.RelationField); ok {
		rel.Required = true
	}
	if tf, ok := provs.Fields.GetByName("base_url").(*core.TextField); ok {
		tf.Required = true
	}
	return app.Save(provs)
}
