import { Trans } from '@lingui/react/macro'
import type { MachineStatus } from '@/types/collections'

const styles: Record<MachineStatus, string> = {
  online: 'bg-[var(--good)]',
  offline: 'bg-[var(--ink3)]',
  paused: 'bg-[var(--warn)]',
}

export function StatusDot({ status }: { status: MachineStatus }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span className={`inline-block h-2 w-2 rounded-full ${styles[status]}`} aria-hidden />
      {status === 'online' && <Trans>在线</Trans>}
      {status === 'offline' && <Trans>离线</Trans>}
      {status === 'paused' && <Trans>已暂停</Trans>}
    </span>
  )
}
