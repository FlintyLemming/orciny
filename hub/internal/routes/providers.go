package routes

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/providers"
)

// providerBody 是新建 / 更新服务配置的请求体。
type providerBody struct {
	Name       string               `json:"name"`
	Preset     string               `json:"preset"`
	BaseURL    string               `json:"base_url"`
	AuthField  string               `json:"auth_field"`
	Credential string               `json:"credential"`
	Models     []string             `json:"models"`
	Defaults   providers.ModelSlots `json:"defaults"`
	Note       string               `json:"note"`
}

func (b providerBody) input() providers.Input {
	return providers.Input{
		Name: b.Name, Preset: b.Preset, BaseURL: b.BaseURL,
		AuthField: b.AuthField, Credential: b.Credential,
		Models: b.Models, Defaults: b.Defaults, Note: b.Note,
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
	id, err := d.Admin.CreateProvider(req.input())
	if err != nil {
		return mapErr(e, err)
	}
	return e.JSON(http.StatusOK, map[string]any{"id": id})
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
