import { useEffect, useState } from 'react'
import { Trans } from '@lingui/react/macro'
import { ExternalLink } from 'lucide-react'
import { pb } from '@/lib/pb'
import { Placeholder } from '@/components/Placeholder'

interface HubInfo {
  version: string
  publicKey: string
  fingerprint: string
}

export function Settings() {
  const [info, setInfo] = useState<HubInfo | null>(null)

  useEffect(() => {
    pb.send<HubInfo>('/api/orciny/hub-info', { method: 'GET' })
      .then(setInfo)
      .catch(() => setInfo(null))
  }, [])

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-lg font-semibold">
        <Trans>设置</Trans>
      </h1>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold">
          <Trans>站点信息</Trans>
        </h2>
        <dl className="space-y-2 text-sm">
          <div className="flex gap-4">
            <dt className="w-32 text-ink3">
              <Trans>hub 版本</Trans>
            </dt>
            <dd className="font-mono">{info?.version ?? '—'}</dd>
          </div>
          <div className="flex gap-4">
            <dt className="w-32 text-ink3">
              <Trans>公钥指纹</Trans>
            </dt>
            <dd className="font-mono break-all">{info?.fingerprint ?? '—'}</dd>
          </div>
        </dl>
        <p className="mt-3 text-xs text-ink3">
          <Trans>
            安装 agent 时可用 --hub-key 带上这个指纹做带外校验，防止在首次接入的那一刻被中间人冒充。
          </Trans>
        </p>
      </section>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold">
          <Trans>备份</Trans>
        </h2>
        <p className="mb-3 text-sm text-ink2">
          <Trans>
            备份与恢复在 PocketBase 管理后台操作。注意：hub 的私钥也在数据目录里，
            恢复时若丢了它，所有 agent 都需要重新 enroll。
          </Trans>
        </p>
        <a
          href="/_/#/settings/backups"
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-sm text-accent hover:underline"
        >
          <Trans>打开备份设置</Trans>
          <ExternalLink size={14} aria-hidden />
        </a>
      </section>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold">
          <Trans>agent 更新策略</Trans>
        </h2>
        <Placeholder milestone="M3" />
      </section>
    </div>
  )
}
