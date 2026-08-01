import { useEffect } from 'react'
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import {
  Boxes, CreditCard, Gauge, Inbox, KeyRound, Layers, Server, Settings as Cog,
} from 'lucide-react'
import { $route, navigate, type RouteKey } from '@/router'
import { $openDriftCount, subscribeDrifts } from '@/stores/drift'

const items: { key: RouteKey; icon: typeof Server; label: React.ReactNode; milestone?: string }[] = [
  { key: 'overview', icon: Gauge, label: <Trans>总览</Trans>, milestone: 'M2' },
  { key: 'machines', icon: Server, label: <Trans>机器</Trans> },
  { key: 'configsets', icon: Layers, label: <Trans>配置集</Trans> },
  { key: 'inbox', icon: Inbox, label: <Trans>收件箱</Trans> },
  { key: 'usage', icon: Boxes, label: <Trans>用量</Trans>, milestone: 'M2' },
  { key: 'subscriptions', icon: CreditCard, label: <Trans>订阅</Trans>, milestone: 'M2' },
  { key: 'credentials', icon: KeyRound, label: <Trans>凭据</Trans> },
  { key: 'settings', icon: Cog, label: <Trans>设置</Trans> },
]

export function Sidebar() {
  const route = useStore($route)
  const openCount = useStore($openDriftCount)

  // 侧栏角标需要 open 计数，即使人不在收件箱页也订一份
  useEffect(() => subscribeDrifts(), [])

  return (
    <nav aria-label="primary" className="w-52 shrink-0 border-r border-line bg-surface p-3">
      <div className="mb-6 px-2 text-lg font-semibold">Orciny</div>
      <ul className="space-y-0.5">
        {items.map(({ key, icon: Icon, label, milestone }) => (
          <li key={key}>
            <button
              type="button"
              aria-current={route.key === key ? 'page' : undefined}
              onClick={() => navigate(key)}
              className={`flex w-full items-center gap-2 rounded px-2 py-1.5 text-sm ${
                route.key === key ? 'bg-accent-soft text-accent' : 'text-ink2 hover:bg-wash'
              }`}
            >
              <Icon size={16} aria-hidden />
              <span className="flex-1 text-left">{label}</span>
              {key === 'inbox' && openCount > 0 && (
                <span className="rounded-full bg-accent px-1.5 text-[10px] leading-4 text-white">
                  {openCount > 99 ? '99+' : openCount}
                </span>
              )}
              {milestone && <span className="text-[10px] text-ink3">{milestone}</span>}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  )
}
