// Package providers 管 AI 服务配置（M1.5 spec §2）。
//
// 它与 credentials 是同一类东西——被配置集引用的资源，不是配置本身。
// key 不另存：服务配置里的 API key 就是一条 credential。
package providers

// 鉴权字段名。auth_field 决定的是 env 的**键名**，占位符替的是**值**，
// 替不了键名——所以换绑到 auth_field 不同的 Provider 时，settings.json 里
// 那行键名会对不上。方案是发布校验报错 + 一键修复（spec §3.3），
// 不自动改写用户文件。
const (
	AuthToken  = "ANTHROPIC_AUTH_TOKEN"
	AuthAPIKey = "ANTHROPIC_API_KEY"
)

// ModelSlots 是四个模型槽。四空 = 透传模式。
//
// 为什么是四槽而非单槽（spec §2.3 的实证依据，不是猜测）：对 cc-switch 的
// 69 个 Claude 预设统计，34 个设模型变量且恒为四个一起设，其余 35 个一个
// 都不设。原因是 Claude Code 会自己去要 haiku 做标题生成一类的轻量活，
// 只钉主模型会让它拿 claude-haiku-* 去打人家的 endpoint。
type ModelSlots struct {
	Main   string `json:"main"`
	Opus   string `json:"opus"`
	Sonnet string `json:"sonnet"`
	Haiku  string `json:"haiku"`
}

func (m ModelSlots) Empty() bool {
	return m.Main == "" && m.Opus == "" && m.Sonnet == "" && m.Haiku == ""
}

func (m ModelSlots) Full() bool {
	return m.Main != "" && m.Opus != "" && m.Sonnet != "" && m.Haiku != ""
}

// Binding 是配置集的服务绑定：{provider, models{main,opus,sonnet,haiku}}。
//
// **单数，不是数组**（spec §1.3）。多绑定的位置留给以后的迁移，现在不预留
// 结构——一个只可能有一个元素的 map 会诱使实现去支持它。
type Binding struct {
	Provider string     `json:"provider"` // providers 记录 id
	Models   ModelSlots `json:"models"`
}

// Preset 是内置平台条目。编译进二进制、只读（spec §2.3）。
type Preset struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	BaseURL    string     `json:"base_url"`
	AuthField  string     `json:"auth_field"`
	Models     []string   `json:"models"`
	Defaults   ModelSlots `json:"defaults"`
	WebsiteURL string     `json:"website_url,omitempty"`
	APIKeyURL  string     `json:"api_key_url,omitempty"`
	Icon       string     `json:"icon,omitempty"`
	IconColor  string     `json:"icon_color,omitempty"`

	// —— 订阅域预留（spec §9），本期只填数据不消费 ——
	// 预设表是服务配置与订阅采集器的公共接缝：一个平台条目同时携带
	// BaseURL/Models（喂服务配置）与 CollectorType/CollectorMode（喂采集器）。
	// M2 做采集器时只需给新表加一个可选 relation，不用回头返工服务配置。
	CollectorType string `json:"collector_type,omitempty"` // "zhipu" | "kimi" | ... | "" 表示无余额接口
	CollectorMode string `json:"collector_mode,omitempty"` // "fetch" | "local" | ""
}

// presets 是种子表。加一条就是加一行——长尾随用随加。
//
// base_url 的路径段与模型 id 各家都在变，本表按 2026-08 各平台的 Claude Code
// 接入文档逐条核过。它是编译期常量、跟 hub 版本走；过时了用户可以建一条
// preset 为空的自定义 Provider 随时绕过（spec §13），因此不做在线更新。
var presets = []Preset{
	{
		ID:        "zhipu",
		Name:      "智谱 GLM",
		BaseURL:   "https://open.bigmodel.cn/api/anthropic",
		AuthField: AuthToken,
		// [1m] 后缀开 100 万上下文窗口，官方文档同时要求把
		// CLAUDE_CODE_AUTO_COMPACT_WINDOW 调到 1000000。
		Models: []string{"glm-5.2[1m]", "glm-5.2", "glm-5-turbo", "glm-4.7"},
		Defaults: ModelSlots{
			Main: "glm-5.2[1m]", Opus: "glm-5.2[1m]",
			Sonnet: "glm-5.2[1m]", Haiku: "glm-4.7",
		},
		WebsiteURL:    "https://open.bigmodel.cn",
		APIKeyURL:     "https://open.bigmodel.cn/usercenter/apikeys",
		Icon:          "zhipu",
		IconColor:     "#3859FF",
		CollectorType: "zhipu",
		CollectorMode: "fetch",
	},
	{
		ID:        "zhipu_intl",
		Name:      "Z.ai (GLM International)",
		BaseURL:   "https://api.z.ai/api/anthropic",
		AuthField: AuthToken,
		Models:    []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
		Defaults: ModelSlots{
			Main: "glm-5.2", Opus: "glm-5.2", Sonnet: "glm-5.2", Haiku: "glm-4.7",
		},
		WebsiteURL:    "https://z.ai",
		APIKeyURL:     "https://z.ai/manage-apikey/apikey-list",
		Icon:          "zhipu",
		IconColor:     "#3859FF",
		CollectorType: "zhipu",
		CollectorMode: "fetch",
	},
	{
		ID:        "kimi",
		Name:      "Kimi (Moonshot)",
		BaseURL:   "https://api.moonshot.cn/anthropic",
		AuthField: AuthToken,
		Models: []string{
			"kimi-k3[1m]", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6",
		},
		Defaults: ModelSlots{
			Main: "kimi-k3[1m]", Opus: "kimi-k3[1m]",
			Sonnet: "kimi-k3[1m]", Haiku: "kimi-k3[1m]",
		},
		WebsiteURL:    "https://platform.moonshot.cn",
		APIKeyURL:     "https://platform.moonshot.cn/console/api-keys",
		Icon:          "kimi",
		IconColor:     "#000000",
		CollectorType: "kimi",
		CollectorMode: "fetch",
	},
	{
		ID:      "volcengine",
		Name:    "火山方舟 Coding Plan",
		BaseURL: "https://ark.cn-beijing.volces.com/api/coding",
		// 注意：/api/v3 是 OpenAI 协议口，/api/coding 才是 Anthropic 协议口。
		// 用错会走计费不同的通道。
		AuthField: AuthToken,
		Models: []string{
			"doubao-seed-code-preview-latest", "doubao-seed-2.0-code",
			"deepseek-v3.2", "glm-4.7", "kimi-k2.5",
		},
		Defaults: ModelSlots{
			Main: "doubao-seed-code-preview-latest", Opus: "doubao-seed-code-preview-latest",
			Sonnet: "doubao-seed-code-preview-latest", Haiku: "doubao-seed-code-preview-latest",
		},
		WebsiteURL:    "https://www.volcengine.com/product/ark",
		APIKeyURL:     "https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey",
		Icon:          "volcengine",
		IconColor:     "#00D0DC",
		CollectorType: "volcengine",
		CollectorMode: "fetch",
	},
	{
		ID:        "zenmux",
		Name:      "ZenMux",
		BaseURL:   "https://zenmux.ai/api/anthropic",
		AuthField: AuthToken,
		// Claude 系用别名（能开 1M 上下文与 effort 控制），
		// 其余厂商用 `厂商/模型` 全 id。
		Models: []string{
			"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5",
			"openai/gpt-5.2", "google/gemini-3-pro-preview", "volcengine/doubao-seed-code",
		},
		Defaults: ModelSlots{
			Main: "claude-sonnet-4-6", Opus: "claude-opus-4-7",
			Sonnet: "claude-sonnet-4-6", Haiku: "claude-haiku-4-5",
		},
		WebsiteURL: "https://zenmux.ai",
		APIKeyURL:  "https://zenmux.ai/settings/keys",
		Icon:       "zenmux",
		IconColor:  "#6D28D9",
	},
	{
		ID:      "minimax",
		Name:    "MiniMax",
		BaseURL: "https://api.minimax.io/anthropic",
		// 国内站是 https://api.minimaxi.com/anthropic，建自定义 Provider 即可。
		AuthField: AuthToken,
		Models:    []string{"MiniMax-M2"},
		Defaults: ModelSlots{
			Main: "MiniMax-M2", Opus: "MiniMax-M2",
			Sonnet: "MiniMax-M2", Haiku: "MiniMax-M2",
		},
		WebsiteURL: "https://platform.minimax.io",
		APIKeyURL:  "https://platform.minimax.io/user-center/basic-information/interface-key",
		Icon:       "minimax",
		IconColor:  "#F23F5D",
	},
	{
		ID:        "anthropic",
		Name:      "Anthropic 官方",
		BaseURL:   "https://api.anthropic.com",
		AuthField: AuthAPIKey,
		Models: []string{
			"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-haiku-4-5-20251001",
		},
		// 透传：四槽留空，让 Claude Code 用它自己的默认模型。
		// 那 35 个中转预设就是这么干的（spec §2.3）。
		Defaults:   ModelSlots{},
		WebsiteURL: "https://www.anthropic.com",
		APIKeyURL:  "https://console.anthropic.com/settings/keys",
		Icon:       "anthropic",
		IconColor:  "#D97757",
	},
}

// Presets 返回种子表的副本。调用方改它不影响下一次调用。
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
