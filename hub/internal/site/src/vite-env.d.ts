/// <reference types="vite/client" />

// Lingui 的 vite 插件把 .po 编译成 ES module 后再交给 bundler，
// 因此 TS 侧需要一份模块声明才认得 `import('../locales/zh.po')`。
declare module '*.po' {
  import type { Messages } from '@lingui/core'
  export const messages: Messages
}
