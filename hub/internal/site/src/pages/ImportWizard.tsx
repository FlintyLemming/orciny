import { useCallback, useEffect, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { FindingList, type FindingAction } from '@/components/FindingList'
import {
  startImport,
  importFindings,
  extractCredential,
  removeDraftFile,
  publishConfigSet,
  validateConfigSet,
} from '@/lib/api'
import { reloadConfigSet } from '@/stores/configsets'
import { navigate } from '@/router'
import type { Finding, ValidateProblem } from '@/types/collections'
import { pb } from '@/lib/pb'
import { COLLECTION_CONFIG_SETS, type ConfigSetRecord, type FileEntry } from '@/types/collections'
import { normalizeFileEntry } from '@/lib/api'

type Step = 1 | 2 | 3 | 4

/**
 * 导入向导四步（spec §9.1）：采集 → 勾选纳管范围 → 敏感项抽取 → 发布。
 * 入口：机器详情「从本机导入」，或直接带 setId 打开。
 */
export function ImportWizard({
  machineId,
  setId: initialSetId,
}: {
  machineId?: string
  setId?: string
}) {
  const { t } = useLingui()
  const [step, setStep] = useState<Step>(initialSetId ? 2 : 1)
  const [setId, setSetId] = useState(initialSetId ?? '')
  const [files, setFiles] = useState<FileEntry[]>([])
  const [excluded, setExcluded] = useState<Set<string>>(new Set())
  const [findings, setFindings] = useState<Finding[]>([])
  const [handled, setHandled] = useState<Set<string>>(new Set())
  const [problems, setProblems] = useState<ValidateProblem[]>([])
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [waiting, setWaiting] = useState(false)

  const loadDraft = useCallback(async (id: string) => {
    const rec = await pb.collection(COLLECTION_CONFIG_SETS).getOne<ConfigSetRecord>(id)
    const draft = (rec.draft ?? []).map((f) =>
      normalizeFileEntry(f as unknown as Record<string, unknown>),
    )
    setFiles(draft)
  }, [])

  async function handleStart() {
    if (!machineId) return
    setBusy(true)
    setError('')
    setWaiting(true)
    try {
      const res = await startImport(machineId)
      setSetId(res.config_set)
      // 轮询草稿直到有文件（agent 回传 CollectResult）
      const deadline = Date.now() + 60_000
      while (Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 500))
        const rec = await pb.collection(COLLECTION_CONFIG_SETS).getOne<ConfigSetRecord>(res.config_set)
        const draft = rec.draft ?? []
        if (draft.length > 0) {
          await loadDraft(res.config_set)
          setWaiting(false)
          setStep(2)
          return
        }
      }
      setError(t`等待采集超时，请确认机器在线后重试`)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
      setWaiting(false)
    }
  }

  useEffect(() => {
    if (initialSetId) void loadDraft(initialSetId)
  }, [initialSetId, loadDraft])

  async function goFindings() {
    if (!setId) return
    setBusy(true)
    setError('')
    try {
      // 把用户移出纳管的文件从草稿删掉
      for (const p of excluded) {
        await removeDraftFile(setId, p)
      }
      await loadDraft(setId)
      const f = await importFindings(setId)
      setFindings(f ?? [])
      setStep(3)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function onFindingAction(action: FindingAction, f: Finding) {
    if (!setId) return
    const key = `${f.path}:${f.location}`
    if (action === 'extract') {
      const name = window.prompt(t`凭据名称`, f.suggested || f.key.toLowerCase())
      if (!name) return
      setBusy(true)
      try {
        await extractCredential(setId, f.path, f.location, name)
        setHandled((h) => new Set(h).add(key))
        await loadDraft(setId)
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy(false)
      }
      return
    }
    if (action === 'keep') {
      setHandled((h) => new Set(h).add(key))
      return
    }
    if (action === 'exclude') {
      setBusy(true)
      try {
        await removeDraftFile(setId, f.path)
        setFindings((list) => list.filter((x) => x.path !== f.path))
        await loadDraft(setId)
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy(false)
      }
    }
  }

  async function goPublish() {
    if (!setId) return
    setBusy(true)
    setError('')
    try {
      const p = await validateConfigSet(setId)
      setProblems(p ?? [])
      setStep(4)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function handlePublish() {
    if (!setId || problems.length > 0) return
    setBusy(true)
    setError('')
    try {
      await publishConfigSet(setId, note || 'import')
      await reloadConfigSet(setId)
      navigate('configsets', setId)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const pendingFindings = findings.filter((f) => !handled.has(`${f.path}:${f.location}`))

  return (
    <div className="mx-auto max-w-3xl space-y-4">
      <h1 className="text-lg font-semibold">
        <Trans>导入向导</Trans>
      </h1>
      <ol className="flex gap-2 text-xs text-ink3">
        {[1, 2, 3, 4].map((n) => (
          <li
            key={n}
            className={`rounded px-2 py-1 ${step === n ? 'bg-accent-soft text-accent' : 'bg-wash'}`}
          >
            {n === 1 && <Trans>1. 采集</Trans>}
            {n === 2 && <Trans>2. 纳管范围</Trans>}
            {n === 3 && <Trans>3. 敏感项</Trans>}
            {n === 4 && <Trans>4. 发布</Trans>}
          </li>
        ))}
      </ol>

      {error && <p className="text-sm text-rose-600">{error}</p>}

      {step === 1 && (
        <section className="rounded-lg border border-line bg-surface p-5">
          <p className="mb-3 text-sm text-ink2">
            <Trans>从在线机器采集 ~/.claude 配置，生成草稿配置集。</Trans>
          </p>
          <button
            type="button"
            disabled={busy || !machineId}
            onClick={() => void handleStart()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            {waiting ? <Trans>采集中…</Trans> : <Trans>开始采集</Trans>}
          </button>
        </section>
      )}

      {step === 2 && (
        <section className="rounded-lg border border-line bg-surface p-5">
          <p className="mb-3 text-sm text-ink2">
            <Trans>勾选取消不想纳管的文件，然后继续。</Trans>
          </p>
          <ul className="mb-4 max-h-80 space-y-1 overflow-auto font-mono text-xs">
            {files.map((f) => (
              <li key={f.path} className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={!excluded.has(f.path)}
                  onChange={(e) => {
                    setExcluded((prev) => {
                      const n = new Set(prev)
                      if (e.target.checked) n.delete(f.path)
                      else n.add(f.path)
                      return n
                    })
                  }}
                />
                <span className={excluded.has(f.path) ? 'text-ink3 line-through' : ''}>{f.path}</span>
              </li>
            ))}
          </ul>
          <button
            type="button"
            disabled={busy}
            onClick={() => void goFindings()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            <Trans>下一步：敏感项</Trans>
          </button>
        </section>
      )}

      {step === 3 && (
        <section className="space-y-3 rounded-lg border border-line bg-surface p-5">
          <p className="text-sm text-ink2">
            <Trans>
              处理敏感项：抽取为凭据（推荐）、保留明文（进版本历史）、或移出纳管。
            </Trans>
          </p>
          {pendingFindings.length === 0 ? (
            <p className="text-sm text-ink3">
              <Trans>没有待处理的敏感项。</Trans>
            </p>
          ) : (
            <FindingList findings={pendingFindings} onAction={(a, f) => void onFindingAction(a, f)} />
          )}
          <button
            type="button"
            disabled={busy}
            onClick={() => void goPublish()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            <Trans>下一步：发布</Trans>
          </button>
        </section>
      )}

      {step === 4 && (
        <section className="space-y-3 rounded-lg border border-line bg-surface p-5">
          {problems.length > 0 ? (
            <div className="rounded border border-rose-300/50 bg-rose-500/5 p-3 text-sm">
              <p className="mb-1 font-semibold text-rose-600">
                <Trans>校验未通过</Trans>
              </p>
              <ul className="space-y-1 text-xs">
                {problems.map((p, i) => (
                  <li key={i}>
                    {p.path} · {p.kind}: {p.detail}
                  </li>
                ))}
              </ul>
            </div>
          ) : (
            <p className="text-sm text-ink2">
              <Trans>校验通过，可以发布为 v1。</Trans>
            </p>
          )}
          <input
            className="w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
            placeholder={t`版本说明`}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          <div className="flex gap-2">
            <button
              type="button"
              disabled={busy || problems.length > 0}
              onClick={() => void handlePublish()}
              className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
            >
              <Trans>发布</Trans>
            </button>
            <button
              type="button"
              onClick={() => navigate('configsets', setId)}
              className="text-sm text-ink2"
            >
              <Trans>稍后在编辑器里处理</Trans>
            </button>
          </div>
        </section>
      )}
    </div>
  )
}
