import { useEffect, useRef, useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'

export function AddFileDialog({
  existingPaths = [],
  onSubmit,
  onClose,
}: {
  existingPaths?: string[]
  onSubmit: (path: string) => void | Promise<void>
  onClose: () => void
}) {
  const { t } = useLingui()
  const [path, setPath] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function handleSubmit() {
    const trimmed = path.trim()
    if (!trimmed) {
      setError(t`请输入路径`)
      return
    }
    if (trimmed.startsWith('/') || trimmed.includes('..')) {
      setError(t`请使用相对 HOME 的路径，不能以 / 开头或包含 ..`)
      return
    }
    if (existingPaths.includes(trimmed)) {
      setError(t`草稿里已有该路径`)
      return
    }
    setSubmitting(true)
    setError('')
    try {
      await onSubmit(trimmed)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setSubmitting(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog">
      <div
        className="w-full max-w-md rounded-lg border border-line bg-surface p-5 shadow-xl"
        aria-modal="true"
        aria-label={t`添加文件`}
      >
        <h2 className="mb-3 text-base font-semibold">
          <Trans>添加文件</Trans>
        </h2>
        <p className="mb-3 text-sm text-ink2">
          <Trans>路径相对于 HOME，例如 .claude/settings.json</Trans>
        </p>
        <label className="mb-1 block text-xs text-ink3" htmlFor="add-file-path">
          <Trans>文件路径</Trans>
        </label>
        <input
          id="add-file-path"
          ref={inputRef}
          className="mb-3 w-full rounded border border-line bg-wash px-2 py-1.5 font-mono text-sm"
          value={path}
          onChange={(e) => {
            setPath(e.target.value)
            if (error) setError('')
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void handleSubmit()
            }
          }}
          placeholder={t`例如 .claude/CLAUDE.md`}
          disabled={submitting}
          spellCheck={false}
          autoComplete="off"
        />
        {error && <p className="mb-3 text-sm text-rose-600">{error}</p>}
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            disabled={submitting}
            className="rounded px-3 py-1.5 text-sm text-ink2 hover:bg-wash disabled:opacity-40"
          >
            <Trans>取消</Trans>
          </button>
          <button
            type="button"
            disabled={submitting || !path.trim()}
            onClick={() => void handleSubmit()}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            {submitting ? <Trans>添加中…</Trans> : <Trans>添加</Trans>}
          </button>
        </div>
      </div>
    </div>
  )
}
