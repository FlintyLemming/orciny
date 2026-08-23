package routes

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// endpointBody 是请求体里的一个协议端点。
//
// Key 用 *string 承载三态（M1.6 spec §5.2）：JSON 里字段缺席 = 不修改，
// 显式的 "" = 清空，非空 = 替换。两种意图在 wire 上就分得开。
type endpointBody struct {
	BaseURL      string               `json:"base_url"`
	AuthField    string               `json:"auth_field"`
	Models       []string             `json:"models"`
	Key          *string              `json:"key"`
	Defaults     providers.ModelSlots `json:"defaults"`
	DefaultModel string               `json:"default_model"`
}

func (b endpointBody) input() providers.EndpointInput {
	return providers.EndpointInput{
		BaseURL: b.BaseURL, AuthField: b.AuthField, Models: b.Models,
		Key: b.Key, Defaults: b.Defaults, DefaultModel: b.DefaultModel,
	}
}

// providerBody 是新建 / 更新服务配置的请求体。
type providerBody struct {
	Name   string       `json:"name"`
	Preset string       `json:"preset"`
	Note   string       `json:"note"`
	Key    *string      `json:"key"` // 平台级，三态同 endpointBody.Key
	Claude endpointBody `json:"claude"`
	OpenAI endpointBody `json:"openai"`

	// FromDrift 非空时先把漂移里的 key 取出来内联进 provider（M1.6 spec §5.5）。
	FromDrift *struct {
		Event    string `json:"event"`
		Location string `json:"location"`
		Endpoint string `json:"endpoint"`
	} `json:"from_drift"`
}

func (b providerBody) input() providers.Input {
	return providers.Input{
		Name: b.Name, Preset: b.Preset, Note: b.Note, Key: b.Key,
		Claude: b.Claude.input(), OpenAI: b.OpenAI.input(),
	}
}

func (d Deps) providerPresets(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	return e.JSON(http.StatusOK, d.Admin.ProviderPresets())
}

func (d Deps) createProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providerBody
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	if req.FromDrift != nil {
		id, err := d.Admin.CreateProviderFromDrift(
			req.FromDrift.Event, req.FromDrift.Location, req.FromDrift.Endpoint, req.input())
		if err != nil {
			return mapErr(e, err)
		}
		return e.JSON(http.StatusOK, map[string]any{"id": id})
	}
	id, err := d.Admin.CreateProvider(req.input())
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"id": id})
}

func (d Deps) bindingMatch(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	m, err := d.Admin.MatchBindingDrift(e.Request.PathValue("id"))
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, m)
}

func (d Deps) rebindDrift(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req struct {
		Provider string `json:"provider"`
	}
	if err := e.BindBody(&req); err != nil || req.Provider == "" {
		return e.BadRequestError("需要 provider", nil)
	}
	revID, err := d.Admin.RebindFromDrift(e.Request.PathValue("id"), req.Provider)
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"revision": revID})
}

func (d Deps) updateProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providerBody
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	if err := d.Admin.UpdateProvider(e.Request.PathValue("id"), req.input()); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) deleteProvider(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.DeleteProvider(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

// setBinding 设置或清空草稿绑定。provider 为空串即解绑——
// 与「设置」同一个端点，不另开 DELETE：前端只有一个下拉框，
// 「选空」与「选别的」是同一个动作。
func (d Deps) setBinding(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	var req providers.Binding
	if err := e.BindBody(&req); err != nil {
		return e.BadRequestError("请求体格式错误", nil)
	}
	var b *providers.Binding
	if req.Provider != "" {
		b = &req
	}
	if err := d.Admin.SetBinding(e.Request.PathValue("id"), b); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}

func (d Deps) fixAuthField(e *core.RequestEvent) error {
	if d.Admin == nil {
		return e.InternalServerError("管理服务未就绪", nil)
	}
	if err := d.Admin.FixAuthField(e.Request.PathValue("id")); err != nil {
		return mapErr(e, err)
	}
	return e.NoContent(http.StatusNoContent)
}
