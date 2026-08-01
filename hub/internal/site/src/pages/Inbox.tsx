import { useEffect, useMemo, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import {
  $drifts,
  $driftsError,
  $driftsLoading,
  filterDrifts,
  subscribeDrifts,
  toDriftEvent,
  type DriftFilter,
} from '@/stores/drift'
import { $machines, subscribeMachines } from '@/stores/machines'
import {
  adoptBlockers,
  findConflicts,
  groupByMachine,
  needsReview,
  type DriftEvent,
} from '@/lib/inbox'
import { DriftCard } from '@/components/DriftCard'
import { ThreeWayCompare } from '@/components/ThreeWayCompare'
import { adoptDrift, getBlob, ignoreDrift, restoreDrift } from '@/lib/api'
import type { DriftEventRecord } from '@/types/collections'

const FILTERS: { key: DriftFilter; label: React.ReactNode }[] = [
  { key: 'open', label: <Trans>未处理</Trans> },
  { key: 'superseded', label: <Trans>已被覆盖</Trans> },
  { key: 'adopted', label: <Trans>已收编</Trans> },
  { key: 'restored', label: <Trans>已恢复</Trans> },
  { key: 'ignored', label: <Trans>已忽略</Trans> },
  { key: 'all', label: <Trans>全部</Trans> },
]

export function Inbox() {
  const { t } = useLingui()
  const drifts = useStore($drifts)
  const loading = useStore($driftsLoading)
  const error = useStore($driftsError)
  const machines = useStore($machines)

  const [filter, setFilter] = useState<DriftFilter>('open')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)
  const [actionErr, setActionErr] = useState('')
  const [reviewing, setReviewing] = useState<DriftEvent[] | null>(null)
  const [reviewChecked, setReviewChecked] = useState<Set<string>>(new Set())
  const [compare, setCompare] = useState<{
    path: string
    base: string
    left: { label: string; content: string }
    right: { label: string; content: string }
  } | null>(null)
  const [globalIgnore, setGlobalIgnore] = useState(false)

  useEffect(() => subscribeDrifts(), [])
  useEffect(() => subscribeMachines(), [])

  const machineName = useMemo(() => {
    const m = new Map<string, string>()
    for (const x of machines) m.set(x.id, x.name || x.hostname || x.id)
    return m
  }, [machines])

  const visible = useMemo(() => filterDrifts(drifts, filter), [drifts, filter])
  const byId = useMemo(() => {
    const m = new Map<string, DriftEventRecord>()
    for (const d of drifts) m.set(d.id, d)
    return m
  }, [drifts])

  const selectedEvents: DriftEvent[] = useMemo(() => {
    const out: DriftEvent[] = []
    for (const id of selected) {
      const r = byId.get(id)
      if (r) out.push(toDriftEvent(r))
    }
    return out
  }, [selected, byId])

  const blockers = useMemo(() => adoptBlockers(selectedEvents), [selectedEvents])
  const conflicts = useMemo(() => findConflicts(selectedEvents), [selectedEvents])
  const groups = useMemo(
    () => groupByMachine(visible.map(toDriftEvent)),
    [visible],
  )

  function toggle(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function clearSelection() {
    setSelected(new Set())
  }

  async function doAdopt(reviewed: string[] = []) {
    setBusy(true)
    setActionErr('')
    try {
      await adoptDrift([...selected], reviewed)
      clearSelection()
      setReviewing(null)
      setReviewChecked(new Set())
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  function handleAdopt() {
    if (blockers.length > 0) return
    const review = needsReview(selectedEvents)
    if (review.length > 0) {
      setReviewing(review)
      setReviewChecked(new Set())
      return
    }
    void doAdopt()
  }

  async function handleRestore() {
    if (selected.size === 0) return
    setBusy(true)
    setActionErr('')
    try {
      await restoreDrift([...selected])
      clearSelection()
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleIgnore() {
    if (selected.size === 0) return
    setBusy(true)
    setActionErr('')
    try {
      await ignoreDrift([...selected], globalIgnore)
      clearSelection()
      setGlobalIgnore(false)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function openCompare(path: string, events: DriftEvent[]) {
    if (events.length < 2) return
    const a = byId.get(events[0].id)
    const b = byId.get(events[1].id)
    if (!a || !b) return

    setBusy(true)
    setActionErr('')
    try {
      const baseHash = a.base_hash || b.base_hash
      const base = baseHash ? await getBlob(baseHash).catch(() => '') : ''
      const leftContent = await loadCurrent(a)
      const rightContent = await loadCurrent(b)
      setCompare({
        path,
        base,
        left: {
          label: machineName.get(a.machine) ?? a.machine,
          content: leftContent,
        },
        right: {
          label: machineName.get(b.machine) ?? b.machine,
          content: rightContent,
        },
      })
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4 pb-24">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">
          <Trans>收件箱</Trans>
        </h1>
        <div className="flex flex-wrap gap-1">
          {FILTERS.map((f) => (
            <button
              key={f.key}
              type="button"
              onClick={() => setFilter(f.key)}
              className={`rounded px-2 py-1 text-xs ${
                filter === f.key ? 'bg-accent text-white' : 'bg-wash text-ink2'
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>

      {(error || actionErr) && (
        <p className="text-sm text-rose-600">{error || actionErr}</p>
      )}
      {loading && (
        <p className="text-sm text-ink3">
          <Trans>加载中…</Trans>
        </p>
      )}

      {!loading && visible.length === 0 && (
        <p className="rounded border border-dashed border-line px-6 py-8 text-center text-sm text-ink3">
          <Trans>暂无漂移条目</Trans>
        </p>
      )}

      <div className="space-y-6">
        {groups.map((g) => (
          <section key={g.machine}>
            <h2 className="mb-2 text-sm font-semibold text-ink2">
              {machineName.get(g.machine) ?? g.machine}
              <span className="ml-2 font-normal text-ink3">({g.events.length})</span>
            </h2>
            <ul className="space-y-2">
              {g.events.map((e) => {
                const rec = byId.get(e.id)
                return (
                  <li key={e.id}>
                    <DriftCard
                      event={e}
                      selected={selected.has(e.id)}
                      onToggle={() => toggle(e.id)}
                      machineLabel={machineName.get(e.machine)}
                      setLabel={rec?.expand?.config_set?.name}
                    />
                  </li>
                )
              })}
            </ul>
          </section>
        ))}
      </div>

      {selected.size > 0 && (
        <div className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-surface/95 px-4 py-3 backdrop-blur">
          <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-3">
            <span className="text-sm text-ink2">
              <Trans>已选 {selected.size} 条</Trans>
            </span>

            {blockers.length > 0 && (
              <ul className="flex-1 text-xs text-rose-600">
                {blockers.map((b) => (
                  <li key={b}>{b}</li>
                ))}
              </ul>
            )}

            {conflicts.length > 0 && (
              <button
                type="button"
                disabled={busy}
                onClick={() => void openCompare(conflicts[0].path, conflicts[0].events)}
                className="rounded bg-wash px-3 py-1.5 text-xs"
              >
                <Trans>三方对比</Trans>
              </button>
            )}

            <label className="flex items-center gap-1 text-xs text-ink3">
              <input
                type="checkbox"
                checked={globalIgnore}
                onChange={(e) => setGlobalIgnore(e.target.checked)}
              />
              <Trans>全局忽略</Trans>
            </label>

            <button
              type="button"
              disabled={busy || blockers.length > 0}
              onClick={handleAdopt}
              className="rounded bg-accent px-3 py-1.5 text-xs text-white disabled:opacity-40"
            >
              <Trans>收编</Trans>
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => void handleRestore()}
              className="rounded bg-wash px-3 py-1.5 text-xs"
            >
              <Trans>恢复</Trans>
            </button>
            <button
              type="button"
              disabled={busy}
              onClick={() => void handleIgnore()}
              className="rounded bg-wash px-3 py-1.5 text-xs"
            >
              <Trans>忽略</Trans>
            </button>
            <button
              type="button"
              onClick={clearSelection}
              className="text-xs text-ink3"
            >
              <Trans>取消选择</Trans>
            </button>
          </div>
        </div>
      )}

      {reviewing && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog">
          <div className="max-h-[80vh] w-full max-w-2xl overflow-auto rounded-lg border border-line bg-surface p-5 shadow-xl">
            <h2 className="mb-2 text-base font-semibold">
              <Trans>收编前复核</Trans>
            </h2>
            <p className="mb-4 text-sm text-ink3">
              <Trans>
                以下条目变量还原不完整（restore_partial）。请逐条查看 diff 并勾选确认后再收编。
              </Trans>
            </p>
            <ul className="mb-4 space-y-3">
              {reviewing.map((e) => (
                <li key={e.id} className="rounded border border-line p-2">
                  <label className="mb-2 flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={reviewChecked.has(e.id)}
                      onChange={() => {
                        setReviewChecked((prev) => {
                          const next = new Set(prev)
                          if (next.has(e.id)) next.delete(e.id)
                          else next.add(e.id)
                          return next
                        })
                      }}
                    />
                    <span className="font-mono">{e.path}</span>
                  </label>
                  <DriftCard event={e} selected={false} onToggle={() => {}} />
                </li>
              ))}
            </ul>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                className="rounded bg-wash px-3 py-1.5 text-sm"
                onClick={() => setReviewing(null)}
              >
                <Trans>取消</Trans>
              </button>
              <button
                type="button"
                disabled={busy || reviewChecked.size !== reviewing.length}
                className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
                onClick={() => void doAdopt([...reviewChecked])}
              >
                {t`确认收编`}
              </button>
            </div>
          </div>
        </div>
      )}

      {compare && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog">
          <div className="max-h-[85vh] w-full max-w-5xl overflow-auto rounded-lg border border-line bg-surface p-5 shadow-xl">
            <div className="mb-3 flex items-center justify-between gap-2">
              <h2 className="text-base font-semibold">
                <Trans>三方对比</Trans>
                <span className="ml-2 font-mono text-sm font-normal text-ink3">{compare.path}</span>
              </h2>
              <button
                type="button"
                className="text-sm text-ink3"
                onClick={() => setCompare(null)}
              >
                <Trans>关闭</Trans>
              </button>
            </div>
            <ThreeWayCompare
              base={{ label: t`基线`, content: compare.base }}
              left={compare.left}
              right={compare.right}
            />
            <p className="mt-3 text-xs text-ink3">
              <Trans>
                请取消其中一侧的选择后，再对保留侧执行收编。系统不会静默合并两边的改动。
              </Trans>
            </p>
          </div>
        </div>
      )}
    </div>
  )
}

async function loadCurrent(rec: DriftEventRecord): Promise<string> {
  if (rec.kind === 'deleted') return ''
  const hash = rec.expand?.current_blob?.hash
  if (hash) {
    try {
      return await getBlob(hash)
    } catch {
      return ''
    }
  }
  return ''
}
