// Package providers 管 AI 服务配置（M1.6 spec §2）。
//
// 一条记录 = **一家平台**，内含 claude 与 openai 两个协议端点。
// API key 直接落在这条记录上（平台级一把，端点可单独覆盖）：
// 加密落库、末四位回显、启动自检这套复用降在代码层（secretbox 包），
// 不再做成用户可见的 credentials 实体（spec §1.1 第二条）。
package providers

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

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

// OneMSuffix 是 Claude Code 的 100 万上下文声明。它是模型名字符串上的语法
// （Claude Code 侧按 /\[1m\]/i 匹配后把上下文窗口按 1e6 计），不是一个独立
// 的模型——所以它只出现在 ModelSlots 里，模型清单只列基名。
const OneMSuffix = "[1m]"

// HasOneM 判断模型名是否带 1M 声明。大小写不敏感，与 Claude Code 一致。
func HasOneM(model string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimRight(model, " ")), OneMSuffix)
}

// StripOneM 去掉尾部的 1M 声明，返回模型基名。不带声明时原样返回。
func StripOneM(model string) string {
	trimmed := strings.TrimRight(model, " ")
	if !HasOneM(trimmed) {
		return model
	}
	return strings.TrimRight(trimmed[:len(trimmed)-len(OneMSuffix)], " ")
}

// 端点名。与占位符的端点段（protocol.ProviderKeys）逐字符一致。
const (
	EndpointClaude = "claude"
	EndpointOpenAI = "openai"
)

// DefaultOpenAIAuthField 是 openai 端点的默认鉴权字段名。
//
// 它是**自由文本，不是枚举**（spec §2.4）：auth_field 的二选一枚举是
// Claude Code 的 settings.json env 键名问题；openai 侧本期没有消费者，
// 也就没有依据去定枚举。一个默认 OPENAI_API_KEY 的文本字段既够用，
// 又不会在接 Codex 时挡路。
const DefaultOpenAIAuthField = "OPENAI_API_KEY"

// Endpoint 是两个协议端点的公共部分。
//
// **密文不在这里**：三处密文放在记录的顶层 Hidden 字段
// （key_cipher / claude_key_cipher / openai_key_cipher），
// 因为 PocketBase 没法只隐藏 JSON 字段里的一个子键，而 spec §5.2 要求
// 密文永不回传前端。末四位留在这里——它正是要回传给 UI 回显的东西。
type Endpoint struct {
	BaseURL   string   `json:"base_url"`
	AuthField string   `json:"auth_field"`
	KeyLast4  string   `json:"key_last4,omitempty"`
	Models    []string `json:"models"`
}

// Configured 是「这个端点配没配」的唯一判定（spec §2.2）。
//
// 一个没有 base_url 的端点本来就无从使用，因此不另加 enabled 布尔——
// 两个字段表达同一件事只会产生「enabled=true 但 base_url 为空」这种
// 需要额外校验、且没有正确处理方式的中间态。
// 发布校验、UI 置灰、快照组装三处共用这一个判断。
func (e Endpoint) Configured() bool { return e.BaseURL != "" }

// ClaudeEndpoint 比 openai 侧多四个模型槽。
//
// 两个端点的字段刻意不对称（spec §2.4）：四模型槽是 Claude Code 特有的
// 概念——它会自己去要 haiku 做标题生成一类的轻量活。Codex 没有这个机制，
// 强行统一等于给它编造出不存在的 opus/haiku 概念。
type ClaudeEndpoint struct {
	Endpoint
	Defaults ModelSlots `json:"defaults"`
}

// OpenAIEndpoint 只有一个默认模型。
type OpenAIEndpoint struct {
	Endpoint
	DefaultModel string `json:"default_model"`
}

// ClaudeOf / OpenAIOf 从记录解出端点。
// 空 JSON 字段返回零值——刚建的记录与 004 之前的老记录都是这样，不是错误。
func ClaudeOf(r *core.Record) ClaudeEndpoint {
	var e ClaudeEndpoint
	_ = r.UnmarshalJSONField(EndpointClaude, &e)
	return e
}

func OpenAIOf(r *core.Record) OpenAIEndpoint {
	var e OpenAIEndpoint
	_ = r.UnmarshalJSONField(EndpointOpenAI, &e)
	return e
}

// Binding 是配置集的服务绑定：{provider, models{main,opus,sonnet,haiku}}。
//
// **单数，不是数组**（spec §1.3）。多绑定的位置留给以后的迁移，现在不预留
// 结构——一个只可能有一个元素的 map 会诱使实现去支持它。
type Binding struct {
	Provider string     `json:"provider"` // providers 记录 id
	Models   ModelSlots `json:"models"`
}

// PresetEndpoint 是预设表里的一个协议端点。BaseURL 为空 = 该平台没有这个口。
type PresetEndpoint struct {
	BaseURL      string     `json:"base_url"`
	AuthField    string     `json:"auth_field"`
	Models       []string   `json:"models"`
	Defaults     ModelSlots `json:"defaults"`      // 仅 claude 侧填
	DefaultModel string     `json:"default_model"` // 仅 openai 侧填
}

// Preset 是内置平台条目。编译进二进制、只读（M1.5 spec §2.3）。
//
// 一条 = 一家平台，两组端点一起带出（spec §5.6）：选预设时对话框把两个
// 端点分区都填好，用户只需要粘一次 key。
type Preset struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Claude PresetEndpoint `json:"claude"`
	OpenAI PresetEndpoint `json:"openai"`

	WebsiteURL string `json:"website_url,omitempty"`
	APIKeyURL  string `json:"api_key_url,omitempty"`
	Icon       string `json:"icon,omitempty"`
	IconColor  string `json:"icon_color,omitempty"`

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
		ID:   "zhipu",
		Name: "智谱 GLM",
		Claude: PresetEndpoint{
			BaseURL:   "https://open.bigmodel.cn/api/anthropic",
			AuthField: AuthToken,
			// [1m] 只写进 Defaults：它是槽位上的上下文声明，不是一个独立模型。
			// Claude Code 见到该后缀即按 1e6 计算 auto-compact 阈值与 /context，
			// 无需另设 CLAUDE_CODE_AUTO_COMPACT_WINDOW。
			Models: []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
			Defaults: ModelSlots{
				Main: "glm-5.2[1m]", Opus: "glm-5.2[1m]",
				Sonnet: "glm-5.2[1m]", Haiku: "glm-4.7",
			},
		},
		OpenAI: PresetEndpoint{
			// OpenAI 协议口是 /api/paas/v4，不认 [1m] 那类 Claude Code 侧的后缀。
			BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
			AuthField:    DefaultOpenAIAuthField,
			Models:       []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
			DefaultModel: "glm-5.2",
		},
		WebsiteURL:    "https://open.bigmodel.cn",
		APIKeyURL:     "https://open.bigmodel.cn/usercenter/apikeys",
		Icon:          "zhipu",
		IconColor:     "#3859FF",
		CollectorType: "zhipu",
		CollectorMode: "fetch",
	},
	{
		ID:   "zhipu_intl",
		Name: "Z.ai (GLM International)",
		Claude: PresetEndpoint{
			BaseURL:   "https://api.z.ai/api/anthropic",
			AuthField: AuthToken,
			Models:    []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
			Defaults: ModelSlots{
				Main: "glm-5.2", Opus: "glm-5.2", Sonnet: "glm-5.2", Haiku: "glm-4.7",
			},
		},
		OpenAI: PresetEndpoint{
			BaseURL:      "https://api.z.ai/api/paas/v4",
			AuthField:    DefaultOpenAIAuthField,
			Models:       []string{"glm-5.2", "glm-5-turbo", "glm-4.7"},
			DefaultModel: "glm-5.2",
		},
		WebsiteURL:    "https://z.ai",
		APIKeyURL:     "https://z.ai/manage-apikey/apikey-list",
		Icon:          "zhipu",
		IconColor:     "#3859FF",
		CollectorType: "zhipu",
		CollectorMode: "fetch",
	},
	{
		ID:   "kimi",
		Name: "Kimi (Moonshot)",
		Claude: PresetEndpoint{
			BaseURL:   "https://api.moonshot.cn/anthropic",
			AuthField: AuthToken,
			Models: []string{
				"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6",
			},
			Defaults: ModelSlots{
				Main: "kimi-k3[1m]", Opus: "kimi-k3[1m]",
				Sonnet: "kimi-k3[1m]", Haiku: "kimi-k3[1m]",
			},
		},
		OpenAI: PresetEndpoint{
			BaseURL:   "https://api.moonshot.cn/v1",
			AuthField: DefaultOpenAIAuthField,
			Models: []string{
				"kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "kimi-k2.6",
			},
			DefaultModel: "kimi-k3",
		},
		WebsiteURL:    "https://platform.moonshot.cn",
		APIKeyURL:     "https://platform.moonshot.cn/console/api-keys",
		Icon:          "kimi",
		IconColor:     "#000000",
		CollectorType: "kimi",
		CollectorMode: "fetch",
	},
	{
		ID:   "volcengine",
		Name: "火山方舟 Coding Plan",
		// 同一个域名下两个协议口，用错会走计费不同的通道：
		//   /api/coding 是 Anthropic 协议口
		//   /api/v3     是 OpenAI 协议口
		Claude: PresetEndpoint{
			BaseURL:   "https://ark.cn-beijing.volces.com/api/coding",
			AuthField: AuthToken,
			Models: []string{
				"doubao-seed-code-preview-latest", "doubao-seed-2.0-code",
				"deepseek-v3.2", "glm-4.7", "kimi-k2.5",
			},
			Defaults: ModelSlots{
				Main: "doubao-seed-code-preview-latest", Opus: "doubao-seed-code-preview-latest",
				Sonnet: "doubao-seed-code-preview-latest", Haiku: "doubao-seed-code-preview-latest",
			},
		},
		OpenAI: PresetEndpoint{
			BaseURL:   "https://ark.cn-beijing.volces.com/api/v3",
			AuthField: DefaultOpenAIAuthField,
			Models: []string{
				"doubao-seed-code-preview-latest", "doubao-seed-2.0-code",
				"deepseek-v3.2", "glm-4.7", "kimi-k2.5",
			},
			DefaultModel: "doubao-seed-code-preview-latest",
		},
		WebsiteURL:    "https://www.volcengine.com/product/ark",
		APIKeyURL:     "https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey",
		Icon:          "volcengine",
		IconColor:     "#00D0DC",
		CollectorType: "volcengine",
		CollectorMode: "fetch",
	},
	{
		ID:   "zenmux",
		Name: "ZenMux",
		Claude: PresetEndpoint{
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
		},
		OpenAI: PresetEndpoint{
			// OpenAI 协议口不认 Claude 系的别名，一律 `厂商/模型` 全 id。
			BaseURL:   "https://zenmux.ai/api/v1",
			AuthField: DefaultOpenAIAuthField,
			Models: []string{
				"openai/gpt-5.2", "anthropic/claude-opus-4-7", "anthropic/claude-sonnet-4-6",
				"google/gemini-3-pro-preview", "volcengine/doubao-seed-code",
			},
			DefaultModel: "openai/gpt-5.2",
		},
		WebsiteURL: "https://zenmux.ai",
		APIKeyURL:  "https://zenmux.ai/settings/keys",
		Icon:       "zenmux",
		IconColor:  "#6D28D9",
	},
	{
		ID:   "minimax",
		Name: "MiniMax",
		// 国内站两个口都换域名：Anthropic 口是 https://api.minimaxi.com/anthropic，
		// OpenAI 口是 https://api.minimaxi.com/v1，建自定义 Provider 即可。
		Claude: PresetEndpoint{
			BaseURL:   "https://api.minimax.io/anthropic",
			AuthField: AuthToken,
			Models:    []string{"MiniMax-M2"},
			Defaults: ModelSlots{
				Main: "MiniMax-M2", Opus: "MiniMax-M2",
				Sonnet: "MiniMax-M2", Haiku: "MiniMax-M2",
			},
		},
		OpenAI: PresetEndpoint{
			BaseURL:      "https://api.minimax.io/v1",
			AuthField:    DefaultOpenAIAuthField,
			Models:       []string{"MiniMax-M2"},
			DefaultModel: "MiniMax-M2",
		},
		WebsiteURL: "https://platform.minimax.io",
		APIKeyURL:  "https://platform.minimax.io/user-center/basic-information/interface-key",
		Icon:       "minimax",
		IconColor:  "#F23F5D",
	},
	{
		ID:   "anthropic",
		Name: "Anthropic 官方",
		Claude: PresetEndpoint{
			BaseURL:   "https://api.anthropic.com",
			AuthField: AuthAPIKey,
			Models: []string{
				"claude-opus-5", "claude-sonnet-5", "claude-fable-5", "claude-haiku-4-5-20251001",
			},
			// 透传：四槽留空，让 Claude Code 用它自己的默认模型。
			// 那 35 个中转预设就是这么干的（M1.5 spec §2.3）。
			Defaults: ModelSlots{},
		},
		// Anthropic 官方没有 OpenAI 协议口。UI 显示「该平台未提供 OpenAI 端点」，
		// 用户仍可手填（比如挂在自己的兼容层后面）。
		OpenAI:     PresetEndpoint{},
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
