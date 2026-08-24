# Orciny M1.7 · 模型能力归位与一键快切 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement model capability attribution (`ClaudeModel{Name, OneM}`), migration 006, one-click quick switch in ConfigSets list, publish validation warning `one_m_unsupported`, undo toast, and guardrails.

**Architecture:** Split `Models` from shared `Endpoint` struct so Claude endpoint models carry `one_m` capability while OpenAI models stay `[]string`. Migrate existing provider records with automatic capability inference from `defaults`. Provide a zero-dialog quick-switch dropdown in the config set list that sets binding, runs validation, publishes, and shows an undo toast, while adhering to 4 safety guardrails.

**Tech Stack:** Go (PocketBase, standard testing, testify), TypeScript, React, Nanostores, Vitest, Testing Library, Tailwind CSS.

**Spec:** `docs/superpowers/specs/2026-08-23-model-quick-switch-design.md`

## Global Constraints

- Go structs: `ClaudeModel` has `name` (string) and `one_m` (bool, `omitempty`). `Endpoint` retains only `base_url`, `auth_field`, `key_last4`.
- Migration 006: Automatic inference (`oneMBases := { StripOneM(s) | s in claude.defaults, HasOneM(s) }`). Single migration, no down migration.
- Wire protocol: Model capabilities never cross protocol to agents or snapshot blobs.
- Validation: `one_m_unsupported` is a non-blocking warning triggered when binding has `[1m]`, model base name exists in provider claude models, and `one_m == false`.
- Safety Guardrails: Quick switch disabled with link to detail page when draft has file changes (`有未发布改动 →`) or when 4 slots are split (`已分设 →`).
- Undo toast: 8s duration, rollback restores prior head and draft.

---

### Task 1: Go `ClaudeModel` Struct, Endpoint Separation, Preset Table Rewrite, and Invariant Test

**Files:**
- Modify: `hub/internal/providers/presets.go`
- Modify: `hub/internal/providers/presets_test.go`
- Modify: `hub/internal/providers/endpoint_test.go`
- Modify: `hub/internal/providers/store.go`
- Modify: `hub/internal/providers/store_test.go`
- Modify: `hub/hub_test.go`
- Modify: `hub/internal/configsets/binding_test.go`
- Modify: `hub/internal/drift/adopt_test.go`
- Modify: `hub/internal/importer/service_test.go`
- Modify: `hub/internal/revisions/service_test.go`

**Interfaces:**
- Consumes: Existing `providers` package types.
- Produces:
  - `type ClaudeModel struct { Name string \`json:"name"\`; OneM bool \`json:"one_m,omitempty"\` }`
  - `type Endpoint struct { BaseURL string \`json:"base_url"\`; AuthField string \`json:"auth_field"\`; KeyLast4 string \`json:"key_last4,omitempty"\` }`
  - `type ClaudeEndpoint struct { Endpoint; Models []ClaudeModel \`json:"models"\`; Defaults ModelSlots \`json:"defaults"\` }`
  - `type OpenAIEndpoint struct { Endpoint; Models []string \`json:"models"\`; DefaultModel string \`json:"default_model"\` }`
  - `type PresetClaudeEndpoint struct { BaseURL string \`json:"base_url"\`; AuthField string \`json:"auth_field"\`; Models []ClaudeModel \`json:"models"\`; Defaults ModelSlots \`json:"defaults"\` }`
  - `type PresetOpenAIEndpoint struct { BaseURL string \`json:"base_url"\`; AuthField string \`json:"auth_field"\`; Models []string \`json:"models"\`; DefaultModel string \`json:"default_model"\` }`
  - `type ClaudeEndpointInput struct { BaseURL string; AuthField string; Models []ClaudeModel; Key *string; Defaults ModelSlots }`
  - `type OpenAIEndpointInput struct { BaseURL string; AuthField string; Models []string; Key *string; DefaultModel string }`
  - `type Input struct { Name string; Preset string; Note string; Key *string; Claude ClaudeEndpointInput; OpenAI OpenAIEndpointInput }`

- [x] **Step 1: Write failing invariant and type tests in `hub/internal/providers/presets_test.go` and `endpoint_test.go`**

In `hub/internal/providers/presets_test.go`, update `TestPresetSeedIsWellFormed` to assert the new invariant:
Every base model with `[1m]` in `Defaults` must exist in `p.Claude.Models` and have `OneM == true`. Models in `p.Claude.Models` must not contain `[1m]`.
Add a unit test `TestClaudeModelJSONRoundTrip` in `hub/internal/providers/endpoint_test.go`.

```go
// In hub/internal/providers/endpoint_test.go:
func TestClaudeModelJSONRoundTrip(t *testing.T) {
	m := providers.ClaudeModel{Name: "glm-5.2", OneM: true}
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.Equal(t, `{"name":"glm-5.2","one_m":true}`, string(b))

	m2 := providers.ClaudeModel{Name: "glm-4.7", OneM: false}
	b2, err := json.Marshal(m2)
	require.NoError(t, err)
	require.Equal(t, `{"name":"glm-4.7"}`, string(b2))

	var decoded providers.ClaudeModel
	require.NoError(t, json.Unmarshal([]byte(`{"name":"test"}`), &decoded))
	require.Equal(t, "test", decoded.Name)
	require.False(t, decoded.OneM)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test -tags=testing ./internal/providers/...` in `hub`
Expected: FAIL with compilation errors on `ClaudeModel` / `Models` type changes.

- [x] **Step 3: Update `presets.go`, `store.go`, `store_test.go`, `endpoint_test.go`, and test helper files**

In `hub/internal/providers/presets.go`:
1. Define `ClaudeModel`.
2. Remove `Models` from `Endpoint`.
3. Add `Models []ClaudeModel` to `ClaudeEndpoint` and `Models []string` to `OpenAIEndpoint`.
4. Define `PresetClaudeEndpoint` and `PresetOpenAIEndpoint`, updating `Preset` struct.
5. Update `presets` table:
   - `zhipu`: `Claude.Models` has `glm-5.2` with `OneM: true`, `glm-5-turbo` with `OneM: false`, `glm-4.7` with `OneM: false`.
   - `zhipu_intl`: `Claude.Models` has `glm-5.2`, `glm-5-turbo`, `glm-4.7` (all `OneM: false`).
   - `kimi`: `Claude.Models` has `kimi-k3` with `OneM: true`, and `kimi-k2.7-code`, `kimi-k2.7-code-highspeed`, `kimi-k2.6` (`OneM: false`).
   - `volcengine`, `zenmux`, `minimax`, `anthropic`: `Claude.Models` with `ClaudeModel{Name: ...}` (`OneM: false`).

In `hub/internal/providers/store.go`:
1. Define `ClaudeEndpointInput` (using `Models []ClaudeModel`) and `OpenAIEndpointInput` (using `Models []string`).
2. Update `Input.Claude` to `ClaudeEndpointInput` and `Input.OpenAI` to `OpenAIEndpointInput`.
3. Update `applyAndValidate` in `store.go` to assign `ClaudeEndpoint` with `orEmptyClaude(in.Claude.Models)` and `OpenAIEndpoint` with `orEmptyStrings(in.OpenAI.Models)`.
4. Update `validateEndpointInput`: for claude take `ClaudeEndpointInput`, for openai take `OpenAIEndpointInput`.

Update helper references across tests:
- `hub/hub_test.go`
- `hub/internal/configsets/binding_test.go`
- `hub/internal/drift/adopt_test.go`
- `hub/internal/importer/service_test.go`
- `hub/internal/revisions/service_test.go`
- `hub/internal/providers/presets_test.go`
- `hub/internal/providers/store_test.go`
- `hub/internal/providers/endpoint_test.go`

- [x] **Step 4: Run Go tests and verify they pass**

Run: `go test -tags=testing ./internal/providers/...` in `hub`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/providers/ hub/hub_test.go hub/internal/configsets/binding_test.go hub/internal/drift/adopt_test.go hub/internal/importer/service_test.go hub/internal/revisions/service_test.go
git commit -m "feat(providers): define ClaudeModel and separate endpoint model definitions"
```

---

### Task 2: Migration 006 (`006_claude_models.go`) with Automatic Capability Inference

**Files:**
- Create: `hub/internal/migrations/006_claude_models.go`
- Modify: `hub/internal/migrations/migrations_test.go`

**Interfaces:**
- Consumes: PocketBase records in `providers` collection.
- Produces:
  - `func up006(app core.App) error`
  - `func down006(app core.App) error`
  - `func BackfillClaudeModels(app core.App) error`

- [x] **Step 1: Write failing migration tests in `hub/internal/migrations/migrations_test.go`**

Add tests:
1. `TestMigration006InfersOneMCapability`: Seeds provider with string models and defaults containing `[1m]`, runs 006 backfill, verifies `claude.models` contains `{Name: "glm-5.2", OneM: true}` and `{Name: "glm-4.7", OneM: false}`, and `openai.models` remains `[]string`.
2. `TestMigration006PreservesRenderedKeys`: Verify that `protocol.ProviderKeys` rendering from revision/binding is unchanged before and after.
3. `TestDown006Refuses`: Verify `down006` returns an error.

```go
func TestMigration006InfersOneMCapability(t *testing.T) {
	app := newApp(t)
	c, err := app.FindCollectionByNameOrId("providers")
	require.NoError(t, err)

	r := core.NewRecord(c)
	r.Set("name", "测试 GLM")
	r.Set("claude", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/anthropic",
		"auth_field":"ANTHROPIC_AUTH_TOKEN",
		"models":["glm-5.2","glm-4.7"],
		"defaults":{"main":"glm-5.2[1m]","opus":"glm-5.2[1m]","sonnet":"glm-5.2[1m]","haiku":"glm-4.7"}
	}`))
	r.Set("openai", json.RawMessage(`{
		"base_url":"https://open.bigmodel.cn/api/paas/v4",
		"auth_field":"OPENAI_API_KEY",
		"models":["glm-5.2","glm-4.7"],
		"default_model":"glm-5.2"
	}`))
	require.NoError(t, app.Save(r))

	require.NoError(t, migrations.BackfillClaudeModels(app))

	got, err := app.FindRecordById("providers", r.Id)
	require.NoError(t, err)

	var cl struct {
		Models []struct {
			Name string `json:"name"`
			OneM bool   `json:"one_m"`
		} `json:"models"`
	}
	require.NoError(t, got.UnmarshalJSONField("claude", &cl))
	require.Len(t, cl.Models, 2)
	require.Equal(t, "glm-5.2", cl.Models[0].Name)
	require.True(t, cl.Models[0].OneM)
	require.Equal(t, "glm-4.7", cl.Models[1].Name)
	require.False(t, cl.Models[1].OneM)

	var oa struct {
		Models []string `json:"models"`
	}
	require.NoError(t, got.UnmarshalJSONField("openai", &oa))
	require.Equal(t, []string{"glm-5.2", "glm-4.7"}, oa.Models)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test -tags=testing ./internal/migrations/... -run TestMigration006` in `hub`
Expected: FAIL (unresolved `BackfillClaudeModels` or `006_claude_models.go`).

- [x] **Step 3: Implement `hub/internal/migrations/006_claude_models.go`**

```go
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
```

- [x] **Step 4: Run migration tests and verify they pass**

Run: `go test -tags=testing ./internal/migrations/...` in `hub`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/migrations/006_claude_models.go hub/internal/migrations/migrations_test.go
git commit -m "feat(migrations): add 006 claude models migration with automatic inference"
```

---

### Task 3: Routes and Probe Endpoint Handling for `ClaudeModel`

**Files:**
- Modify: `hub/internal/routes/providers.go`
- Modify: `hub/internal/routes/providers_test.go`

**Interfaces:**
- Consumes: `providers.ClaudeModel`, `providers.Input`, `providers.ClaudeEndpointInput`.
- Produces: Updated HTTP request handlers for `/api/orciny/providers` and `/api/orciny/provider-presets`.

- [x] **Step 1: Write failing route tests in `hub/internal/routes/providers_test.go`**

Add tests:
- `TestCreateProviderWithClaudeModelPayload`: verify `claude.models` can receive `[{"name":"glm-5.2","one_m":true}]` and pass to Admin service.
- `TestProviderPresetsOutputsClaudeModels`: verify `/api/orciny/provider-presets` outputs `one_m: true` on `zhipu` / `kimi`.

- [x] **Step 2: Run test to verify it fails**

Run: `go test -tags=testing ./internal/routes/... -run TestCreateProviderWithClaudeModelPayload` in `hub`
Expected: FAIL (types mismatch or unmarshaling issue).

- [x] **Step 3: Update `hub/internal/routes/providers.go`**

Split `endpointBody` into `claudeEndpointBody` and `openAIEndpointBody`:
```go
type claudeEndpointBody struct {
	BaseURL   string                  `json:"base_url"`
	AuthField string                  `json:"auth_field"`
	Models    []providers.ClaudeModel `json:"models"`
	Key       *string                 `json:"key"`
	Defaults  providers.ModelSlots    `json:"defaults"`
}

func (b claudeEndpointBody) input() providers.ClaudeEndpointInput {
	return providers.ClaudeEndpointInput{
		BaseURL: b.BaseURL, AuthField: b.AuthField, Models: b.Models,
		Key: b.Key, Defaults: b.Defaults,
	}
}

type openAIEndpointBody struct {
	BaseURL      string   `json:"base_url"`
	AuthField    string   `json:"auth_field"`
	Models       []string `json:"models"`
	Key          *string  `json:"key"`
	DefaultModel string   `json:"default_model"`
}

func (b openAIEndpointBody) input() providers.OpenAIEndpointInput {
	return providers.OpenAIEndpointInput{
		BaseURL: b.BaseURL, AuthField: b.AuthField, Models: b.Models,
		Key: b.Key, DefaultModel: b.DefaultModel,
	}
}

type providerBody struct {
	Name      string             `json:"name"`
	Preset    string             `json:"preset"`
	Note      string             `json:"note"`
	Key       *string            `json:"key"`
	Claude    claudeEndpointBody `json:"claude"`
	OpenAI    openAIEndpointBody `json:"openai"`
	FromDrift *struct {
		Event    string `json:"event"`
		Location string `json:"location"`
		Endpoint string `json:"endpoint"`
	} `json:"from_drift"`
}
```

- [x] **Step 4: Run route tests and verify they pass**

Run: `go test -tags=testing ./internal/routes/...` in `hub`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/routes/providers.go hub/internal/routes/providers_test.go
git commit -m "feat(routes): support ClaudeModel in provider endpoints"
```

---

### Task 4: Publish Validation `one_m_unsupported` Warning

**Files:**
- Modify: `hub/internal/configsets/validate.go`
- Modify: `hub/internal/configsets/validate_test.go`

**Interfaces:**
- Consumes: `providers.Binding`, `providers.ClaudeOf(prov).Models`.
- Produces: `ProblemOneMUnsupported = "one_m_unsupported"` in `Validate(setID)`.

- [x] **Step 1: Write failing validation tests in `hub/internal/configsets/validate_test.go`**

Add tests:
- `TestValidateOneMUnsupportedWhenMarkedFalse`: Set draft binding with `glm-4.7[1m]`, provider `claude.models` has `{Name: "glm-4.7", OneM: false}`. Assert `ProblemOneMUnsupported` is produced with `Warning: true`.
- `TestValidateOneMUnsupportedQuietWhenNotInList`: Set draft binding with `custom-model[1m]`, model not in provider models list. Assert NO `ProblemOneMUnsupported`.
- `TestValidateOneMUnsupportedQuietWhenSupported`: Set draft binding with `glm-5.2[1m]`, provider models has `{Name: "glm-5.2", OneM: true}`. Assert NO `ProblemOneMUnsupported`.

```go
func TestValidateOneMUnsupportedWhenMarkedFalse(t *testing.T) {
	app, s := newService(t)
	set, err := s.Create("主力配置", "")
	require.NoError(t, err)

	provID := seedProviderWithModels(t, app, "智谱 GLM", []providers.ClaudeModel{
		{Name: "glm-5.2", OneM: true},
		{Name: "glm-4.7", OneM: false},
	})

	require.NoError(t, s.SetDraftBinding(set.Id, &providers.Binding{
		Provider: provID,
		Models: providers.ModelSlots{
			Main: "glm-4.7[1m]", Opus: "glm-4.7[1m]", Sonnet: "glm-4.7[1m]", Haiku: "glm-4.7[1m]",
		},
	}))

	problems, err := s.Validate(set.Id)
	require.NoError(t, err)

	hit := findProblem(problems, configsets.ProblemOneMUnsupported)
	require.NotNil(t, hit, "必须报 one_m_unsupported")
	require.True(t, hit.Warning, "必须是警告")
	require.Contains(t, hit.Detail, "glm-4.7")
	require.Contains(t, hit.Detail, "智谱 GLM")
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test -tags=testing ./internal/configsets/... -run TestValidateOneMUnsupported` in `hub`
Expected: FAIL (constant `ProblemOneMUnsupported` or check missing).

- [x] **Step 3: Implement `one_m_unsupported` check in `hub/internal/configsets/validate.go`**

1. Define `ProblemOneMUnsupported = "one_m_unsupported"`
2. In `validateBinding(setID, files)`:
```go
// 5. one_m_unsupported: 绑定声明了 [1m]，但 provider 标记不支持 (spec §6)
claudeModels := providers.ClaudeOf(prov).Models
modelCapability := make(map[string]bool, len(claudeModels))
for _, m := range claudeModels {
	modelCapability[m.Name] = m.OneM
}

slotChecks := []struct {
	name  string
	value string
}{
	{"main 槽", binding.Models.Main},
	{"opus 槽", binding.Models.Opus},
	{"sonnet 槽", binding.Models.Sonnet},
	{"haiku 槽", binding.Models.Haiku},
}

for _, sc := range slotChecks {
	if !providers.HasOneM(sc.value) {
		continue
	}
	base := providers.StripOneM(sc.value)
	oneM, inList := modelCapability[base]
	if inList && !oneM {
		out = append(out, Problem{
			Kind:    ProblemOneMUnsupported,
			Warning: true,
			Detail: fmt.Sprintf(
				"绑定的 %s 声明了 1M 上下文，但 %s 的模型清单里 %s 标记为不支持",
				sc.name, prov.GetString("name"), base),
		})
	}
}
```

- [x] **Step 4: Run validation tests and verify they pass**

Run: `go test -tags=testing ./internal/configsets/...` in `hub`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/configsets/validate.go hub/internal/configsets/validate_test.go
git commit -m "feat(configsets): add one_m_unsupported publish validation warning"
```

---

### Task 5: Frontend Types, Store, and `ProviderDialog` 1M Capability Checkboxes

**Files:**
- Modify: `hub/internal/site/src/types/collections.ts`
- Modify: `hub/internal/site/src/lib/api.ts`
- Modify: `hub/internal/site/src/stores/providers.ts`
- Modify: `hub/internal/site/src/stores/providers.test.ts`
- Modify: `hub/internal/site/src/components/ProviderDialog.tsx`
- Modify: `hub/internal/site/src/components/ProviderDialog.test.tsx`

**Interfaces:**
- Consumes: `ClaudeModel` `{ name: string, one_m?: boolean }`.
- Produces: `ProviderDialog` editing Claude model list with per-row 1M checkbox, probe adopt defaulting `one_m: false`.

- [x] **Step 1: Write failing frontend tests in `hub/internal/site/src/components/ProviderDialog.test.tsx`**

Add tests:
- `勾选「支持 1M」写进 models[i].one_m`: Check that clicking checkbox next to a Claude model toggles `one_m: true` in submitted payload.
- `探测合并回来的模型 one_m=false`: Check that adopting probed models sets `one_m: false`.

- [x] **Step 2: Run test to verify it fails**

Run: `npm test src/components/ProviderDialog.test.tsx` in `hub/internal/site`
Expected: FAIL

- [x] **Step 3: Update `types/collections.ts`, `lib/api.ts`, `stores/providers.ts`, and `components/ProviderDialog.tsx`**

In `hub/internal/site/src/types/collections.ts`:
```ts
export interface ClaudeModel {
  name: string
  one_m?: boolean
}

export interface ClaudeEndpointRecord {
  base_url: string
  auth_field: string
  key_last4?: string
  models: ClaudeModel[] | null
  defaults: ModelSlots | null
}
```
Update `PresetClaudeEndpoint` and `ClaudeEndpointBody` accordingly.

In `hub/internal/site/src/components/ProviderDialog.tsx`:
1. `EndpointDraft` for Claude uses `models: ClaudeModel[]`, for OpenAI uses `models: string[]`.
2. Split `ModelList` into `ClaudeModelList` (with `[支持 1M ☐]` checkbox and `[×]` per row) and `OpenAIModelList` (tag chips with `[×]`).
3. For probed models adoption in Claude endpoint, convert `probe.models.map(m => ({ name: m, one_m: false }))`.
4. In `claudeModelOptions`: `claude.models.map(m => m.name)`.

- [x] **Step 4: Run frontend tests and verify they pass**

Run: `npm test src/components/ProviderDialog.test.tsx src/stores/providers.test.ts` in `hub/internal/site`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/site/src/types/collections.ts hub/internal/site/src/lib/api.ts hub/internal/site/src/stores/providers.ts hub/internal/site/src/stores/providers.test.ts hub/internal/site/src/components/ProviderDialog.tsx hub/internal/site/src/components/ProviderDialog.test.tsx
git commit -m "feat(ui): add 1M capability checkboxes in ProviderDialog"
```

---

### Task 6: Pure Function `diffAgainstHead` and `draftState` Initial Value Fix

**Files:**
- Modify: `hub/internal/site/src/lib/draftState.ts`
- Modify: `hub/internal/site/src/lib/draftState.test.ts`
- Modify: `hub/internal/site/src/pages/ConfigSetDetail.tsx`

**Interfaces:**
- Consumes: `ConfigSetRecord` with `draft`, `draft_binding`, and `expand.head`.
- Produces: `export function diffAgainstHead(set: ConfigSetRecord | null | undefined): { files: boolean; binding: boolean }`.

- [x] **Step 1: Write failing tests in `hub/internal/site/src/lib/draftState.test.ts`**

Add tests for `diffAgainstHead`:
- Empty head (never published): `files` true if draft has files, `binding` true if draft has binding.
- Only files changed: returns `{ files: true, binding: false }`.
- Only binding changed: returns `{ files: false, binding: true }`.
- Both changed: returns `{ files: true, binding: true }`.
- Files in different order but same path/hash/mode: returns `{ files: false, binding: false }`.
- Binding with same provider and slots: returns `{ files: false, binding: false }`.

- [x] **Step 2: Run test to verify it fails**

Run: `npm test src/lib/draftState.test.ts` in `hub/internal/site`
Expected: FAIL (`diffAgainstHead` not defined).

- [x] **Step 3: Implement `diffAgainstHead` in `hub/internal/site/src/lib/draftState.ts` and fix initial `draftState` in `ConfigSetDetail.tsx`**

In `hub/internal/site/src/lib/draftState.ts`:
```ts
import type { ConfigSetRecord, FileEntry, Binding } from '@/types/collections'

export function diffAgainstHead(set: ConfigSetRecord | null | undefined): {
  files: boolean
  binding: boolean
} {
  if (!set) return { files: false, binding: false }
  const head = set.expand?.head

  if (!head) {
    const hasFiles = Boolean(set.draft && set.draft.length > 0)
    const hasBinding = Boolean(set.draft_binding && set.draft_binding.provider)
    return { files: hasFiles, binding: hasBinding }
  }

  const draftFiles = [...(set.draft ?? [])].sort((a, b) => a.path.localeCompare(b.path))
  const headFiles = [...(head.files ?? [])].sort((a, b) => a.path.localeCompare(b.path))

  let filesDiff = draftFiles.length !== headFiles.length
  if (!filesDiff) {
    for (let i = 0; i < draftFiles.length; i++) {
      if (
        draftFiles[i].path !== headFiles[i].path ||
        draftFiles[i].hash !== headFiles[i].hash ||
        draftFiles[i].mode !== headFiles[i].mode
      ) {
        filesDiff = true
        break
      }
    }
  }

  const b1: Binding | null = set.draft_binding
  const b2: Binding | null = head.binding

  let bindingDiff = false
  const p1 = b1?.provider ?? ''
  const p2 = b2?.provider ?? ''
  if (p1 !== p2) {
    bindingDiff = true
  } else if (p1 !== '') {
    const s1 = b1?.models ?? { main: '', opus: '', sonnet: '', haiku: '' }
    const s2 = b2?.models ?? { main: '', opus: '', sonnet: '', haiku: '' }
    if (
      s1.main !== s2.main ||
      s1.opus !== s2.opus ||
      s1.sonnet !== s2.sonnet ||
      s1.haiku !== s2.haiku
    ) {
      bindingDiff = true
    }
  }

  return { files: filesDiff, binding: bindingDiff }
}
```

In `hub/internal/site/src/pages/ConfigSetDetail.tsx`:
Update `draftState` initialization from `diffAgainstHead(set)`:
```ts
const isDirty = useMemo(() => {
  const diff = diffAgainstHead(set)
  return diff.files || diff.binding
}, [set])

useEffect(() => {
  if (isDirty) setDraftState((s) => (s === 'clean' ? 'dirty' : s))
}, [isDirty])
```

- [x] **Step 4: Run test to verify it passes**

Run: `npm test src/lib/draftState.test.ts` in `hub/internal/site`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/site/src/lib/draftState.ts hub/internal/site/src/lib/draftState.test.ts hub/internal/site/src/pages/ConfigSetDetail.tsx
git commit -m "fix(draftState): add diffAgainstHead and initialize draftState from server state"
```

---

### Task 7: Toast Notification Store & Global `Toast` Component

**Files:**
- Create: `hub/internal/site/src/stores/toast.ts`
- Create: `hub/internal/site/src/components/Toast.tsx`
- Create: `hub/internal/site/src/components/Toast.test.tsx`
- Modify: `hub/internal/site/src/components/Shell.tsx`

**Interfaces:**
- Consumes: Nanostores `atom`.
- Produces: `$toast`, `showToast({ message, actionLabel?, onAction?, durationMs? })`, `hideToast()`, `<Toast />`.

- [x] **Step 1: Write failing tests in `hub/internal/site/src/components/Toast.test.tsx`**

Test scenarios:
1. `showToast` displays toast with message.
2. Clicking action button executes `onAction` and hides toast.
3. Automatically hides after duration timeout.

- [x] **Step 2: Run test to verify it fails**

Run: `npm test src/components/Toast.test.tsx` in `hub/internal/site`
Expected: FAIL

- [x] **Step 3: Implement `hub/internal/site/src/stores/toast.ts` and `hub/internal/site/src/components/Toast.tsx`, integrate into `Shell.tsx`**

In `hub/internal/site/src/stores/toast.ts`:
```ts
import { atom } from 'nanostores'

export interface ToastItem {
  id: string
  message: string
  actionLabel?: string
  onAction?: () => void
  durationMs?: number
}

export const $toast = atom<ToastItem | null>(null)

let timer: ReturnType<typeof setTimeout> | null = null

export function showToast(toast: Omit<ToastItem, 'id'>) {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  const id = Math.random().toString(36).slice(2)
  $toast.set({ ...toast, id })
  const duration = toast.durationMs ?? 8000
  timer = setTimeout(() => {
    if ($toast.get()?.id === id) {
      $toast.set(null)
    }
  }, duration)
}

export function hideToast() {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  $toast.set(null)
}
```

In `hub/internal/site/src/components/Toast.tsx`:
```tsx
import { useStore } from '@nanostores/react'
import { X } from 'lucide-react'
import { $toast, hideToast } from '@/stores/toast'

export function Toast() {
  const toast = useStore($toast)
  if (!toast) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-4 right-4 z-50 flex items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-lg text-sm text-ink1"
    >
      <span>{toast.message}</span>
      {toast.actionLabel && toast.onAction && (
        <button
          type="button"
          className="rounded bg-accent px-2 py-1 text-xs text-white font-medium hover:opacity-90"
          onClick={() => {
            toast.onAction?.()
            hideToast()
          }}
        >
          {toast.actionLabel}
        </button>
      )}
      <button
        type="button"
        aria-label="关闭提示"
        className="text-ink3 hover:text-ink1"
        onClick={hideToast}
      >
        <X size={14} />
      </button>
    </div>
  )
}
```

Mount `<Toast />` inside `hub/internal/site/src/components/Shell.tsx`.

- [x] **Step 4: Run toast tests and verify they pass**

Run: `npm test src/components/Toast.test.tsx` in `hub/internal/site`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/site/src/stores/toast.ts hub/internal/site/src/components/Toast.tsx hub/internal/site/src/components/Toast.test.tsx hub/internal/site/src/components/Shell.tsx
git commit -m "feat(ui): add global Toast notification store and component"
```

---

### Task 8: `ModelQuickSwitch` Component & `ConfigSets` List Page Integration

**Files:**
- Create: `hub/internal/site/src/components/ModelQuickSwitch.tsx`
- Create: `hub/internal/site/src/components/ModelQuickSwitch.test.tsx`
- Modify: `hub/internal/site/src/stores/configsets.ts`
- Modify: `hub/internal/site/src/pages/ConfigSets.tsx`

**Interfaces:**
- Consumes: `ConfigSetRecord`, `ProviderRecord[]`, `showToast`, `setBinding`, `validateConfigSet`, `publishConfigSet`, `rollbackConfigSet`, `diffAgainstHead`.
- Produces: Inline model quick switcher in `ConfigSets.tsx` with guardrails §5.1 & §5.2.

- [x] **Step 1: Write failing tests in `hub/internal/site/src/components/ModelQuickSwitch.test.tsx`**

Test scenarios:
1. Normal switch: selecting a model calls `setBinding` and `publishConfigSet` with auto note `快切 · <Provider> › <Model>`, without opening `PublishDialog`.
2. Selecting 1M model includes `[1m]`, selecting non-1M does not include `[1m]`.
3. Validation blocking issues opens `PublishDialog` without publishing.
4. Guard §5.1: When draft has file modifications (`diff.files === true`), renders `有未发布改动 →` and disables quick-switch.
5. Guard §5.2: When slots are split (`stripOneM` on 4 slots not all equal), renders `已分设 →` and disables quick-switch.
6. Undo: Clicking undo button on toast calls `rollbackConfigSet` passing previous head revision.

- [x] **Step 2: Run test to verify it fails**

Run: `npm test src/components/ModelQuickSwitch.test.tsx` in `hub/internal/site`
Expected: FAIL

- [x] **Step 3: Implement `hub/internal/site/src/components/ModelQuickSwitch.tsx` and integrate into `ConfigSets.tsx`**

Implement `ModelQuickSwitch.tsx`:
```tsx
import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { fillAllSlots, isPassthrough, setOneM, stripOneM } from '@/lib/binding'
import { diffAgainstHead } from '@/lib/draftState'
import { publishConfigSet, rollbackConfigSet, setBinding, validateConfigSet } from '@/lib/api'
import { showToast } from '@/stores/toast'
import { reloadConfigSet } from '@/stores/configsets'
import { PublishDialog } from '@/components/PublishDialog'
import { navigate } from '@/router'
import type { ConfigSetRecord, ProviderRecord } from '@/types/collections'

export function isSlotsSplit(slots: { main: string; opus: string; sonnet: string; haiku: string }): boolean {
  if (isPassthrough(slots)) return false
  const m = stripOneM(slots.main)
  const o = stripOneM(slots.opus)
  const s = stripOneM(slots.sonnet)
  const h = stripOneM(slots.haiku)
  return !(m === o && o === s && s === h)
}

export function ModelQuickSwitch({
  set,
  providers,
  affectedCount,
}: {
  set: ConfigSetRecord
  providers: ProviderRecord[]
  affectedCount: number
}) {
  const { t } = useLingui()
  const [busy, setBusy] = useState(false)
  const [showPublishDialog, setShowPublishDialog] = useState(false)

  const diff = diffAgainstHead(set)
  const currentBinding = set.draft_binding ?? set.expand?.head?.binding
  const slots = currentBinding?.models ?? { main: '', opus: '', sonnet: '', haiku: '' }

  // Guard 5.1: 草稿有文件改动
  if (diff.files) {
    return (
      <button
        type="button"
        onClick={() => navigate('configsets', set.id)}
        className="text-xs text-accent hover:underline"
      >
        <Trans>有未发布改动 →</Trans>
      </button>
    )
  }

  // Guard 5.2: 四槽已分设
  if (isSlotsSplit(slots)) {
    return (
      <button
        type="button"
        onClick={() => navigate('configsets', set.id)}
        className="text-xs text-accent hover:underline"
      >
        <Trans>已分设 →</Trans>
      </button>
    )
  }

  const currentProviderId = currentBinding?.provider ?? ''
  const currentMainBase = stripOneM(slots.main)
  const currentValue = currentProviderId && currentMainBase ? `${currentProviderId}:${currentMainBase}` : ''

  async function handleSelect(val: string) {
    if (val === currentValue) return
    setBusy(true)
    const prevHead = set.expand?.head?.id ?? set.head

    try {
      let nextBinding = null
      let note = t`快切 · 透传`
      let modelLabel = t`透传`

      if (val !== '') {
        const [pId, modelName] = val.split(':')
        const prov = providers.find((p) => p.id === pId)
        const claudeModel = prov?.claude?.models?.find((m) => m.name === modelName)
        const oneM = claudeModel?.one_m ?? false
        nextBinding = {
          provider: pId,
          models: fillAllSlots(setOneM(modelName, oneM)),
        }
        note = t`快切 · ${prov?.name ?? pId} › ${modelName}`
        modelLabel = modelName
      }

      await setBinding(set.id, nextBinding)
      const problems = (await validateConfigSet(set.id)) ?? []
      const blocking = problems.filter((p) => !p.warning)

      if (blocking.length > 0) {
        setShowPublishDialog(true)
        return
      }

      await publishConfigSet(set.id, note)
      await reloadConfigSet(set.id)

      showToast({
        message: t`已切到 ${modelLabel} · 影响 ${affectedCount} 台`,
        actionLabel: t`撤销`,
        onAction: async () => {
          if (prevHead) {
            await rollbackConfigSet(set.id, prevHead)
            await reloadConfigSet(set.id)
            showToast({ message: t`已撤销切换` })
          }
        },
      })
    } catch (e) {
      showToast({ message: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }

  const usableProviders = providers.filter((p) => Boolean(p.claude?.base_url))

  return (
    <>
      <select
        aria-label={t`模型快切`}
        disabled={busy}
        value={currentValue}
        onChange={(e) => void handleSelect(e.target.value)}
        className="rounded border border-line bg-wash px-2 py-1 font-mono text-xs text-ink1"
      >
        <option value="">{t`透传（不指定模型）`}</option>
        {usableProviders.map((p) => (
          <optgroup key={p.id} label={p.name}>
            {(p.claude?.models ?? []).map((m) => (
              <option key={m.name} value={`${p.id}:${m.name}`}>
                {m.name}
                {m.one_m ? ' · 1M' : ''}
              </option>
            ))}
          </optgroup>
        ))}
      </select>

      {showPublishDialog && (
        <PublishDialog
          setId={set.id}
          draftState="dirty"
          affectedMachines={affectedCount}
          onClose={() => setShowPublishDialog(false)}
          onPublished={() => {
            setShowPublishDialog(false)
            void reloadConfigSet(set.id)
          }}
        />
      )}
    </>
  )
}
```

In `hub/internal/site/src/pages/ConfigSets.tsx`:
Subscribe to `$providers` and assignments count, and render `<ModelQuickSwitch />` and `影响 N 台` in each list row.

- [x] **Step 4: Run tests and verify they pass**

Run: `npm test src/components/ModelQuickSwitch.test.tsx` in `hub/internal/site`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/site/src/components/ModelQuickSwitch.tsx hub/internal/site/src/components/ModelQuickSwitch.test.tsx hub/internal/site/src/stores/configsets.ts hub/internal/site/src/pages/ConfigSets.tsx
git commit -m "feat(ui): implement ModelQuickSwitch and integrate into ConfigSets page"
```

---

### Task 9: `BindingBar` Capability-Aware 1M & `ConfigSetDetail` Publish Dialog Bypass

**Files:**
- Modify: `hub/internal/site/src/components/BindingBar.tsx`
- Modify: `hub/internal/site/src/components/BindingBar.test.tsx`
- Modify: `hub/internal/site/src/pages/ConfigSetDetail.tsx`

**Interfaces:**
- Consumes: `provider.claude.models` (`ClaudeModel[]`), `diffAgainstHead`.
- Produces:
  - `BindingBar` disables 1M checkbox if selected model is in models list and `one_m === false`.
  - Selecting model sets 1M according to model capability.
  - Detail page skips dialog if only binding changed.

- [x] **Step 1: Write failing tests in `hub/internal/site/src/components/BindingBar.test.tsx`**

Test scenarios:
1. Selecting model with `one_m: false` disables 1M checkbox and clears `[1m]`.
2. Selecting model with `one_m: true` enables and checks 1M.
3. Model not in list does not disable 1M checkbox.

- [x] **Step 2: Run test to verify it fails**

Run: `npm test src/components/BindingBar.test.tsx` in `hub/internal/site`
Expected: FAIL

- [x] **Step 3: Update `BindingBar.tsx` and `ConfigSetDetail.tsx`**

In `hub/internal/site/src/components/BindingBar.tsx`:
```tsx
const claudeModels = provider?.claude?.models ?? []
const currentModel = claudeModels.find((m) => m.name === mainBase)
const oneMDisabled = mainBase === '' || (currentModel !== undefined && currentModel.one_m === false)
```
When selecting main model:
```tsx
onChange={(e) => {
  const val = e.target.value
  const target = claudeModels.find((m) => m.name === val)
  const targetOneM = target ? Boolean(target.one_m) : false
  onChange({ ...binding, models: fillAllSlots(setOneM(val, targetOneM)) })
}}
```

In `hub/internal/site/src/pages/ConfigSetDetail.tsx`:
In `openPublish`:
```tsx
const diff = diffAgainstHead(set)
if (!diff.files && diff.binding) {
  // Direct publish
  const problems = (await validateConfigSet(set.id)) ?? []
  const blocking = problems.filter((p) => !p.warning)
  if (blocking.length === 0) {
    const prevHead = set.expand?.head?.id ?? set.head
    await publishConfigSet(set.id, t`更新服务绑定`)
    await reloadConfigSet(set.id)
    setDraftState('clean')
    showToast({
      message: t`已发布服务绑定 · 影响 ${affected} 台`,
      actionLabel: t`撤销`,
      onAction: async () => {
        if (prevHead) {
          await rollbackConfigSet(set.id, prevHead)
          await reloadConfigSet(set.id)
          showToast({ message: t`已撤销切换` })
        }
      },
    })
    return
  }
}
setShowPublish(true)
```

- [x] **Step 4: Run tests and verify they pass**

Run: `npm test src/components/BindingBar.test.tsx` in `hub/internal/site`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add hub/internal/site/src/components/BindingBar.tsx hub/internal/site/src/components/BindingBar.test.tsx hub/internal/site/src/pages/ConfigSetDetail.tsx
git commit -m "feat(ui): update BindingBar 1M capability handling and optimize Detail page publish"
```

---

### Task 10: `Machines` Read-Only Model Column, i18n, and Full Verification

**Files:**
- Modify: `hub/internal/site/src/pages/Machines.tsx`
- Modify: `hub/internal/site/src/locales/zh.po` / `hub/internal/site/src/locales/en.po`

**Interfaces:**
- Consumes: `$machines`, `$configSets`, `$providers`, `assignments`.
- Produces: Read-only model column on machines table with link to config set.

- [x] **Step 1: Write/update tests for Machines page**

Verify machine table displays model name (e.g. `智谱 GLM · glm-5.2` or `未绑定` or `透传` or `—`).

- [x] **Step 2: Update `hub/internal/site/src/pages/Machines.tsx`**

Add "模型" column:
```tsx
<th className="py-2 font-normal"><Trans>模型</Trans></th>
```
In table row:
Resolve machine's assignment -> config set -> head binding -> model text.
Clicking model text navigates to `navigate('configsets', configSetId)`.

- [x] **Step 3: Run i18n extraction / compilation and all tests**

Run:
```bash
cd hub && go test -tags=testing ./...
cd internal/site && npm test && npm run build
```
Expected: All Go and frontend tests pass, build succeeds.

- [x] **Step 4: Commit**

```bash
git add hub/internal/site/src/pages/Machines.tsx hub/internal/site/src/locales/
git commit -m "feat(ui): add read-only model column to Machines table and update i18n"
```
