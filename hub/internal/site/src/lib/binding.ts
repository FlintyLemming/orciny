/**
 * 服务绑定的纯逻辑：模型槽、env 片段、provider 引用检测。
 *
 * 与 UI 分开是为了能单测——组件里只留渲染与事件。
 */

import { parsePlaceholders } from '@/lib/placeholder'
import type { AuthField, ModelSlots } from '@/types/collections'

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

/** 透传模式 = 四槽全空。半填不算——那是配置错误。 */
export function isPassthrough(s: ModelSlots): boolean {
  return !s.main && !s.opus && !s.sonnet && !s.haiku
}

/** 内容里是否已经有 {{provider.*}} 引用（转义的不算）。 */
export function hasProviderRefs(text: string): boolean {
  return parsePlaceholders(text).refs.some((r) => r.kind === 'provider')
}

/** env 片段的六个键名 → 占位符。authField 决定承载 key 的那一行的键名。 */
export function envSnippet(authField: AuthField): Record<string, string> {
  return {
    ANTHROPIC_BASE_URL: '{{provider.base_url}}',
    [authField]: '{{provider.auth_token}}',
    ANTHROPIC_MODEL: '{{provider.model}}',
    ANTHROPIC_DEFAULT_OPUS_MODEL: '{{provider.model_opus}}',
    ANTHROPIC_DEFAULT_SONNET_MODEL: '{{provider.model_sonnet}}',
    ANTHROPIC_DEFAULT_HAIKU_MODEL: '{{provider.model_haiku}}',
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
