import { useCallback, useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { pb } from '@/lib/pb'
import { remanage } from '@/lib/api'
import { COLLECTION_IGNORE_RULES, type IgnoreRuleRecord } from '@/types/collections'

/**
 * 机器详情页的「不再受管的路径」区块（M1.8 spec §6.4）。
 *
 * 这是存量踩坑用户的补救入口（spec §3.1）：点「恢复受管」会**原子地**
 * 把机器打回 survey 并删掉规则——survey 不是可选项，直接恢复受管的话
 * 下一次 apply 会当场用中台版本盖掉他想捞回来的那份改动。
 */
export function UnmanagedPaths({ machineId }: { machineId: string }) {
  const { t } = useLingui()
  const [rules, setRules] = useState<IgnoreRuleRecord[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    void pb
      .collection(COLLECTION_IGNORE_RULES)
      .getFullList<IgnoreRuleRecord>({
        filter: pb.filter('machine = {:m} || machine = ""', { m: machineId }),
        sort: 'path',
      })
      .then(setRules)
      .catch(() => setRules([]))
  }, [machineId])

  useEffect(load, [load])

  async function restore(path: string) {
    setBusy(true)
    setError('')
    try {
      await remanage(machineId, path)
      load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (rules.length === 0) {
    return (
      <p className="text-sm text-ink3">
        <Trans>没有退管的路径。</Trans>
      </p>
    )
  }

  return (
    <div className="space-y-2">
      {error && <p className="text-sm text-rose-600">{error}</p>}
      <ul className="divide-y divide-line/60">
        {rules.map((r) => (
          <li key={r.id} className="flex flex-wrap items-center gap-3 py-2 text-xs">
            <span className="font-mono break-all">{r.path}</span>
            <span className="flex-1 text-ink3">
              {r.machine ? <Trans>机器级</Trans> : <Trans>全局</Trans>}
            </span>
            {r.machine ? (
              <button
                type="button"
                disabled={busy}
                onClick={() => void restore(r.path)}
                title={t`会把这台机器打回「先看看」模式，差异先进收件箱，再由你决定保留哪几处`}
                className="rounded bg-wash px-2 py-1"
              >
                <Trans>恢复受管</Trans>
              </button>
            ) : (
              <span className="text-ink3">
                <Trans>全局规则请到设置里解除</Trans>
              </span>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
