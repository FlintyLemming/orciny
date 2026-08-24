import { describe, expect, it } from 'vitest'
import { canPublish, diffAgainstHead, nextDraftState } from '@/lib/draftState'

describe('nextDraftState', () => {
  it('编辑让草稿变脏，保存不清脏（脏指的是与 head 有别）', () => {
    expect(nextDraftState('clean', { type: 'edit' })).toBe('dirty')
    expect(nextDraftState('dirty', { type: 'save' })).toBe('dirty')
  })

  it('发布成功回到 clean，失败进 failed', () => {
    expect(nextDraftState('dirty', { type: 'publish' })).toBe('publishing')
    expect(nextDraftState('publishing', { type: 'published' })).toBe('clean')
    expect(nextDraftState('publishing', { type: 'failed', error: 'x' })).toBe('failed')
  })

  it('失败之后继续编辑回到 dirty', () => {
    expect(nextDraftState('failed', { type: 'edit' })).toBe('dirty')
  })

  it('丢弃回到 clean', () => {
    expect(nextDraftState('dirty', { type: 'discard' })).toBe('clean')
    expect(nextDraftState('failed', { type: 'discard' })).toBe('clean')
  })

  // 发布中不接受编辑：内容会与已提交的清单对不上
  it('发布中忽略编辑事件', () => {
    expect(nextDraftState('publishing', { type: 'edit' })).toBe('publishing')
  })
})

describe('canPublish', () => {
  it('有校验问题就不许发布', () => {
    expect(canPublish('dirty', 0)).toBe(true)
    expect(canPublish('dirty', 1)).toBe(false)
    expect(canPublish('publishing', 0)).toBe(false)
    expect(canPublish('clean', 0)).toBe(false)
  })
})

describe('diffAgainstHead', () => {
  const baseBinding = {
    provider: 'p1',
    models: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
  }

  const baseFiles = [
    { path: '.claude/CLAUDE.md', hash: 'h1', size: 10, mode: 0o644 },
    { path: '.claude/settings.json', hash: 'h2', size: 20, mode: 0o600 },
  ]

  it('都没改时不脏', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: 'r1',
      draft: [...baseFiles],
      draft_refs: null,
      draft_binding: { ...baseBinding },
      head_provider: 'p1',
      created: '',
      updated: '',
      expand: {
        head: {
          id: 'r1',
          config_set: 's1',
          seq: 1,
          files: [...baseFiles],
          manifest: null,
          checksum: '',
          refs: null,
          binding: { ...baseBinding },
          note: '',
          source: 'publish' as const,
          created: '',
        },
      },
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(false)
    expect(diff.binding).toBe(false)
    expect(diff.isDirty).toBe(false)
  })

  it('draft 与 head 顺序不同但内容相同时不算脏', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: 'r1',
      draft: [baseFiles[1], baseFiles[0]], // reverse order
      draft_refs: null,
      draft_binding: { ...baseBinding },
      head_provider: 'p1',
      created: '',
      updated: '',
      expand: {
        head: {
          id: 'r1',
          config_set: 's1',
          seq: 1,
          files: [baseFiles[0], baseFiles[1]],
          manifest: null,
          checksum: '',
          refs: null,
          binding: { ...baseBinding },
          note: '',
          source: 'publish' as const,
          created: '',
        },
      },
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(false)
    expect(diff.isDirty).toBe(false)
  })

  it('仅文件改', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: 'r1',
      draft: [{ path: '.claude/CLAUDE.md', hash: 'h1-modified', size: 12, mode: 0o644 }],
      draft_refs: null,
      draft_binding: { ...baseBinding },
      head_provider: 'p1',
      created: '',
      updated: '',
      expand: {
        head: {
          id: 'r1',
          config_set: 's1',
          seq: 1,
          files: [...baseFiles],
          manifest: null,
          checksum: '',
          refs: null,
          binding: { ...baseBinding },
          note: '',
          source: 'publish' as const,
          created: '',
        },
      },
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(true)
    expect(diff.binding).toBe(false)
    expect(diff.isDirty).toBe(true)
  })

  it('仅绑定改', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: 'r1',
      draft: [...baseFiles],
      draft_refs: null,
      draft_binding: {
        provider: 'p1',
        models: { main: 'glm-5.2[1m]', opus: 'glm-5.2[1m]', sonnet: 'glm-5.2[1m]', haiku: 'glm-4.7' },
      },
      head_provider: 'p1',
      created: '',
      updated: '',
      expand: {
        head: {
          id: 'r1',
          config_set: 's1',
          seq: 1,
          files: [...baseFiles],
          manifest: null,
          checksum: '',
          refs: null,
          binding: { ...baseBinding },
          note: '',
          source: 'publish' as const,
          created: '',
        },
      },
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(false)
    expect(diff.binding).toBe(true)
    expect(diff.isDirty).toBe(true)
  })

  it('都改', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: 'r1',
      draft: [{ path: '.claude/CLAUDE.md', hash: 'h1-new', size: 10, mode: 0o644 }],
      draft_refs: null,
      draft_binding: null,
      head_provider: 'p1',
      created: '',
      updated: '',
      expand: {
        head: {
          id: 'r1',
          config_set: 's1',
          seq: 1,
          files: [...baseFiles],
          manifest: null,
          checksum: '',
          refs: null,
          binding: { ...baseBinding },
          note: '',
          source: 'publish' as const,
          created: '',
        },
      },
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(true)
    expect(diff.binding).toBe(true)
    expect(diff.isDirty).toBe(true)
  })

  it('head 为空（从未发布）时有草稿即为脏', () => {
    const set = {
      id: 's1',
      name: 'set',
      note: '',
      manifest: null,
      paused: false,
      head: '',
      draft: [...baseFiles],
      draft_refs: null,
      draft_binding: { ...baseBinding },
      head_provider: '',
      created: '',
      updated: '',
    }
    const diff = diffAgainstHead(set)
    expect(diff.files).toBe(true)
    expect(diff.binding).toBe(true)
    expect(diff.isDirty).toBe(true)
  })
})

