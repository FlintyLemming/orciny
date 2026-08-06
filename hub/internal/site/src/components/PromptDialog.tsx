import { useEffect, useRef, useState } from 'react'
import { useLingui } from '@lingui/react/macro'

export function PromptDialog({
  title,
  message,
  label,
  defaultValue = '',
  placeholder,
  confirmLabel,
  cancelLabel,
  busy,
  onSubmit,
  onClose,
}: {
  title: string
  message?: string
  label?: string
  defaultValue?: string
  placeholder?: string
  confirmLabel?: string
  cancelLabel?: string
  busy?: boolean
  onSubmit: (value: string) => void
  onClose: () => void
}) {
  const { t } = useLingui()
  const [value, setValue] = useState(defaultValue)
  const inputRef = useRef<HTMLInputElement>(null)
  const confirmText = confirmLabel ?? t`确定`
  const cancelText = cancelLabel ?? t`取消`

  useEffect(() => {
    inputRef.current?.focus()
    inputRef.current?.select()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  function handleSubmit() {
    const trimmed = value.trim()
    if (!trimmed || busy) return
    onSubmit(trimmed)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation">
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="w-full max-w-md rounded-lg border border-line bg-surface p-5 shadow-xl"
      >
        <h2 className="mb-3 text-base font-semibold">{title}</h2>
        {message && <p className="mb-3 text-sm text-ink2">{message}</p>}
        {label && (
          <label className="mb-1 block text-xs text-ink3" htmlFor="prompt-dialog-input">
            {label}
          </label>
        )}
        <input
          id="prompt-dialog-input"
          ref={inputRef}
          className="mb-4 w-full rounded border border-line bg-wash px-2 py-1.5 text-sm"
          value={value}
          disabled={busy}
          placeholder={placeholder}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              handleSubmit()
            }
          }}
        />
        <div className="flex justify-end gap-2">
          <button
            type="button"
            disabled={busy}
            onClick={onClose}
            className="rounded px-3 py-1.5 text-sm text-ink2 hover:bg-wash disabled:opacity-40"
          >
            {cancelText}
          </button>
          <button
            type="button"
            disabled={busy || !value.trim()}
            onClick={handleSubmit}
            className="rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40"
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
