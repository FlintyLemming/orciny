import { useEffect, useMemo, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { Trash2 } from 'lucide-react'
import { useLingui } from '@lingui/react/macro'
import { $overrides, overridesByPath, subscribeOverrides } from '@/stores/overrides'
import { useAttentionText } from '@/components/AttentionBanner'
import { dropOverride } from '@/lib/api'

/** 机器详情页的「本机覆盖」区块（M1.8 spec §6.4）。 */
export function MachineOverrides({ machineId }: { machineId: string }) {
  const { t } = useLingui()
  // 与 AttentionBanner 复用同一套措辞（M1.8 spec R3）。
  const attentionText = useAttentionText()
  const all = useStore($overrides)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => subscribeOverrides(), [])

  const groups = useMemo(() => overridesByPath(all, machineId), [all, machineId])

  async function remove(id: string) {
    setBusy(true)
    setError('')
    try {
      await dropOverride(id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (groups.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>这台机器没有本机覆盖。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-3">
      {error && <p className="text-sm text-rose-600">{error}</p>}
      {groups.map((g) => (
        <section key={g.path} className="rounded border border-line">
          <div className="flex flex-wrap items-baseline gap-2 border-b border-line px-3 py-1.5">
            <span className="font-mono text-xs break-all">{g.path}</span>
            <span className="text-xs text-ink3">
              <Trans>{g.items.length} 处</Trans>
            </span>
          </div>
          <ul className="divide-y divide-line/60">
            {g.items.map((o) => (
              <li
                key={o.id}
                className={`flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-xs ${
                  o.attention !== '' ? 'bg-amber-500/10' : ''
                }`}
              >
                <span className="font-mono">
                  {o.kind === 'json_key' ? o.selector : t`整份文件`}
                </span>
                {o.attention !== '' && (
                  <span className="text-amber-700 dark:text-amber-300">
                    {attentionText(o.attention)}
                  </span>
                )}
                <span className="flex-1 text-ink3">
                  {new Date(o.created).toLocaleString()}
                </span>
                <button
                  type="button"
                  disabled={busy}
                  aria-label={t`删除这处覆盖`}
                  onClick={() => void remove(o.id)}
                  className="rounded p-1 text-crit hover:bg-wash"
                >
                  <Trash2 size={14} aria-hidden />
                </button>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
