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
import { BindingBar } from '@/components/BindingBar'
import { CodeEditor } from '@/components/CodeEditor'
import { DiffView } from '@/components/DiffView'
import { PublishDialog } from '@/components/PublishDialog'
import { AddFileDialog } from '@/components/AddFileDialog'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { VersionHistory } from '@/components/VersionHistory'
import {
  getBlob,
  setDraftFile,
  removeDraftFile,
  normalizeFileEntry,
} from '@/lib/api'
import { nextDraftState, type DraftState } from '@/lib/draftState'
import { insertEnvSnippet } from '@/lib/binding'
import { $providers, subscribeProviders } from '@/stores/providers'
import { setBinding } from '@/lib/api'
import { SETTINGS_PATH } from '@/lib/binding'
import { pb } from '@/lib/pb'
import { COLLECTION_ASSIGNMENTS, type Binding, type FileEntry } from '@/types/collections'

export function ConfigSetDetail({ id }: { id: string }) {
  const { t } = useLingui()
  const set = useStore($currentSet)
  const revisions = useStore($revisions)
  const creds = useStore($credentials)
  const providers = useStore($providers)

  const [tab, setTab] = useState<'editor' | 'history'>('editor')
  const [selected, setSelected] = useState('')
  const [content, setContent] = useState('')
  const [baseline, setBaseline] = useState('')
  const [draftState, setDraftState] = useState<DraftState>('clean')
  const [showPublish, setShowPublish] = useState(false)
  const [showAddFile, setShowAddFile] = useState(false)
  const [pendingDeletePath, setPendingDeletePath] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [affected, setAffected] = useState(0)

  useEffect(() => subscribeConfigSet(id), [id])
  useEffect(() => subscribeCredentials(), [])
  useEffect(() => subscribeProviders(), [])
  // settings.json 的当前草稿文本，决定要不要显示「插入 env 片段」。
  const [settingsText, setSettingsText] = useState('')

  const draftFiles: FileEntry[] = useMemo(() => {
    const raw = set?.draft ?? []
    return raw.map((f) => normalizeFileEntry(f as unknown as Record<string, unknown>))
  }, [set])

  const paths = draftFiles.map((f) => f.path)
  const selectedEntry = draftFiles.find((f) => f.path === selected)
  const selectedHash = selectedEntry?.hash ?? ''
  const unsaved = Boolean(selected) && content !== baseline

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

  // 只在「选中路径」或「该文件的 hash」变化时重载内容。
  // 不能依赖整个 draftFiles：保存/实时推送会换新数组引用，
  // 否则会把编辑器里尚未落库的内容冲掉。
  useEffect(() => {
    if (!selected) {
      setContent('')
      setBaseline('')
      return
    }
    if (!selectedHash) {
      setContent('')
      setBaseline('')
      return
    }
    let cancelled = false
    void getBlob(selectedHash)
      .then((text) => {
        if (cancelled) return
        setContent(text)
        setBaseline(text)
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [selected, selectedHash])

  // settings.json 的草稿内容跟着 draftFiles 走：BindingBar 靠它判断
  // 「插入 env 片段」该不该出现。
  const settingsEntry = draftFiles.find((f) => f.path === SETTINGS_PATH)
  const settingsHash = settingsEntry?.hash ?? ''
  useEffect(() => {
    if (!settingsHash) {
      setSettingsText('')
      return
    }
    let cancelled = false
    void getBlob(settingsHash)
      .then((text) => {
        if (!cancelled) setSettingsText(text)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [settingsHash])

  /**
   * 改绑定只落进 draft_binding：换绑定要产生新 Revision，
   * 走正常的草稿/发布流程（M1.5 spec §2.2）。
   */
  async function handleBindingChange(b: Binding | null) {
    if (!set) return
    setError('')
    try {
      await setBinding(set.id, b)
      await reloadConfigSet(set.id)
      setDraftState((s) => nextDraftState(s, { type: 'edit' }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  /** 把占位符形态的 env 块写进草稿的 settings.json；没有该文件就新建。 */
  async function handleInsertSnippet() {
    if (!set || !set.draft_binding) return
    const provider = providers.find((p) => p.id === set.draft_binding?.provider)
    if (!provider) return
    setError('')
    try {
      const next = insertEnvSnippet(settingsText || '{}', provider.auth_field)
      await setDraftFile(set.id, SETTINGS_PATH, next, settingsEntry?.mode || 0o600,
        settingsEntry?.keys)
      setDraftState((s) => nextDraftState(s, { type: 'edit' }))
      await reloadConfigSet(set.id)
      if (selected === SETTINGS_PATH) {
        setContent(next)
        setBaseline(next)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  /** 把编辑器里未落库的内容写入草稿。发布/切文件前必须先调用。 */
  async function flushUnsaved(): Promise<void> {
    if (!selected || !set || content === baseline) return
    const entry = draftFiles.find((f) => f.path === selected)
    await setDraftFile(set.id, selected, content, entry?.mode || 0o644, entry?.keys)
    setDraftState((s) => nextDraftState(s, { type: 'save' }))
    setBaseline(content)
    await reloadConfigSet(set.id)
  }

  async function handleSave() {
    if (!selected || !set) return
    setSaving(true)
    setError('')
    try {
      await flushUnsaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleSelectFile(path: string) {
    if (path === selected) return
    setError('')
    try {
      await flushUnsaved()
      setSelected(path)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function openPublish() {
    setError('')
    setSaving(true)
    try {
      // 发布冻结的是服务端草稿；编辑器本地改动若不先 flush，
      // 会把「新建空文件 + 只在前端写过内容」发成空文件。
      await flushUnsaved()
      setShowPublish(true)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleDeleteFile(path: string) {
    if (!set) return
    try {
      await removeDraftFile(set.id, path)
      if (selected === path) {
        setSelected('')
        setContent('')
        setBaseline('')
      }
      setPendingDeletePath(null)
      setDraftState((s) => nextDraftState(s, { type: 'edit' }))
      await reloadConfigSet(set.id)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function handleAddFile(path: string) {
    if (!set) return
    // 切到新文件前先落盘当前编辑，避免未保存内容丢失
    await flushUnsaved()
    await setDraftFile(set.id, path, '', 0o644)
    setDraftState((s) => nextDraftState(s, { type: 'edit' }))
    await reloadConfigSet(set.id)
    setSelected(path)
    setContent('')
    setBaseline('')
    setShowAddFile(false)
  }

  if (!set) {
    return (
      <p className="text-sm text-ink3">
        <Trans>加载中…</Trans>
      </p>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden">
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
          disabled={draftState === 'clean' || draftState === 'publishing' || saving}
          onClick={() => void openPublish()}
          className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
        >
          {saving ? <Trans>保存中…</Trans> : <Trans>发布</Trans>}
        </button>
      </div>

      {error && <p className="text-sm text-rose-600">{error}</p>}

      {tab === 'history' ? (
        <div className="min-h-0 flex-1 overflow-auto">
          <VersionHistory
            setId={set.id}
            revisions={revisions}
            onRolledBack={() => {
              void reloadConfigSet(set.id)
              setDraftState('clean')
            }}
          />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <BindingBar
          providers={providers}
          binding={set.draft_binding}
          settingsText={settingsText}
          onChange={(b) => void handleBindingChange(b)}
          onInsertSnippet={() => void handleInsertSnippet()}
        />
        <div className="grid min-h-0 flex-1 grid-cols-[220px_1fr] gap-3 overflow-hidden">
          <aside className="flex min-h-0 flex-col overflow-auto rounded-lg border border-line bg-surface p-2">
            <div className="mb-2 flex items-center justify-between px-1">
              <span className="text-xs font-semibold text-ink3">
                <Trans>文件</Trans>
              </span>
              <button type="button" onClick={() => setShowAddFile(true)} className="text-xs text-accent">
                <Trans>添加</Trans>
              </button>
            </div>
            <FileTree paths={paths} selected={selected} onSelect={(p) => void handleSelectFile(p)} />
          </aside>

          <div className="flex min-h-0 flex-col gap-2 overflow-hidden">
            {selected ? (
              <>
                <div className="flex shrink-0 items-center gap-2">
                  <span className="font-mono text-xs text-ink2">{selected}</span>
                  <div className="flex-1" />
                  <button
                    type="button"
                    disabled={saving || !unsaved}
                    onClick={() => void handleSave()}
                    className="rounded bg-wash px-2 py-1 text-xs disabled:opacity-40"
                  >
                    {saving ? <Trans>保存中…</Trans> : unsaved ? <Trans>保存到草稿</Trans> : <Trans>已保存</Trans>}
                  </button>
                  <button
                    type="button"
                    onClick={() => setPendingDeletePath(selected)}
                    className="rounded px-2 py-1 text-xs text-rose-600 hover:bg-wash"
                  >
                    <Trans>移除</Trans>
                  </button>
                </div>
                {/* 编辑器吃剩余高度；必须 overflow-hidden，否则 CM 会按内容撑破 flex 槽盖住下方 diff */}
                <div className="min-h-0 flex-1 overflow-hidden">
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
                {/* diff 按内容增高，但封顶 40%，内部滚动，避免和编辑器抢高度/重叠 */}
                {content !== baseline && (
                  <div className="flex min-h-0 max-h-[40%] shrink-0 flex-col overflow-hidden">
                    <h3 className="mb-1 shrink-0 text-xs font-semibold text-ink3">
                      <Trans>未保存 diff</Trans>
                    </h3>
                    <div className="min-h-0 flex-1 overflow-auto">
                      <DiffView localBefore={baseline} localAfter={content} />
                    </div>
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
        </div>
      )}

      {showAddFile && (
        <AddFileDialog
          existingPaths={paths}
          onSubmit={handleAddFile}
          onClose={() => setShowAddFile(false)}
        />
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

      {pendingDeletePath && (
        <ConfirmDialog
          title={t`移除文件`}
          message={t`从草稿中移除 ${pendingDeletePath}？`}
          confirmLabel={t`移除`}
          danger
          onConfirm={() => void handleDeleteFile(pendingDeletePath)}
          onClose={() => setPendingDeletePath(null)}
        />
      )}
    </div>
  )
}
