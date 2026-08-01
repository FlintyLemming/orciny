/**
 * 配置集草稿编辑器的状态机。
 *
 * dirty 指的是「与 head 有别」——保存草稿文件不会清脏，只有发布成功才回到 clean。
 * 发布中忽略编辑：内容会与已提交的清单对不上。
 */

export type DraftState = 'clean' | 'dirty' | 'publishing' | 'failed'

export type DraftEvent =
  | { type: 'edit' }
  | { type: 'save' }
  | { type: 'publish' }
  | { type: 'published' }
  | { type: 'failed'; error: string }
  | { type: 'discard' }

export function nextDraftState(s: DraftState, e: DraftEvent): DraftState {
  switch (e.type) {
    case 'edit':
      if (s === 'publishing') return 'publishing'
      return 'dirty'
    case 'save':
      return s === 'clean' ? 'clean' : s === 'publishing' ? 'publishing' : 'dirty'
    case 'publish':
      return s === 'dirty' || s === 'failed' ? 'publishing' : s
    case 'published':
      return s === 'publishing' ? 'clean' : s
    case 'failed':
      return s === 'publishing' ? 'failed' : s
    case 'discard':
      return s === 'publishing' ? 'publishing' : 'clean'
  }
}

/** 有校验问题就不许发布；只有 dirty/failed 可点发布。 */
export function canPublish(s: DraftState, problems: number): boolean {
  if (problems > 0) return false
  return s === 'dirty' || s === 'failed'
}
