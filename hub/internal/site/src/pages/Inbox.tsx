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
import { AttentionBanner } from '@/components/AttentionBanner'
import {
  OverrideDialog,
  defaultChecked,
  selectionsOf,
  type OverrideTarget,
} from '@/components/OverrideDialog'
import { $overrides, subscribeOverrides } from '@/stores/overrides'
import { diffHunkCount, splitPoints } from '@/lib/overridePoints'
import { ThreeWayCompare } from '@/components/ThreeWayCompare'
import {
  adoptDrift,
  createProviderFromDrift,
  dropOverride,
  getBlob,
  ignoreDrift,
  keepOverride,
  overrideDrift,
  listProviderPresets,
  matchBindingDrift,
  rebindDrift,
  restoreDrift,
} from '@/lib/api'
import { reloadProviders } from '@/stores/providers'
import type {
  BindingMatchResult,
  DriftEventRecord,
  MachineOverrideRecord,
  ProviderPreset,
} from '@/types/collections'

const FILTERS: { key: DriftFilter; label: React.ReactNode }[] = [
  { key: 'open', label: <Trans>未处理</Trans> },
  { key: 'superseded', label: <Trans>已被覆盖</Trans> },
  { key: 'adopted', label: <Trans>已收编</Trans> },
  { key: 'restored', label: <Trans>已恢复</Trans> },
  { key: 'overridden', label: <Trans>已本机保留</Trans> },
  // 「已退管」与「已本机保留」是两个不同的去向，不合并（spec §2.2）。
  { key: 'ignored', label: <Trans>已退管</Trans> },
  { key: 'all', label: <Trans>全部</Trans> },
]

/**
 * 把选中的漂移变成弹窗要的 target。
 *
 * baseByHash / curById 是已经取回来的内容（base_hash → 基线内容、
 * event id → 本机内容）。两侧都是 JSON 对象就切 selector，否则走文本一路
 * ——hunk 下标就是 diff 里 @@ 块的序号，与后端的 overrides.Hunks 一一对应。
 */
export function buildOverrideTargets(
  events: DriftEventRecord[],
  baseByHash: Record<string, string>,
  curById: Record<string, string>,
): OverrideTarget[] {
  return events.map((e) => {
    const base = baseByHash[e.base_hash] ?? ''
    const cur = curById[e.id] ?? ''
    const points = splitPoints(base, cur)
    return {
      id: e.id,
      path: e.path,
      diff: e.diff ?? '',
      points,
      hunkCount: points ? 0 : diffHunkCount(e.diff ?? ''),
    }
  })
}

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
  const [overriding, setOverriding] = useState<OverrideTarget[] | null>(null)
  const [overrideChecked, setOverrideChecked] = useState<Record<string, Set<string>>>({})
  const [ignoreMenu, setIgnoreMenu] = useState(false)

  const overrides = useStore($overrides)

  useEffect(() => subscribeDrifts(), [])
  useEffect(() => subscribeMachines(), [])
  useEffect(() => subscribeOverrides(), [])

  const attention = useMemo(
    () => overrides.filter((o) => o.attention !== ''),
    [overrides],
  )

  // 绑定漂移的反查结果。一般只有一两条，进页面时一次性取回来。
  const [matches, setMatches] = useState<Record<string, BindingMatchResult>>({})
  const [presets, setPresets] = useState<ProviderPreset[]>([])
  const bindingIDs = useMemo(
    () =>
      drifts
        .filter((d) => d.binding_drift && d.state === 'open')
        .map((d) => d.id)
        .sort()
        .join(','),
    [drifts],
  )
  useEffect(() => {
    if (!bindingIDs) {
      setMatches({})
      return
    }
    let cancelled = false
    void Promise.all(
      bindingIDs.split(',').map((id) => matchBindingDrift(id).then((m) => [id, m] as const)),
    )
      .then((pairs) => {
        if (!cancelled) setMatches(Object.fromEntries(pairs))
      })
      .catch(() => {
        // 反查失败就退回第三档兜底文案，不打断收件箱。
      })
    return () => {
      cancelled = true
    }
  }, [bindingIDs])
  useEffect(() => {
    void listProviderPresets()
      .then(setPresets)
      .catch(() => setPresets([]))
  }, [])

  /** 第一档：改绑定并发布新版本。全机队跟着走。 */
  async function handleRebind(eventID: string, providerID: string) {
    setBusy(true)
    setActionErr('')
    try {
      await rebindDrift(eventID, providerID)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /**
   * 第二档：用预设新建服务配置（顺手把机器上手写的 key 内联进它的 claude
   * 端点），建成后立刻回到第一档的动作——这就是 M1.5 spec §6.3 说的
   * 「建成后回到上一行」。
   *
   * 端点恒为 claude：漂移源只有 .claude/**（M1.6 spec §1.3）。
   */
  async function handleCreateProvider(
    eventID: string,
    presetID: string,
    keyLocation: string,
  ) {
    const preset = presets.find((p) => p.id === presetID)
    if (!preset || !keyLocation) {
      setActionErr(t`缺少预设或 key 位置，无法自动新建，请到「AI 服务」页手工新建`)
      return
    }
    setBusy(true)
    setActionErr('')
    try {
      const { id } = await createProviderFromDrift({
        name: preset.name,
        preset: preset.id,
        note: t`从收件箱的绑定漂移创建`,
        claude: {
          base_url: preset.claude.base_url,
          auth_field: preset.claude.auth_field,
          models: preset.claude.models,
          defaults: preset.claude.defaults,
        },
        openai: {
          base_url: preset.openai.base_url,
          auth_field: preset.openai.auth_field,
          models: preset.openai.models,
          default_model: preset.openai.default_model,
        },
        from_drift: {
          event: eventID,
          location: keyLocation,
          endpoint: 'claude',
        },
      })
      await reloadProviders()
      await rebindDrift(eventID, id)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

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

  /** 本机保留：取回两侧内容 → 切差异点 → 开弹窗（默认全选）。 */
  async function handleOverride() {
    if (selected.size === 0) return
    setBusy(true)
    setActionErr('')
    try {
      const recs = [...selected]
        .map((id) => byId.get(id))
        .filter((r): r is DriftEventRecord => Boolean(r))
      const baseByHash: Record<string, string> = {}
      const curById: Record<string, string> = {}
      for (const r of recs) {
        if (r.base_hash && !(r.base_hash in baseByHash)) {
          baseByHash[r.base_hash] = await getBlob(r.base_hash).catch(() => '')
        }
        curById[r.id] = await loadCurrent(r)
      }
      const targets = buildOverrideTargets(recs, baseByHash, curById)
      setOverriding(targets)
      setOverrideChecked(defaultChecked(targets))
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function doOverride() {
    if (!overriding) return
    setBusy(true)
    setActionErr('')
    try {
      const points = selectionsOf(overriding, overrideChecked)
      // restore_partial 的条目必须列进 reviewed —— 与收编同一条规矩。
      const reviewed = overriding
        .filter((tg) => byId.get(tg.id)?.restore_partial)
        .map((tg) => tg.id)
      await overrideDrift(Object.keys(points), points, reviewed)
      setOverriding(null)
      setOverrideChecked({})
      clearSelection()
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handleAttentionAction(fn: (id: string) => Promise<unknown>, id: string) {
    setBusy(true)
    setActionErr('')
    try {
      await fn(id)
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  /**
   * 三方对比：中台现值（theirs）/ 排除那一刻的基线（base）/ 本机（mine）。
   *
   * 两种 kind 的三份内容存在**不同的地方**：json_key 在 base_value /
   * mine_value / shadowed_value 三个文本字段里，text 在三个 blob 关系里。
   * 读错地方会得到三个空白面板。
   */
  async function openOverrideCompare(o: MachineOverrideRecord) {
    setBusy(true)
    setActionErr('')
    try {
      const blobText = async (hash?: string) =>
        hash ? await getBlob(hash).catch(() => '') : ''
      const isText = o.kind === 'text'
      const theirs = isText
        ? await blobText(o.expand?.shadowed_blob?.hash)
        : o.shadowed_value
      const base = isText ? await blobText(o.expand?.base_blob?.hash) : o.base_value
      const mine = isText ? await blobText(o.expand?.mine_blob?.hash) : o.mine_value

      setCompare({
        path: o.path,
        base: theirs,
        left: { label: t`排除那一刻的基线`, content: base },
        right: { label: machineName.get(o.machine) ?? o.machine, content: mine },
      })
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

      <AttentionBanner
        items={attention}
        machineName={machineName}
        busy={busy}
        onDrop={(id) => void handleAttentionAction(dropOverride, id)}
        onKeep={(id) => void handleAttentionAction(keepOverride, id)}
        onCompare={(o) => void openOverrideCompare(o)}
      />

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
                      bindingMatch={matches[e.id]}
                      onRebind={(providerID) => void handleRebind(e.id, providerID)}
                      onCreateProvider={(presetID, loc) =>
                        void handleCreateProvider(e.id, presetID, loc)
                      }
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
              disabled={busy || selected.size === 0}
              onClick={() => void handleOverride()}
              title={t`勾出的差异点归这台机器所有，中台以后改到别处照旧同步过来`}
              className="rounded bg-emerald-600 px-3 py-1.5 text-xs text-white disabled:opacity-40"
            >
              <Trans>本机保留</Trans>
            </button>

            {/*
              「全局」收进下拉：它是个重量级动作（改全机队的行为），
              不该和常用动作抢同一层视觉权重（spec §6.1）。
            */}
            <div className="relative">
              <button
                type="button"
                disabled={busy}
                onClick={() => setIgnoreMenu((v) => !v)}
                title={t`中台不再向这台机器下发该路径，也不再提醒。想只保留几处差异请用「本机保留」`}
                className="rounded bg-wash px-3 py-1.5 text-xs"
              >
                <Trans>不再管这个路径 ▾</Trans>
              </button>
              {ignoreMenu && (
                <div className="absolute right-0 bottom-full mb-1 w-64 rounded border border-line bg-surface p-3 text-xs shadow-lg">
                  <p className="mb-2 text-ink3">
                    <Trans>
                      中台不再向这台机器下发该路径，也不再提醒。
                      想只保留几处差异请用「本机保留」。
                    </Trans>
                  </p>
                  <label className="mb-2 flex items-center gap-1 text-ink2">
                    <input
                      type="checkbox"
                      checked={globalIgnore}
                      onChange={(e) => setGlobalIgnore(e.target.checked)}
                    />
                    <Trans>对全机队生效</Trans>
                  </label>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      setIgnoreMenu(false)
                      void handleIgnore()
                    }}
                    className="w-full rounded bg-wash px-2 py-1"
                  >
                    <Trans>确认退管</Trans>
                  </button>
                </div>
              )}
            </div>
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
        <ReviewDialog
          events={reviewing}
          checked={reviewChecked}
          busy={busy}
          onToggle={(id) => {
            setReviewChecked((prev) => {
              const next = new Set(prev)
              if (next.has(id)) next.delete(id)
              else next.add(id)
              return next
            })
          }}
          onCancel={() => setReviewing(null)}
          onConfirm={() => void doAdopt([...reviewChecked])}
        />
      )}

      {overriding && (
        <OverrideDialog
          targets={overriding}
          checked={overrideChecked}
          busy={busy}
          onToggle={(targetId, key) => {
            setOverrideChecked((prev) => {
              const next = { ...prev }
              const set = new Set(next[targetId])
              if (set.has(key)) set.delete(key)
              else set.add(key)
              next[targetId] = set
              return next
            })
          }}
          onCancel={() => {
            setOverriding(null)
            setOverrideChecked({})
          }}
          onConfirm={() => void doOverride()}
        />
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

/**
 * 收编前复核弹窗：restore_partial 的条目要逐条看过 diff 再勾。
 * 单独导出是为了能直接测——整页要连 store 与 api 一起 mock。
 */
export function ReviewDialog({
  events,
  checked,
  busy,
  onToggle,
  onCancel,
  onConfirm,
}: {
  events: DriftEvent[]
  checked: Set<string>
  busy: boolean
  onToggle: (id: string) => void
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useLingui()
  return (
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
          {events.map((e) => (
            // 复核的勾选就用卡片自己的复选框。再在卡片外面套一个，两个框
            // 指的是同一件事却各管各的状态，用户不知道该勾哪个。
            <li key={e.id}>
              <DriftCard
                event={e}
                selected={checked.has(e.id)}
                onToggle={() => onToggle(e.id)}
              />
            </li>
          ))}
        </ul>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            className="rounded bg-wash px-3 py-1.5 text-sm"
            onClick={onCancel}
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={busy || checked.size !== events.length}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            onClick={onConfirm}
          >
            {t`确认收编`}
          </button>
        </div>
      </div>
    </div>
  )
}
