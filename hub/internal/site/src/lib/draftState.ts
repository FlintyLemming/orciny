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

import type { ConfigSetRecord } from '@/types/collections'
import { normalizeFileEntry } from '@/lib/api'

/**
 * 草稿相对 head 的差异。head 为空（从未发布）时一切都算差异。
 * 比对 set.draft 与 set.expand.head.files（按 path 排序后逐项比 path/hash/mode），
 * 以及 set.draft_binding 与 set.expand.head.binding。
 */
export function diffAgainstHead(set: ConfigSetRecord | null | undefined): {
  files: boolean
  binding: boolean
  isDirty: boolean
} {
  if (!set) {
    return { files: false, binding: false, isDirty: false }
  }

  const head = set.expand?.head

  // head 为空（从未发布）时有草稿即为脏
  if (!set.head || !head) {
    const rawDraft = set.draft ?? []
    const hasFiles = rawDraft.length > 0
    const hasBinding = Boolean(set.draft_binding && set.draft_binding.provider)
    return {
      files: hasFiles,
      binding: hasBinding,
      isDirty: hasFiles || hasBinding,
    }
  }

  // 比对文件
  const draftFiles = (set.draft ?? [])
    .map((f) => normalizeFileEntry(f as unknown as Record<string, unknown>))
    .sort((a, b) => a.path.localeCompare(b.path))
  const headFiles = (head.files ?? [])
    .map((f) => normalizeFileEntry(f as unknown as Record<string, unknown>))
    .sort((a, b) => a.path.localeCompare(b.path))

  let filesChanged = false
  if (draftFiles.length !== headFiles.length) {
    filesChanged = true
  } else {
    for (let i = 0; i < draftFiles.length; i++) {
      const d = draftFiles[i]
      const h = headFiles[i]
      if (d.path !== h.path || d.hash !== h.hash || d.mode !== h.mode) {
        filesChanged = true
        break
      }
    }
  }

  // 比对绑定
  const db = set.draft_binding
  const hb = head.binding
  const dbProv = db?.provider || ''
  const hbProv = hb?.provider || ''

  let bindingChanged = false
  if (dbProv !== hbProv) {
    bindingChanged = true
  } else if (dbProv !== '') {
    const dm = db?.models
    const hm = hb?.models
    if (
      dm?.main !== hm?.main ||
      dm?.opus !== hm?.opus ||
      dm?.sonnet !== hm?.sonnet ||
      dm?.haiku !== hm?.haiku
    ) {
      bindingChanged = true
    }
  }

  return {
    files: filesChanged,
    binding: bindingChanged,
    isDirty: filesChanged || bindingChanged,
  }
}

