/**
 * 服务绑定的纯逻辑：模型槽、env 片段、provider 引用检测。
 *
 * 与 UI 分开是为了能单测——组件里只留渲染与事件。
 */

import { parsePlaceholders } from '@/lib/placeholder'
import type { AuthField, ModelSlots } from '@/types/collections'

/** settings.json 的受管相对路径，与 Go 侧 configsets.SettingsPath 一致。 */
export const SETTINGS_PATH = '.claude/settings.json'

export function emptySlots(): ModelSlots {
  return { main: '', opus: '', sonnet: '', haiku: '' }
}

/**
 * 选一个模型 → 四槽同填。
 *
 * 这是默认交互（M1.5 spec §2.3）：cc-switch 的 34 个带模型的预设，
 * 四个槽填的都是同一个值。分开设置放进「高级」。
 */
export function fillAllSlots(model: string): ModelSlots {
  return { main: model, opus: model, sonnet: model, haiku: model }
}

/**
 * Claude Code 的 100 万上下文声明。它是模型名字符串上的语法（Claude Code 按
 * /\[1m\]/i 匹配后把窗口按 1e6 计），不是一个独立模型——所以它只落在槽位值
 * 里，端点的模型清单只列基名。
 */
export const ONE_M_SUFFIX = '[1m]'

/** 模型名是否带 1M 声明。大小写不敏感，与 Claude Code 一致。 */
export function hasOneM(model: string): boolean {
  return model.trimEnd().toLowerCase().endsWith(ONE_M_SUFFIX)
}

/** 去掉尾部的 1M 声明，返回模型基名。不带声明时原样返回。 */
export function stripOneM(model: string): string {
  const trimmed = model.trimEnd()
  if (!hasOneM(trimmed)) return model
  return trimmed.slice(0, -ONE_M_SUFFIX.length).trimEnd()
}

/** 按 enabled 给模型基名加上或去掉 1M 声明。空名原样返回。 */
export function setOneM(model: string, enabled: boolean): string {
  const base = stripOneM(model).trim()
  if (!base || !enabled) return base
  return base + ONE_M_SUFFIX
}

/**
 * 只给基名等于 base 的槽换 1M 声明，其余槽原样保留。
 *
 * 预设可以把 haiku 单独指到别的便宜模型（智谱就是 glm-4.7），1M 开关不该
 * 把那种拆分一并抹平——所以按基名匹配，而不是四槽同填。
 */
export function setSlotsOneM(slots: ModelSlots, base: string, enabled: boolean): ModelSlots {
  const apply = (slot: string) => (stripOneM(slot) === base ? setOneM(slot, enabled) : slot)
  return {
    main: apply(slots.main),
    opus: apply(slots.opus),
    sonnet: apply(slots.sonnet),
    haiku: apply(slots.haiku),
  }
}

/** 透传模式 = 四槽全空。半填不算——那是配置错误。 */
export function isPassthrough(s: ModelSlots): boolean {
  return !s.main && !s.opus && !s.sonnet && !s.haiku
}

/** 内容里是否已经有 {{provider.*}} 引用（转义的不算）。 */
export function hasProviderRefs(text: string): boolean {
  return parsePlaceholders(text).refs.some((r) => r.kind === 'provider')
}

/**
 * env 片段的六个键名 → 占位符。authField 决定承载 key 的那一行的键名。
 *
 * 六个全部是 **claude 端点**：受管范围只有 .claude/**，openai 端点本期
 * 没有消费者（M1.6 spec §1.3）。接 Codex 时会另起一套片段。
 */
export function envSnippet(authField: AuthField): Record<string, string> {
  return {
    ANTHROPIC_BASE_URL: '{{provider.claude.base_url}}',
    [authField]: '{{provider.claude.auth_token}}',
    ANTHROPIC_MODEL: '{{provider.claude.model}}',
    ANTHROPIC_DEFAULT_OPUS_MODEL: '{{provider.claude.model_opus}}',
    ANTHROPIC_DEFAULT_SONNET_MODEL: '{{provider.claude.model_sonnet}}',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: '{{provider.claude.model_haiku}}',
  }
}

/**
 * 把 env 片段合进 settings.json 文本，保留既有键。
 *
 * 内容不是合法 JSON 时**原样返回**：宁可不动，不可写坏（M1 的既定立场）。
 */
export function insertEnvSnippet(settingsText: string, authField: AuthField): string {
  let parsed: Record<string, unknown>
  try {
    parsed = JSON.parse(settingsText || '{}') as Record<string, unknown>
  } catch {
    return settingsText
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return settingsText
  }
  const env = { ...((parsed.env as Record<string, string> | undefined) ?? {}) }
  Object.assign(env, envSnippet(authField))
  return JSON.stringify({ ...parsed, env }, null, 2)
}
