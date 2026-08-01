/**
 * 版本 diff 的前端展示辅助。
 *
 * 编辑器实时草稿 diff 在本地用 jsdiff 算（内容还没落 blob）；
 * 版本间 diff 由 hub 算好后，这里只负责分组与行级渲染。
 */

import { diffLines } from 'diff'

export type ChangeKind = 'added' | 'removed' | 'modified'

export interface FileChange {
  path: string
  kind: ChangeKind
  from_hash: string
  to_hash: string
}

export interface ChangeGroup {
  kind: ChangeKind
  label: string
  files: FileChange[]
}

export interface DiffRow {
  type: 'add' | 'del' | 'ctx'
  text: string
}

const ORDER: ChangeKind[] = ['added', 'modified', 'removed']
const LABELS: Record<ChangeKind, string> = {
  added: '新增',
  modified: '修改',
  removed: '删除',
}

/** 按类型分组，组内按路径排序；空组不出现。 */
export function groupChanges(changes: FileChange[]): ChangeGroup[] {
  const buckets: Record<ChangeKind, FileChange[]> = {
    added: [],
    modified: [],
    removed: [],
  }
  for (const c of changes) {
    buckets[c.kind]?.push(c)
  }
  return ORDER.filter((k) => buckets[k].length > 0).map((k) => ({
    kind: k,
    label: LABELS[k],
    files: buckets[k].slice().sort((a, b) => a.path.localeCompare(b.path)),
  }))
}

/** 行级 inline diff。 */
export function inlineDiff(before: string, after: string): DiffRow[] {
  const parts = diffLines(before, after)
  const rows: DiffRow[] = []
  for (const p of parts) {
    const type: DiffRow['type'] = p.added ? 'add' : p.removed ? 'del' : 'ctx'
    // diffLines 会把换行留在 value 末尾；按行拆开便于渲染
    const lines = p.value.replace(/\n$/, '').split('\n')
    // 空字符串 split 会得到 ['']，保留一行空上下文
    for (const line of lines) {
      rows.push({ type, text: line })
    }
  }
  return rows
}
