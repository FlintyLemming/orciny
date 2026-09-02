import { useEffect, useMemo, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Plus, Trash2 } from 'lucide-react'
import {
  $machines, $machinesLoading, deleteMachine, renameMachine, subscribeMachines,
} from '@/stores/machines'
import { $configSets, subscribeConfigSets } from '@/stores/configsets'
import { $providers, subscribeProviders } from '@/stores/providers'
import { isPassthrough, stripOneM } from '@/lib/binding'
import { pb } from '@/lib/pb'
import {
  COLLECTION_ASSIGNMENTS,
  type AssignmentRecord,
  type MachineRecord,
} from '@/types/collections'
import { StatusDot } from '@/components/StatusDot'
import { $overrides, subscribeOverrides } from '@/stores/overrides'
import { navigate } from '@/router'
import { AddMachineDialog } from '@/components/AddMachineDialog'
import { ConfirmDialog } from '@/components/ConfirmDialog'

export function Machines() {
  const { t } = useLingui()
  const machines = useStore($machines)
  const loading = useStore($machinesLoading)
  const configSets = useStore($configSets)
  const providers = useStore($providers)
  const [assignments, setAssignments] = useState<AssignmentRecord[]>([])
  const [adding, setAdding] = useState(false)
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null)

  useEffect(() => subscribeMachines(), [])
  useEffect(() => subscribeConfigSets(), [])
  useEffect(() => subscribeProviders(), [])
  useEffect(() => subscribeOverrides(), [])

  const overrides = useStore($overrides)
  const overrideCount = useMemo(() => {
    const m = new Map<string, number>()
    for (const o of overrides) m.set(o.machine, (m.get(o.machine) ?? 0) + 1)
    return m
  }, [overrides])

  useEffect(() => {
    let cancelled = false
    void pb
      .collection(COLLECTION_ASSIGNMENTS)
      .getFullList<AssignmentRecord>()
      .then((list) => {
        if (!cancelled) setAssignments(list)
      })
      .catch(() => {
        if (!cancelled) setAssignments([])
      })
    return () => {
      cancelled = true
    }
  }, [])

  function renderModelCell(m: MachineRecord) {
    const assign = assignments.find((a) => a.machine === m.id)
    if (!assign || !assign.config_set) return '—'

    const set = configSets.find((s) => s.id === assign.config_set)
    if (!set) return '—'

    const binding = set.expand?.head?.binding ?? set.draft_binding
    if (!binding || !binding.provider) {
      return (
        <button
          type="button"
          onClick={() => navigate('configsets', set.id)}
          className="text-xs text-ink3 hover:underline"
        >
          <Trans>未绑定</Trans>
        </button>
      )
    }

    if (isPassthrough(binding.models)) {
      return (
        <button
          type="button"
          onClick={() => navigate('configsets', set.id)}
          className="text-xs text-ink2 hover:underline"
        >
          <Trans>透传</Trans>
        </button>
      )
    }

    const provider = providers.find((p) => p.id === binding.provider)
    const provName = provider?.name || set.head_provider || binding.provider
    const baseModel = stripOneM(binding.models.main || '')
    const label = `${provName} · ${baseModel}`

    return (
      <button
        type="button"
        onClick={() => navigate('configsets', set.id)}
        className="text-xs text-ink hover:underline"
      >
        {label}
      </button>
    )
  }

  return (
    <div>
      <div className="mb-4 flex items-center">
        <h1 className="flex-1 text-lg font-semibold">
          <Trans>机器</Trans>
        </h1>
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="flex items-center gap-1 rounded bg-[var(--pri-bg)] px-3 py-1.5 text-sm text-[var(--pri-ink)]"
        >
          <Plus size={14} aria-hidden />
          <Trans>添加机器</Trans>
        </button>
      </div>

      {loading && (
        <p className="text-sm text-ink3">
          <Trans>加载中…</Trans>
        </p>
      )}

      {!loading && machines.length === 0 && (
        <p className="rounded border border-dashed border-line p-8 text-center text-sm text-ink3">
          <Trans>还没有机器。点「添加机器」拿到一行安装命令。</Trans>
        </p>
      )}

      {machines.length > 0 && (
        <table className="w-full text-sm">
          <thead className="text-left text-ink3">
            <tr className="border-b border-line">
              <th className="py-2 font-normal"><Trans>名称</Trans></th>
              <th className="py-2 font-normal"><Trans>状态</Trans></th>
              <th className="py-2 font-normal"><Trans>系统</Trans></th>
              <th className="py-2 font-normal"><Trans>agent 版本</Trans></th>
              <th className="py-2 font-normal"><Trans>Claude Code</Trans></th>
              <th className="py-2 font-normal"><Trans>模型</Trans></th>
              <th className="py-2 font-normal"><Trans>最后心跳</Trans></th>
              <th />
            </tr>
          </thead>
          <tbody>
            {machines.map((m) => (
              <tr key={m.id} className="border-b border-line/60 hover:bg-wash/50">
                <td className="py-2">
                  {/*
                    key 绑当前显示名：defaultValue 只在挂载时生效，若别处（或另一个
                    浏览器标签）改了名字，realtime 推过来的新值不会刷进这个非受控
                    输入框。换 key 强制重挂载，让它跟上。
                  */}
                  <input
                    key={m.name || m.hostname}
                    defaultValue={m.name || m.hostname}
                    aria-label={t`机器名称`}
                    onBlur={(e) => {
                      const v = e.target.value.trim()
                      if (v && v !== m.name) void renameMachine(m.id, v)
                    }}
                    className="w-full rounded bg-transparent px-1 py-0.5 hover:bg-wash focus:bg-page"
                  />
                </td>
                <td className="py-2">
                  <StatusDot status={m.status} />
                  {/*
                    有覆盖层的机器即使「已对齐」也和别人不一样。不加这枚角标，
                    用户建了几十个覆盖层之后面板上全是「已对齐」，
                    而机队实际配置各不相同（M1.8 spec R1）。
                  */}
                  {(overrideCount.get(m.id) ?? 0) > 0 && (
                    <span className="ml-2 rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-300">
                      <Trans>+{overrideCount.get(m.id)} 本机覆盖</Trans>
                    </span>
                  )}
                </td>
                <td className="py-2 font-mono text-xs text-ink2">{m.os}/{m.arch}</td>
                <td className="py-2 font-mono text-xs text-ink2">{m.agent_version}</td>
                <td className="py-2 font-mono text-xs text-ink2">
                  {m.tool_versions?.['claude-code'] ?? '—'}
                </td>
                <td className="py-2 font-mono text-xs">
                  {renderModelCell(m)}
                </td>
                <td className="py-2 text-xs text-ink3">
                  {/* online 时不显示秒级心跳时间：last_seen 只在状态变化时写（spec §6.5） */}
                  {m.status === 'online' ? <Trans>持续在线</Trans> : formatTime(m.last_seen)}
                </td>
                <td className="py-2 text-right">
                  <button
                    type="button"
                    onClick={() => navigate('machines', m.id)}
                    className="rounded px-2 py-1 text-xs hover:bg-wash"
                  >
                    <Trans>详情</Trans>
                  </button>
                  <button
                    type="button"
                    aria-label={t`删除机器`}
                    onClick={() => setPendingDelete({ id: m.id, name: m.name || m.hostname })}
                    className="rounded p-1 text-crit hover:bg-wash"
                  >
                    <Trash2 size={14} aria-hidden />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && <AddMachineDialog onClose={() => setAdding(false)} />}

      {pendingDelete && (
        <ConfirmDialog
          title={t`删除机器`}
          message={t`删除后该机器的 agent 会停止重试，需要重新 enroll。确定删除？`}
          confirmLabel={t`删除`}
          danger
          onConfirm={() => {
            const id = pendingDelete.id
            setPendingDelete(null)
            void deleteMachine(id)
          }}
          onClose={() => setPendingDelete(null)}
        />
      )}
    </div>
  )
}

function formatTime(v: string) {
  if (!v) return '—'
  return new Date(v).toLocaleString()
}
