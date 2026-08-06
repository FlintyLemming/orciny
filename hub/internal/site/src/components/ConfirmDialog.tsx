import { useEffect, useRef } from 'react'
import { useLingui } from '@lingui/react/macro'

export function ConfirmDialog({
  title,
  message,
  confirmLabel,
  cancelLabel,
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  title?: string
  message: string
  confirmLabel?: string
  cancelLabel?: string
  /** 危险操作（删除等）用红色主按钮 */
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useLingui()
  const panelRef = useRef<HTMLDivElement>(null)
  const confirmText = confirmLabel ?? t`确认`
  const cancelText = cancelLabel ?? t`取消`

  useEffect(() => {
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="presentation">
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={title || confirmText}
        tabIndex={-1}
        className="w-full max-w-sm rounded-lg border border-line bg-surface p-5 shadow-xl outline-none"
      >
        {title && <h2 className="mb-2 text-base font-semibold">{title}</h2>}
        <p className="mb-4 text-sm text-ink2">{message}</p>
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
            disabled={busy}
            onClick={onConfirm}
            className={
              danger
                ? 'rounded bg-rose-600 px-3 py-1.5 text-sm text-white disabled:opacity-40'
                : 'rounded bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-40'
            }
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
