import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Plus, Trash2 } from 'lucide-react'
import {
  $machines, $machinesLoading, deleteMachine, renameMachine, subscribeMachines,
} from '@/stores/machines'
import { StatusDot } from '@/components/StatusDot'
import { navigate } from '@/router'
import { AddMachineDialog } from '@/components/AddMachineDialog'

export function Machines() {
  const { t } = useLingui()
  const machines = useStore($machines)
  const loading = useStore($machinesLoading)
  const [adding, setAdding] = useState(false)

  useEffect(() => subscribeMachines(), [])

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
                <td className="py-2"><StatusDot status={m.status} /></td>
                <td className="py-2 font-mono text-xs text-ink2">{m.os}/{m.arch}</td>
                <td className="py-2 font-mono text-xs text-ink2">{m.agent_version}</td>
                <td className="py-2 font-mono text-xs text-ink2">
                  {m.tool_versions?.['claude-code'] ?? '—'}
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
                    onClick={() => {
                      if (confirm(t`删除后该机器的 agent 会停止重试，需要重新 enroll。确定删除？`)) {
                        void deleteMachine(m.id)
                      }
                    }}
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
    </div>
  )
}

function formatTime(v: string) {
  if (!v) return '—'
  return new Date(v).toLocaleString()
}
