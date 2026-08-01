import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { ArrowLeft } from 'lucide-react'
import { $machines, subscribeMachines } from '@/stores/machines'
import { $events, subscribeEvents } from '@/stores/events'
import { $configSets, subscribeConfigSets } from '@/stores/configsets'
import { StatusDot } from '@/components/StatusDot'
import { Placeholder } from '@/components/Placeholder'
import { AssignDialog } from '@/components/AssignDialog'
import { VariablesEditor } from '@/components/VariablesEditor'
import { navigate } from '@/router'
import { assignConfigSet } from '@/lib/api'
import { pb } from '@/lib/pb'
import {
  COLLECTION_ASSIGNMENTS,
  type AssignmentRecord,
  type EventKind,
} from '@/types/collections'

const eventLabel: Partial<Record<EventKind, React.ReactNode>> = {
  'machine.enrolled': <Trans>已注册</Trans>,
  'machine.re-enrolled': <Trans>重新注册</Trans>,
  'machine.connected': <Trans>已连接</Trans>,
  'machine.disconnected': <Trans>已断开</Trans>,
  'machine.removed': <Trans>已删除</Trans>,
  'token.issued': <Trans>签发注册 token</Trans>,
  'auth.failed': <Trans>认证失败</Trans>,
  'configset.published': <Trans>配置集已发布</Trans>,
  'configset.rolled_back': <Trans>配置集已回滚</Trans>,
  'assign.changed': <Trans>指派已变更</Trans>,
  'apply.ok': <Trans>应用成功</Trans>,
  'apply.failed': <Trans>应用失败</Trans>,
  'apply.rollback_failed': <Trans>回滚失败</Trans>,
  'drift.reported': <Trans>上报漂移</Trans>,
  'drift.adopted': <Trans>漂移已收编</Trans>,
  'drift.restored': <Trans>漂移已恢复</Trans>,
  'drift.ignored': <Trans>漂移已忽略</Trans>,
  'drift.superseded': <Trans>漂移已覆盖</Trans>,
  'credential.created': <Trans>凭据已创建</Trans>,
  'credential.rotated': <Trans>凭据已轮换</Trans>,
  'credential.deleted': <Trans>凭据已删除</Trans>,
  'import.completed': <Trans>导入完成</Trans>,
}

export function MachineDetail({ id }: { id: string }) {
  const machines = useStore($machines)
  const events = useStore($events)
  const configSets = useStore($configSets)
  const machine = machines.find((m) => m.id === id)

  const [assignment, setAssignment] = useState<AssignmentRecord | null>(null)
  const [showAssign, setShowAssign] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => subscribeMachines(), [])
  useEffect(() => subscribeEvents(id), [id])
  useEffect(() => subscribeConfigSets(), [])

  useEffect(() => {
    let cancelled = false
    void pb
      .collection(COLLECTION_ASSIGNMENTS)
      .getFullList<AssignmentRecord>({
        filter: pb.filter('machine = {:m}', { m: id }),
        expand: 'config_set,applied_revision',
      })
      .then((list) => {
        if (!cancelled) setAssignment(list[0] ?? null)
      })
      .catch(() => {
        if (!cancelled) setAssignment(null)
      })
    return () => {
      cancelled = true
    }
  }, [id, showAssign])

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

      {error && <p className="text-sm text-rose-600">{error}</p>}

      <div className="grid gap-4 md:grid-cols-3">
        <section className="rounded-lg border border-line bg-surface p-4">
          <h2 className="mb-2 text-sm font-semibold">
            <Trans>配置对齐状态</Trans>
          </h2>
          {assignment ? (
            <dl className="space-y-1 text-sm">
              <div>
                <dt className="text-xs text-ink3"><Trans>配置集</Trans></dt>
                <dd>{assignment.expand?.config_set?.name ?? assignment.config_set}</dd>
              </div>
              <div>
                <dt className="text-xs text-ink3"><Trans>模式</Trans></dt>
                <dd>{assignment.mode}</dd>
              </div>
              <div>
                <dt className="text-xs text-ink3"><Trans>状态</Trans></dt>
                <dd>
                  {assignment.state}
                  {assignment.last_error && (
                    <span className="mt-1 block text-xs text-rose-600">{assignment.last_error}</span>
                  )}
                </dd>
              </div>
              {assignment.expand?.applied_revision && (
                <div>
                  <dt className="text-xs text-ink3"><Trans>已应用版本</Trans></dt>
                  <dd>v{assignment.expand.applied_revision.seq}</dd>
                </div>
              )}
            </dl>
          ) : (
            <p className="mb-2 text-sm text-ink3">
              <Trans>尚未指派配置集。</Trans>
            </p>
          )}
          <div className="mt-3 flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => setShowAssign(true)}
              className="rounded bg-accent px-2 py-1 text-xs text-white"
            >
              <Trans>指派</Trans>
            </button>
            <button
              type="button"
              onClick={() => navigate('import', id)}
              className="rounded bg-wash px-2 py-1 text-xs"
            >
              <Trans>从本机导入</Trans>
            </button>
          </div>
        </section>

        <section className="rounded-lg border border-line bg-surface p-4">
          <h2 className="mb-2 text-sm font-semibold">
            <Trans>漂移</Trans>
          </h2>
          <Placeholder milestone="M1" />
        </section>

        <section className="rounded-lg border border-line bg-surface p-4">
          <h2 className="mb-2 text-sm font-semibold">
            <Trans>机器变量</Trans>
          </h2>
          <VariablesEditor machineId={id} />
        </section>
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

      {showAssign && (
        <AssignDialog
          machineId={id}
          configSets={configSets.map((s) => ({ id: s.id, name: s.name }))}
          applyFileCount={
            configSets.find((s) => s.id === (assignment?.config_set))?.draft?.length
          }
          onClose={() => setShowAssign(false)}
          onSubmit={({ configSet, mode }) => {
            void assignConfigSet(id, configSet, mode)
              .then(() => setShowAssign(false))
              .catch((e: Error) => setError(e.message))
          }}
        />
      )}
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
