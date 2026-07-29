import type { LinguiConfig } from '@lingui/conf'
import { formatter } from '@lingui/format-po'

const config: LinguiConfig = {
  locales: ['zh', 'en'],
  sourceLocale: 'zh',
  catalogs: [{ path: 'src/locales/{locale}', include: ['src'] }],
  // Lingui v6 的 format 收 CatalogFormatter 对象，不再收 'po' 字符串。
  format: formatter({ lineNumbers: false }),
}
export default config
