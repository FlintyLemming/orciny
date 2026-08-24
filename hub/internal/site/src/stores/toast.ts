import { atom } from 'nanostores'

export interface ToastState {
  message: string
  onUndo?: () => void
  id: number
}

export const $toast = atom<ToastState | null>(null)

let timer: ReturnType<typeof setTimeout> | null = null
let nextId = 1

export function showToast(message: string, onUndo?: () => void, durationMs = 8000) {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  const id = nextId++
  $toast.set({ message, onUndo, id })
  if (durationMs > 0) {
    timer = setTimeout(() => {
      if ($toast.get()?.id === id) {
        $toast.set(null)
      }
      timer = null
    }, durationMs)
  }
}

export function hideToast() {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  $toast.set(null)
}
