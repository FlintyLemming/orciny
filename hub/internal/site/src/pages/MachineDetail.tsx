import { useEffect } from 'react'
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { ArrowLeft } from 'lucide-react'
import { $machines, subscribeMachines } from '@/stores/machines'
import { $events, subscribeEvents } from '@/stores/events'
import { StatusDot } from '@/components/StatusDot'
import { Placeholder } from '@/components/Placeholder'
import { navigate } from '@/router'
import type { EventKind } from '@/types/collections'

const eventLabel: Record<EventKind, React.ReactNode> = {
  'machine.enrolled': <Trans>已注册</Trans>,
  'machine.re-enrolled': <Trans>重新注册</Trans>,
  'machine.connected': <Trans>已连接</Trans>,
  'machine.disconnected': <Trans>已断开</Trans>,
  'machine.removed': <Trans>已删除</Trans>,
  'token.issued': <Trans>签发注册 token</Trans>,
  'auth.failed': <Trans>认证失败</Trans>,
}

export function MachineDetail({ id }: { id: string }) {
  const machines = useStore($machines)
  const events = useStore($events)
  const machine = machines.find((m) => m.id === id)

  useEffect(() => subscribeMachines(), [])
  useEffect(() => subscribeEvents(id), [id])

  if (!machine) {
    return (
      <p className="text-sm text-ink3">
        <Trans>找不到这台机器。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-6">
      <button
        type="button"
        onClick={() => navigate('machines')}
        className="flex items-center gap-1 text-sm text-ink2 hover:text-ink"
      >
        <ArrowLeft size={14} aria-hidden />
        <Trans>返回列表</Trans>
      </button>

      <section className="rounded-lg border border-line bg-surface p-5">
        <div className="mb-4 flex items-center gap-3">
          <h1 className="text-lg font-semibold">{machine.name || machine.hostname}</h1>
          <StatusDot status={machine.status} />
        </div>
        <dl className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-3">
          <Field label={<Trans>主机名</Trans>} value={machine.hostname} />
          <Field label={<Trans>系统</Trans>} value={`${machine.os}/${machine.arch}`} />
          <Field label={<Trans>agent 版本</Trans>} value={machine.agent_version} />
          <Field
            label={<Trans>Claude Code</Trans>}
            value={machine.tool_versions?.['claude-code'] ?? '—'}
          />
          <Field label={<Trans>指纹</Trans>} value={machine.fingerprint} mono />
          <Field
            label={<Trans>最后心跳</Trans>}
            value={machine.status === 'online' ? '—' : new Date(machine.last_seen).toLocaleString()}
          />
        </dl>
      </section>

      <div className="grid gap-4 md:grid-cols-3">
        <PlaceholderCard title={<Trans>配置对齐状态</Trans>} />
        <PlaceholderCard title={<Trans>漂移</Trans>} />
        <PlaceholderCard title={<Trans>机器变量</Trans>} />
      </div>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold">
          <Trans>事件流</Trans>
        </h2>
        {events.length === 0 && (
          <p className="text-sm text-ink3">
            <Trans>暂无事件。</Trans>
          </p>
        )}
        <ul className="space-y-1.5">
          {events.map((e) => (
            <li key={e.id} className="flex gap-3 text-sm">
              <time className="w-40 shrink-0 font-mono text-xs text-ink3">
                {new Date(e.created).toLocaleString()}
              </time>
              <span>{eventLabel[e.kind] ?? e.kind}</span>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}

function Field({ label, value, mono }: { label: React.ReactNode; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs text-ink3">{label}</dt>
      <dd className={mono ? 'font-mono text-xs break-all' : ''}>{value}</dd>
    </div>
  )
}

function PlaceholderCard({ title }: { title: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <h2 className="mb-2 text-sm font-semibold">{title}</h2>
      <Placeholder milestone="M1" />
    </section>
  )
}
