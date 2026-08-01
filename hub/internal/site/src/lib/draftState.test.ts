import { describe, expect, it } from 'vitest'
import { canPublish, nextDraftState } from '@/lib/draftState'

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
