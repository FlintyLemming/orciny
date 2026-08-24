import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { $toast, hideToast } from '@/stores/toast'

export function Toast() {
  const toast = useStore($toast)
  if (!toast) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-6 right-6 z-50 flex items-center gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-lg"
    >
      <span className="text-sm text-ink">{toast.message}</span>
      {toast.onUndo && (
        <button
          type="button"
          className="rounded bg-accent px-2 py-1 text-xs text-white hover:bg-accent/90"
          onClick={() => {
            const cb = toast.onUndo
            hideToast()
            cb?.()
          }}
        >
          <Trans>撤销</Trans>
        </button>
      )}
    </div>
  )
}
