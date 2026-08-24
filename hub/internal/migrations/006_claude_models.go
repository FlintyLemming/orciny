package migrations

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

func init() {
	m.Register(up006, down006, "006_claude_models.go")
}

func up006(app core.App) error {
	return BackfillClaudeModels(app)
}

func BackfillClaudeModels(app core.App) error {
	recs, err := app.FindAllRecords("providers")
	if err != nil {
		return fmt.Errorf("006: 扫描服务配置: %w", err)
	}

	for _, r := range recs {
		var rawClaude struct {
			BaseURL   string          `json:"base_url"`
			AuthField string          `json:"auth_field"`
			KeyLast4  string          `json:"key_last4,omitempty"`
			Models    json.RawMessage `json:"models"`
			Defaults  struct {
				Main   string `json:"main"`
				Opus   string `json:"opus"`
				Sonnet string `json:"sonnet"`
				Haiku  string `json:"haiku"`
			} `json:"defaults"`
		}

		if err := r.UnmarshalJSONField("claude", &rawClaude); err != nil || rawClaude.BaseURL == "" {
			continue
		}

		oneMBases := map[string]bool{}
		for _, slot := range []string{
			rawClaude.Defaults.Main, rawClaude.Defaults.Opus,
			rawClaude.Defaults.Sonnet, rawClaude.Defaults.Haiku,
		} {
			if providers.HasOneM(slot) {
				oneMBases[providers.StripOneM(slot)] = true
			}
		}

		// Try unmarshaling as string slice
		var stringModels []string
		if err := json.Unmarshal(rawClaude.Models, &stringModels); err == nil {
			newModels := make([]providers.ClaudeModel, len(stringModels))
			for i, name := range stringModels {
				newModels[i] = providers.ClaudeModel{
					Name: name,
					OneM: oneMBases[name],
				}
			}

			updatedClaude := map[string]any{
				"base_url":   rawClaude.BaseURL,
				"auth_field": rawClaude.AuthField,
				"key_last4":  rawClaude.KeyLast4,
				"models":     newModels,
				"defaults":   rawClaude.Defaults,
			}
			cb, err := json.Marshal(updatedClaude)
			if err != nil {
				return fmt.Errorf("006: 序列化 %s claude 端点: %w", r.Id, err)
			}
			r.Set("claude", json.RawMessage(cb))
			if err := app.Save(r); err != nil {
				return fmt.Errorf("006: 保存 %s: %w", r.Id, err)
			}
		}
	}
	return nil
}

func down006(_ core.App) error {
	return errors.New("006: 不支持回滚，模型能力标注为单向升级")
}

// Down006 供测试断言「它确实拒绝回滚」。
func Down006(app core.App) error { return down006(app) }
