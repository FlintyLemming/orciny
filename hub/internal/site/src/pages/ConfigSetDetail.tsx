import { useEffect, useMemo, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { ArrowLeft } from 'lucide-react'
import {
  $currentSet,
  $revisions,
  subscribeConfigSet,
  reloadConfigSet,
} from '@/stores/configsets'
import { $credentials, subscribeCredentials } from '@/stores/credentials'
import { navigate } from '@/router'
import { FileTree } from '@/components/FileTree'
import { CodeEditor } from '@/components/CodeEditor'
import { DiffView } from '@/components/DiffView'
import { PublishDialog } from '@/components/PublishDialog'
import { VersionHistory } from '@/components/VersionHistory'
import {
  getBlob,
  setDraftFile,
  removeDraftFile,
  normalizeFileEntry,
} from '@/lib/api'
import { nextDraftState, type DraftState } from '@/lib/draftState'
import { pb } from '@/lib/pb'
import { COLLECTION_ASSIGNMENTS, type FileEntry } from '@/types/collections'

export function ConfigSetDetail({ id }: { id: string }) {
  const { t } = useLingui()
  const set = useStore($currentSet)
  const revisions = useStore($revisions)
  const creds = useStore($credentials)

  const [tab, setTab] = useState<'editor' | 'history'>('editor')
  const [selected, setSelected] = useState('')
  const [content, setContent] = useState('')
  const [baseline, setBaseline] = useState('')
  const [draftState, setDraftState] = useState<DraftState>('clean')
  const [showPublish, setShowPublish] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [affected, setAffected] = useState(0)

  useEffect(() => subscribeConfigSet(id), [id])
  useEffect(() => subscribeCredentials(), [])

  const draftFiles: FileEntry[] = useMemo(() => {
    const raw = set?.draft ?? []
    return raw.map((f) => normalizeFileEntry(f as unknown as Record<string, unknown>))
  }, [set])

  const paths = draftFiles.map((f) => f.path)

  const knownRefs = useMemo(() => {
    const refs = creds.map((c) => `cred.${c.name}`)
    const draftRefs = set?.draft_refs
    if (draftRefs?.vars) {
      for (const v of draftRefs.vars) refs.push(`var.${v}`)
    }
    return refs
  }, [creds, set])

  useEffect(() => {
    void pb
      .collection(COLLECTION_ASSIGNMENTS)
      .getFullList({ filter: pb.filter('config_set = {:s}', { s: id }) })
      .then((list) => setAffected(list.length))
      .catch(() => setAffected(0))
  }, [id])

  useEffect(() => {
    if (!selected) {
      setContent('')
      setBaseline('')
      return
    }
    const entry = draftFiles.find((f) => f.path === selected)
    if (!entry?.hash) {
      setContent('')
      setBaseline('')
      return
    }
    let cancelled = false
    void getBlob(entry.hash).then((text) => {
      if (cancelled) return
      setContent(text)
      setBaseline(text)
    }).catch((e: Error) => {
      if (!cancelled) setError(e.message)
    })
    return () => {
      cancelled = true
    }
  }, [selected, draftFiles])

  async function handleSave() {
    if (!selected || !set) return
    setSaving(true)
    setError('')
    try {
      const entry = draftFiles.find((f) => f.path === selected)
      await setDraftFile(set.id, selected, content, entry?.mode || 0o644, entry?.keys)
      setDraftState((s) => nextDraftState(s, { type: 'save' }))
      setBaseline(content)
      await reloadConfigSet(set.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleDeleteFile() {
    if (!selected || !set) return
    if (!window.confirm(t`从草稿中移除 ${selected}？`)) return
    try {
      await removeDraftFile(set.id, selected)
      setSelected('')
      setDraftState((s) => nextDraftState(s, { type: 'edit' }))
      await reloadConfigSet(set.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function handleAddFile() {
    if (!set) return
    const path = window.prompt(t`相对 HOME 的路径，例如 .claude/CLAUDE.md`)
    if (!path) return
    try {
      await setDraftFile(set.id, path, '', 0o644)
      setDraftState((s) => nextDraftState(s, { type: 'edit' }))
      await reloadConfigSet(set.id)
      setSelected(path)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  if (!set) {
    return (
      <p className="text-sm text-ink3">
        <Trans>加载中…</Trans>
      </p>
    )
  }

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => navigate('configsets')}
          className="flex items-center gap-1 text-sm text-ink2 hover:text-ink"
        >
          <ArrowLeft size={14} />
          <Trans>返回</Trans>
        </button>
        <h1 className="text-lg font-semibold">{set.name}</h1>
        <span className="rounded bg-wash px-2 py-0.5 text-xs text-ink3">
          {draftState === 'clean' && <Trans>已对齐 head</Trans>}
          {draftState === 'dirty' && <Trans>有未发布改动</Trans>}
          {draftState === 'publishing' && <Trans>发布中</Trans>}
          {draftState === 'failed' && <Trans>发布失败</Trans>}
        </span>
        <div className="flex-1" />
        <button
          type="button"
          onClick={() => setTab('editor')}
          className={`text-sm ${tab === 'editor' ? 'text-accent' : 'text-ink2'}`}
        >
          <Trans>编辑器</Trans>
        </button>
        <button
          type="button"
          onClick={() => setTab('history')}
          className={`text-sm ${tab === 'history' ? 'text-accent' : 'text-ink2'}`}
        >
          <Trans>版本历史</Trans>
        </button>
        <button
          type="button"
          disabled={draftState === 'clean' || draftState === 'publishing'}
          onClick={() => setShowPublish(true)}
          className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
        >
          <Trans>发布</Trans>
        </button>
      </div>

      {error && <p className="text-sm text-rose-600">{error}</p>}

      {tab === 'history' ? (
        <VersionHistory
          setId={set.id}
          revisions={revisions}
          onRolledBack={() => {
            void reloadConfigSet(set.id)
            setDraftState('clean')
          }}
        />
      ) : (
        <div className="grid min-h-0 flex-1 grid-cols-[220px_1fr] gap-3">
          <aside className="flex flex-col overflow-auto rounded-lg border border-line bg-surface p-2">
            <div className="mb-2 flex items-center justify-between px-1">
              <span className="text-xs font-semibold text-ink3">
                <Trans>文件</Trans>
              </span>
              <button type="button" onClick={() => void handleAddFile()} className="text-xs text-accent">
                <Trans>添加</Trans>
              </button>
            </div>
            <FileTree paths={paths} selected={selected} onSelect={setSelected} />
          </aside>

          <div className="flex min-h-0 flex-col gap-2">
            {selected ? (
              <>
                <div className="flex items-center gap-2">
                  <span className="font-mono text-xs text-ink2">{selected}</span>
                  <div className="flex-1" />
                  <button
                    type="button"
                    disabled={saving || content === baseline}
                    onClick={() => void handleSave()}
                    className="rounded bg-wash px-2 py-1 text-xs disabled:opacity-40"
                  >
                    {saving ? <Trans>保存中…</Trans> : <Trans>保存到草稿</Trans>}
                  </button>
                  <button
                    type="button"
                    onClick={() => void handleDeleteFile()}
                    className="rounded px-2 py-1 text-xs text-rose-600 hover:bg-wash"
                  >
                    <Trans>移除</Trans>
                  </button>
                </div>
                <div className="min-h-0 flex-1">
                  <CodeEditor
                    value={content}
                    path={selected}
                    knownRefs={knownRefs}
                    onChange={(v) => {
                      setContent(v)
                      if (v !== baseline) setDraftState((s) => nextDraftState(s, { type: 'edit' }))
                    }}
                  />
                </div>
                {content !== baseline && (
                  <div>
                    <h3 className="mb-1 text-xs font-semibold text-ink3">
                      <Trans>未保存 diff</Trans>
                    </h3>
                    <DiffView localBefore={baseline} localAfter={content} />
                  </div>
                )}
              </>
            ) : (
              <div className="flex flex-1 items-center justify-center rounded-lg border border-dashed border-line text-sm text-ink3">
                <Trans>选择左侧文件开始编辑</Trans>
              </div>
            )}
          </div>
        </div>
      )}

      {showPublish && (
        <PublishDialog
          setId={set.id}
          draftState={draftState}
          affectedMachines={affected}
          onClose={() => setShowPublish(false)}
          onPublished={() => {
            setShowPublish(false)
            setDraftState((s) => nextDraftState(s, { type: 'published' }))
            // publishing 中间态由对话框内部处理；这里直接到 clean
            setDraftState('clean')
            void reloadConfigSet(set.id)
          }}
        />
      )}
    </div>
  )
}
