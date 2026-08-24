import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { ModelQuickSwitch } from '@/components/ModelQuickSwitch'
import type { ConfigSetRecord, ProviderRecord } from '@/types/collections'
import * as api from '@/lib/api'
import { $toast, hideToast } from '@/stores/toast'

i18n.load('en', {})
i18n.activate('en')

function wrap(ui: React.ReactNode) {
  return <I18nProvider i18n={i18n}>{ui}</I18nProvider>
}

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof api>('@/lib/api')
  return {
    ...actual,
    setBinding: vi.fn().mockResolvedValue(undefined),
    publishConfigSet: vi.fn().mockResolvedValue({ revision: 'r2' }),
    rollbackConfigSet: vi.fn().mockResolvedValue({ revision: 'r1' }),
    validateConfigSet: vi.fn().mockResolvedValue([]),
  }
})

const testProvider: ProviderRecord = {
  id: 'p1',
  name: '智谱 GLM',
  preset: 'zhipu',
  note: '',
  key_last4: '1234',
  claude: {
    base_url: 'https://open.bigmodel.cn/api/anthropic',
    auth_field: 'ANTHROPIC_AUTH_TOKEN',
    key_last4: '1234',
    models: [
      { name: 'glm-5.2', one_m: true },
      { name: 'glm-4.7', one_m: false },
    ],
    defaults: { main: 'glm-5.2[1m]', opus: 'glm-5.2[1m]', sonnet: 'glm-5.2[1m]', haiku: 'glm-4.7' },
  },
  openai: null,
  created: '',
  updated: '',
}

const baseSet: ConfigSetRecord = {
  id: 's1',
  name: '主力配置',
  note: '',
  manifest: null,
  paused: false,
  head: 'r1',
  draft: [{ path: '.claude/CLAUDE.md', hash: 'h1', size: 10, mode: 0o644 }],
  draft_refs: null,
  draft_binding: {
    provider: 'p1',
    models: { main: 'glm-4.7', opus: 'glm-4.7', sonnet: 'glm-4.7', haiku: 'glm-4.7' },
  },
  head_provider: 'p1',
  created: '',
  updated: '',
  expand: {
    head: {
      id: 'r1',
      config_set: 's1',
      seq: 1,
      files: [{ path: '.claude/CLAUDE.md', hash: 'h1', size: 10, mode: 0o644 }],
      manifest: null,
      checksum: '',
      refs: null,
      binding: {
        provider: 'p1',
        models: { main: 'glm-4.7', opus: 'glm-4.7', sonnet: 'glm-4.7', haiku: 'glm-4.7' },
      },
      note: '',
      source: 'publish',
      created: '',
    },
  },
}

describe('ModelQuickSwitch', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    hideToast()
  })

  it('选中即调 setBinding + publishConfigSet，支持 1M 模型自动带上 [1m]', async () => {
    render(
      wrap(
        <ModelQuickSwitch
          set={baseSet}
          providers={[testProvider]}
          affectedMachines={3}
        />,
      ),
    )

    const select = screen.getByRole('combobox', { name: '快切模型' })
    expect(select).toHaveValue('p1:glm-4.7')

    await userEvent.selectOptions(select, 'p1:glm-5.2')

    expect(api.setBinding).toHaveBeenCalledWith('s1', {
      provider: 'p1',
      models: {
        main: 'glm-5.2[1m]',
        opus: 'glm-5.2[1m]',
        sonnet: 'glm-5.2[1m]',
        haiku: 'glm-5.2[1m]',
      },
    })
    expect(api.publishConfigSet).toHaveBeenCalledWith('s1', expect.stringContaining('glm-5.2[1m]'))

    const toast = $toast.get()
    expect(toast?.message).toContain('glm-5.2[1m]')
    expect(toast?.message).toContain('3')

    // Click undo
    await act(async () => {
      toast?.onUndo?.()
    })
    expect(api.rollbackConfigSet).toHaveBeenCalledWith('s1', 'r1')
  })

  it('支持跨 Provider 切换', async () => {
    const provider2: ProviderRecord = {
      id: 'p2',
      name: 'Kimi Moonshot',
      preset: 'kimi',
      note: '',
      key_last4: '5678',
      claude: {
        base_url: 'https://api.moonshot.cn/v1',
        auth_field: 'ANTHROPIC_AUTH_TOKEN',
        key_last4: '5678',
        models: [{ name: 'kimi-k3', one_m: true }],
        defaults: null,
      },
      openai: null,
      created: '',
      updated: '',
    }

    render(
      wrap(
        <ModelQuickSwitch
          set={baseSet}
          providers={[testProvider, provider2]}
          affectedMachines={3}
        />,
      ),
    )

    const select = screen.getByRole('combobox', { name: '快切模型' })
    await userEvent.selectOptions(select, 'p2:kimi-k3')

    expect(api.setBinding).toHaveBeenCalledWith('s1', {
      provider: 'p2',
      models: {
        main: 'kimi-k3[1m]',
        opus: 'kimi-k3[1m]',
        sonnet: 'kimi-k3[1m]',
        haiku: 'kimi-k3[1m]',
      },
    })
    expect(api.publishConfigSet).toHaveBeenCalledWith('s1', expect.stringContaining('kimi-k3[1m]'))
  })

  it('选中不支持 1M 的模型时不带 [1m]', async () => {
    const setWith52: ConfigSetRecord = {
      ...baseSet,
      draft_binding: {
        provider: 'p1',
        models: { main: 'glm-5.2[1m]', opus: 'glm-5.2[1m]', sonnet: 'glm-5.2[1m]', haiku: 'glm-5.2[1m]' },
      },
    }
    render(
      wrap(
        <ModelQuickSwitch
          set={setWith52}
          providers={[testProvider]}
          affectedMachines={1}
        />,
      ),
    )

    const select = screen.getByRole('combobox', { name: '快切模型' })
    await userEvent.selectOptions(select, 'p1:glm-4.7')

    expect(api.setBinding).toHaveBeenCalledWith('s1', {
      provider: 'p1',
      models: {
        main: 'glm-4.7',
        opus: 'glm-4.7',
        sonnet: 'glm-4.7',
        haiku: 'glm-4.7',
      },
    })
  })

  it('校验有阻断项时不发布并回调 onOpenPublishDialog', async () => {
    vi.mocked(api.validateConfigSet).mockResolvedValueOnce([
      { path: '.claude/CLAUDE.md', kind: 'endpoint_missing', detail: 'err', warning: false },
    ])
    const onOpen = vi.fn()

    render(
      wrap(
        <ModelQuickSwitch
          set={baseSet}
          providers={[testProvider]}
          affectedMachines={3}
          onOpenPublishDialog={onOpen}
        />,
      ),
    )

    const select = screen.getByRole('combobox', { name: '快切模型' })
    await userEvent.selectOptions(select, 'p1:glm-5.2')

    expect(api.setBinding).toHaveBeenCalled()
    expect(api.publishConfigSet).not.toHaveBeenCalled()
    expect(onOpen).toHaveBeenCalledTimes(1)
  })

  it('草稿有文件改动时禁用并显示「有未发布改动 →」', () => {
    const dirtySet: ConfigSetRecord = {
      ...baseSet,
      draft: [{ path: '.claude/CLAUDE.md', hash: 'h-modified', size: 15, mode: 0o644 }],
    }
    render(
      wrap(
        <ModelQuickSwitch
          set={dirtySet}
          providers={[testProvider]}
          affectedMachines={3}
        />,
      ),
    )

    expect(screen.queryByRole('combobox', { name: '快切模型' })).toBeNull()
    expect(screen.getByText('有未发布改动 →')).toBeTruthy()
  })

  it('四槽分设时禁用并显示「已分设 →」', () => {
    const splitSet: ConfigSetRecord = {
      ...baseSet,
      draft_binding: {
        provider: 'p1',
        models: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
      },
      expand: {
        head: {
          ...baseSet.expand!.head!,
          binding: {
            provider: 'p1',
            models: { main: 'glm-5.2', opus: 'glm-5.2', sonnet: 'glm-5.2', haiku: 'glm-4.7' },
          },
        },
      },
    }
    render(
      wrap(
        <ModelQuickSwitch
          set={splitSet}
          providers={[testProvider]}
          affectedMachines={3}
        />,
      ),
    )

    expect(screen.queryByRole('combobox', { name: '快切模型' })).toBeNull()
    expect(screen.getByText('已分设 →')).toBeTruthy()
  })
})
