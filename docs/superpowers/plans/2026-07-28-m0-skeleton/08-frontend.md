# M0 计划 8 · 前端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `mock/ui-mock.html` 的设计语言落成真实组件体系，实现 M0 的六个屏：登录、布局壳、机器列表、机器详情、添加机器弹窗、设置页。

**Architecture:** React 19 + Vite + TypeScript，直连 PocketBase JS SDK。列表与事件流走 realtime 订阅，订阅统一收在 `stores/` 下的 nanostores，组件不直接调 SDK；触发动作走 `/api/orciny/*`。collection 的 TS 类型集中在 `types/collections.ts`，是前端与 schema 的唯一契约点。

**Tech Stack:** React 19 · Vite 7 · TypeScript 5 · Tailwind CSS v4 · nanostores + @nanostores/react · PocketBase JS SDK · Lingui（中英）· lucide-react（图标）

**上位文档：** [统括计划](00-overview.md) · [spec §10 §5.2 §5.4](../../specs/2026-07-28-m0-skeleton-design.md) · [UI 原型](../../../../mock/ui-mock.html)

## Global Constraints

- 前端源码位于 `hub/internal/site/`，构建产物 `hub/internal/site/dist/` 被 `//go:embed all:dist` 内嵌。
- **M0 不写前端自动化测试**（spec §10.3 的明示取舍）：三个屏、逻辑集中在数据订阅上，写测试成本远高于收益，靠真机验收覆盖。因此本计划的每个任务以**具体的手工验收步骤**替代测试步骤——每一步都要真的在浏览器里做一遍，看到了才打勾。
- 用户可见文案一律走 Lingui，不得在组件里硬编码中文或英文字面量。
- 组件不直接调 PocketBase SDK；数据一律经 `stores/`。
- 深浅双主题（暖黑 / 暖纸），键盘可达。
- 未实现的页面用统一的 `<Placeholder milestone="M1" />`，不各自编空状态。
- 每个任务以一次 Conventional Commits 风格的提交结束。

---

## 文件结构

| 文件 | 职责 |
|---|---|
| `hub/internal/site/package.json` · `vite.config.ts` · `tsconfig.json` | 构建配置，产物输出到 `dist/` |
| `hub/internal/site/src/styles/tokens.css` | 设计 token（照搬 mock 的 CSS 变量） |
| `hub/internal/site/src/lib/pb.ts` | PocketBase 客户端单例 |
| `hub/internal/site/src/types/collections.ts` | collection 的 TS 类型，与 schema 的唯一契约点 |
| `hub/internal/site/src/i18n/` | Lingui 装配与 `zh` / `en` 词条 |
| `hub/internal/site/src/stores/auth.ts` | 登录状态 |
| `hub/internal/site/src/stores/theme.ts` | 主题与语言偏好 |
| `hub/internal/site/src/stores/machines.ts` | 机器列表 + realtime |
| `hub/internal/site/src/stores/events.ts` | 事件流 + realtime |
| `hub/internal/site/src/components/` | `Placeholder` `StatusDot` `Shell` `Sidebar` 等 |
| `hub/internal/site/src/pages/` | `Login` `Machines` `MachineDetail` `Settings` |
| `hub/internal/site/dist/` | 构建产物（`.gitignore` 只保留 `index.html` 占位） |
| `hub/internal/site/dev.go` | `//go:build dev`：反向代理到 Vite |
| `hub/internal/site/embed.go` | 修改：加 `//go:build !dev` |
| `Makefile` | 修改：`dev` 目标 |

---

## Task 1: 脚手架、设计 token 与 dev 模式

**Files:**
- Create: `hub/internal/site/package.json`, `vite.config.ts`, `tsconfig.json`, `index.html`, `src/main.tsx`, `src/App.tsx`, `src/styles/tokens.css`, `src/lib/pb.ts`, `src/types/collections.ts`, `hub/internal/site/dev.go`
- Modify: `hub/internal/site/embed.go`（加 build tag）, `hub/hub.go`（dev 模式下不挂静态路由）, `Makefile`（`dev` 目标）, `.gitignore`

**Interfaces:**
- Consumes: hub 的 `/api/*` 与 PocketBase 原生 API
- Produces:
  ```ts
  // src/lib/pb.ts
  export const pb: PocketBase
  // src/types/collections.ts
  export type MachineStatus = 'online' | 'offline' | 'paused'
  export interface MachineRecord { id, name, fingerprint, pub_key, hostname, os, arch,
    agent_version, tool_versions, status, last_seen, created, updated }
  export interface EventRecord { id, kind, machine, detail, created }
  export type EventKind = 'machine.enrolled' | 'machine.re-enrolled' | 'machine.connected'
    | 'machine.disconnected' | 'machine.removed' | 'token.issued' | 'auth.failed'
  ```
  ```go
  // hub/internal/site（dev tag）
  func DevProxy() http.Handler
  ```

- [ ] **Step 1: 初始化前端工程**

```bash
cd hub/internal/site
npm create vite@latest . -- --template react-ts
npm install
npm install pocketbase nanostores @nanostores/react lucide-react
npm install @lingui/core @lingui/react
npm install -D @lingui/cli @lingui/vite-plugin tailwindcss @tailwindcss/vite
```

删掉模板自带的 `src/App.css`、`src/assets/`、`public/vite.svg`。

- [ ] **Step 2: 配置 Vite**

写 `hub/internal/site/vite.config.ts`：

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { lingui } from '@lingui/vite-plugin'
import path from 'node:path'

export default defineConfig({
  plugins: [
    react({ babel: { plugins: ['@lingui/babel-plugin-lingui-macro'] } }),
    tailwindcss(),
    lingui(),
  ],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  build: {
    // 产物必须落在 dist/，Go 侧 //go:embed all:dist 认这个目录
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    strictPort: true,
    // 开发时前端直连 hub，避免跨域与 cookie 问题
    proxy: {
      '/api': 'http://127.0.0.1:8090',
      '/_': 'http://127.0.0.1:8090',
    },
  },
})
```

- [ ] **Step 3: 写设计 token**

创建 `hub/internal/site/src/styles/tokens.css`（**照抄 `mock/ui-mock.html` 的 `:root` 变量，不要另发明配色**）：

```css
@import 'tailwindcss';

:root {
  --page: #faf9f6; --surface: #fffefb; --wash: #f0eee8; --wash2: #e9e6df;
  --ink: #1c1b18; --ink2: #57544c; --ink3: #8c887e;
  --line: #e6e3dc; --hair: rgba(28, 27, 24, .10);
  --accent: #b45309; --accent-bright: #c2540f; --accent-soft: rgba(194, 84, 15, .09);
  --pri-bg: #c2540f; --pri-ink: #fff8f2;
  --good: #0ca30c; --good-text: #21691f; --good-bg: rgba(12, 163, 12, .10);
  --warn: #d9a94a; --warn-text: #8a6414; --warn-bg: rgba(217, 169, 74, .16);
  --crit: #d03b3b; --crit-text: #b3372e; --crit-bg: rgba(208, 59, 59, .10);
  --info-text: #1c5cab; --info-bg: rgba(42, 120, 214, .10);
  --brand-fill: #b45309;
  --mono: ui-monospace, "SF Mono", SFMono-Regular, Menlo, Consolas, monospace;
  --sans: system-ui, -apple-system, "PingFang SC", "Hiragino Sans GB",
          "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
}

/* 跟随系统 */
@media (prefers-color-scheme: dark) {
  :root:where(:not([data-theme="light"])) {
    --page: #131210; --surface: #1c1a17; --wash: #262320; --wash2: #2e2b26;
    --ink: #f2efe9; --ink2: #b6b2aa; --ink3: #8c887e;
    --line: #2e2b26; --hair: rgba(242, 239, 233, .10);
    --accent: #f08c4a; --accent-bright: #e8721e; --accent-soft: rgba(232, 114, 30, .13);
    --good-text: #4fbf5e; --good-bg: rgba(12, 163, 12, .16);
    --warn-text: #d9a94a; --warn-bg: rgba(217, 169, 74, .13);
    --crit: #d03b3b; --crit-text: #e66767; --crit-bg: rgba(208, 59, 59, .16);
    --info-text: #86b6ef; --info-bg: rgba(57, 135, 229, .14);
    --brand-fill: #e8721e;
  }
}

/* 显式切换（用户点了主题开关） */
:root[data-theme="dark"] {
  --page: #131210; --surface: #1c1a17; --wash: #262320; --wash2: #2e2b26;
  --ink: #f2efe9; --ink2: #b6b2aa; --ink3: #8c887e;
  --line: #2e2b26; --hair: rgba(242, 239, 233, .10);
  --accent: #f08c4a; --accent-bright: #e8721e; --accent-soft: rgba(232, 114, 30, .13);
  --good-text: #4fbf5e; --good-bg: rgba(12, 163, 12, .16);
  --warn-text: #d9a94a; --warn-bg: rgba(217, 169, 74, .13);
  --crit: #d03b3b; --crit-text: #e66767; --crit-bg: rgba(208, 59, 59, .16);
  --info-text: #86b6ef; --info-bg: rgba(57, 135, 229, .14);
  --brand-fill: #e8721e;
}

@theme inline {
  --color-page: var(--page);
  --color-surface: var(--surface);
  --color-wash: var(--wash);
  --color-wash2: var(--wash2);
  --color-ink: var(--ink);
  --color-ink2: var(--ink2);
  --color-ink3: var(--ink3);
  --color-line: var(--line);
  --color-accent: var(--accent);
  --color-good: var(--good-text);
  --color-warn: var(--warn-text);
  --color-crit: var(--crit-text);
  --font-sans: var(--sans);
  --font-mono: var(--mono);
}

html, body, #root { height: 100%; }
body {
  background: var(--page);
  color: var(--ink);
  font-family: var(--sans);
  -webkit-font-smoothing: antialiased;
}
```

- [ ] **Step 4: 写 PB 客户端与 collection 类型**

创建 `hub/internal/site/src/lib/pb.ts`：

```ts
import PocketBase from 'pocketbase'

// 同源部署：hub 既提供 API 也提供前端，因此 baseUrl 就是当前 origin。
// 开发时由 Vite 的 proxy 转给 127.0.0.1:8090。
export const pb = new PocketBase(window.location.origin)

// realtime 订阅在开发热更新时会残留，autoCancellation 关掉可避免
// 组件重挂载时误取消仍在用的请求。
pb.autoCancellation(false)
```

创建 `hub/internal/site/src/types/collections.ts`：

```ts
/**
 * collection 的类型定义。
 *
 * 这是前端与数据库 schema 的**唯一契约点**（spec §10.2）：前端直连 PB SDK
 * 换来了实时上下线零后端代码，代价是 schema 改名会打到前端。把结构集中
 * 声明在这里，改动至少能被类型检查抓到。
 */

export type MachineStatus = 'online' | 'offline' | 'paused'

export interface MachineRecord {
  id: string
  name: string
  fingerprint: string
  pub_key: string
  hostname: string
  os: string
  arch: string
  agent_version: string
  tool_versions: Record<string, string> | null
  status: MachineStatus
  /** 仅在状态变化时写入（spec §6.5）：online 时不代表「刚刚心跳过」 */
  last_seen: string
  created: string
  updated: string
}

export type EventKind =
  | 'machine.enrolled'
  | 'machine.re-enrolled'
  | 'machine.connected'
  | 'machine.disconnected'
  | 'machine.removed'
  | 'token.issued'
  | 'auth.failed'

export interface EventRecord {
  id: string
  kind: EventKind
  machine: string
  detail: Record<string, unknown> | null
  created: string
}

export const COLLECTION_MACHINES = 'machines'
export const COLLECTION_EVENTS = 'events'
```

- [ ] **Step 5: 写 dev 模式反向代理**

改 `hub/internal/site/embed.go` 的首行为：

```go
//go:build !dev
```

创建 `hub/internal/site/dev.go`：

```go
//go:build dev

package site

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// DevTarget 是 Vite dev server 的地址。
const DevTarget = "http://127.0.0.1:5173"

// DistFS 在 dev 构建下不可用——请用 DevProxy。
// 保留同名函数是为了让 hub 侧的调用点不必分叉。
func DistFS() fs.FS { return nil }

// DevProxy 把非 /api/* 请求反代给 Vite，从而支持 HMR。
//
// 前端 embed 在生产是对的，在开发是灾难——改一行 CSS 要重编译 Go
// （spec §5.4）。用 build tag 分离两种形态，生产构建不含这段代码。
func DevProxy() http.Handler {
	target, err := url.Parse(DevTarget)
	if err != nil {
		panic("site: dev 目标地址不合法: " + err.Error())
	}
	return httputil.NewSingleHostReverseProxy(target)
}
```

对应地，`hub/hub.go` 的 `registerUI` 要分叉。新建两个小文件而不是在函数里写 `if`——build tag 只能作用于整个文件：

`hub/ui_prod.go`：

```go
//go:build !dev

package hub

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"
)

// registerUI 挂内嵌前端。catch-all 在 net/http 的 ServeMux 里优先级最低，
// 不会盖住 /api/* 与 PocketBase 自带的 /_/*。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	e.Router.GET("/{path...}", apis.Static(site.DistFS(), true))
	return nil
}
```

`hub/ui_dev.go`：

```go
//go:build dev

package hub

import (
	"github.com/pocketbase/pocketbase/core"

	"github.com/FlintyLemming/orciny/hub/internal/site"
)

// registerUI 在 dev 构建下把前端请求反代给 Vite。
func (h *Hub) registerUI(e *core.ServeEvent) error {
	proxy := site.DevProxy()
	e.Router.GET("/{path...}", func(re *core.RequestEvent) error {
		proxy.ServeHTTP(re.Response, re.Request)
		return nil
	})
	e.App.Logger().Info("dev 模式：前端请求反代至 " + site.DevTarget)
	return nil
}
```

把原来 `hub/hub.go` 里的 `registerUI` 删掉（它已被这两个文件取代）。

- [ ] **Step 6: 加 Makefile 的 dev 目标**

在 `Makefile` 追加：

```make
## dev: 同时起 Vite、hub（dev tag）与一个本地 agent
dev:
	@echo "前端: http://127.0.0.1:5173   hub: http://127.0.0.1:8090"
	@trap 'kill 0' EXIT; \
	(cd hub/internal/site && npm run dev) & \
	go run -tags dev ./cmd/orciny serve --http=127.0.0.1:8090 --dir=./pb_data & \
	wait
```

并把 `.PHONY` 那行补上 `dev`。

- [ ] **Step 7: 验收**

```bash
cd hub/internal/site && npm run build     # 期望：产物落在 dist/
cd ../../.. && go build ./...             # 期望：内嵌构建通过
go build -tags dev ./...                  # 期望：dev 构建也通过
make dev                                  # 另开浏览器访问 127.0.0.1:8090
```

浏览器里应看到 Vite 的默认页经由 hub 反代出来；改一行 `tokens.css` 应当热更新，**不需要**重编译 Go。确认后 Ctrl-C。

- [ ] **Step 8: 提交**

```bash
git add hub Makefile .gitignore
git commit -m "feat: 前端脚手架、设计 token 与 dev 反向代理"
```

---

## Task 2: i18n、主题与登录

**Files:**
- Create: `src/i18n/index.ts`, `src/locales/zh.po`, `src/locales/en.po`, `lingui.config.ts`, `src/stores/theme.ts`, `src/stores/auth.ts`, `src/pages/Login.tsx`, `src/components/ThemeToggle.tsx`, `src/components/LangToggle.tsx`
- Modify: `src/main.tsx`, `src/App.tsx`

**Interfaces:**
- Consumes: `pb`
- Produces:
  ```ts
  // stores/auth.ts
  export const $authed: ReadableAtom<boolean>
  export const $authEmail: ReadableAtom<string>
  export async function login(email: string, password: string): Promise<void>
  export function logout(): void
  // stores/theme.ts
  export type Theme = 'light' | 'dark' | 'system'
  export const $theme: WritableAtom<Theme>
  export const $locale: WritableAtom<'zh' | 'en'>
  export function applyTheme(t: Theme): void
  ```

- [ ] **Step 1: 配置 Lingui**

创建 `hub/internal/site/lingui.config.ts`：

```ts
import type { LinguiConfig } from '@lingui/conf'

const config: LinguiConfig = {
  locales: ['zh', 'en'],
  sourceLocale: 'zh',
  catalogs: [{ path: 'src/locales/{locale}', include: ['src'] }],
  format: 'po',
}
export default config
```

创建 `hub/internal/site/src/i18n/index.ts`：

```ts
import { i18n } from '@lingui/core'

export type Locale = 'zh' | 'en'

export async function activateLocale(locale: Locale) {
  const { messages } = await import(`../locales/${locale}.po`)
  i18n.load(locale, messages)
  i18n.activate(locale)
}

/** 浏览器语言里带 zh 就用中文，否则英文。 */
export function detectLocale(): Locale {
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}
```

- [ ] **Step 2: 写主题与语言 store**

创建 `hub/internal/site/src/stores/theme.ts`：

```ts
import { atom } from 'nanostores'
import { activateLocale, detectLocale, type Locale } from '@/i18n'

export type Theme = 'light' | 'dark' | 'system'

const THEME_KEY = 'orciny.theme'
const LOCALE_KEY = 'orciny.locale'

export const $theme = atom<Theme>((localStorage.getItem(THEME_KEY) as Theme) ?? 'system')
export const $locale = atom<Locale>((localStorage.getItem(LOCALE_KEY) as Locale) ?? detectLocale())

/** system 时移除 data-theme，交回给 prefers-color-scheme。 */
export function applyTheme(t: Theme) {
  const root = document.documentElement
  if (t === 'system') root.removeAttribute('data-theme')
  else root.setAttribute('data-theme', t)
  localStorage.setItem(THEME_KEY, t)
  $theme.set(t)
}

export async function applyLocale(l: Locale) {
  await activateLocale(l)
  document.documentElement.lang = l
  localStorage.setItem(LOCALE_KEY, l)
  $locale.set(l)
}
```

- [ ] **Step 3: 写认证 store**

创建 `hub/internal/site/src/stores/auth.ts`：

```ts
import { atom } from 'nanostores'
import { pb } from '@/lib/pb'

/**
 * M0 用 PocketBase superuser 作为唯一身份，不建 users collection（spec §5.3）。
 * 已知取舍：前端持有的 token 等价于完整数据库权限——单管理员场景可接受，
 * 省掉一整套用户与角色代码。v2 做微团队时再引入 users collection。
 */
export const $authed = atom(pb.authStore.isValid)
export const $authEmail = atom(pb.authStore.record?.email ?? '')

pb.authStore.onChange(() => {
  $authed.set(pb.authStore.isValid)
  $authEmail.set((pb.authStore.record?.email as string) ?? '')
})

export async function login(email: string, password: string) {
  await pb.collection('_superusers').authWithPassword(email, password)
}

export function logout() {
  pb.authStore.clear()
}
```

- [ ] **Step 4: 写登录页**

创建 `hub/internal/site/src/pages/Login.tsx`：

```tsx
import { useState } from 'react'
import { Trans, useLingui } from '@lingui/react/macro'
import { login } from '@/stores/auth'

export function Login() {
  const { t } = useLingui()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await login(email, password)
    } catch {
      // 不回显后端原文：登录失败的原因对攻击者比对用户更有价值。
      setError(t`邮箱或密码不正确`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center px-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm rounded-lg border border-line bg-surface p-8"
      >
        <h1 className="mb-1 text-xl font-semibold">Orciny</h1>
        <p className="mb-6 text-sm text-ink2">
          <Trans>用管理员账号登录</Trans>
        </p>

        <label className="mb-1 block text-sm" htmlFor="email">
          <Trans>邮箱</Trans>
        </label>
        <input
          id="email"
          type="email"
          required
          autoFocus
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="mb-4 w-full rounded border border-line bg-page px-3 py-2"
        />

        <label className="mb-1 block text-sm" htmlFor="password">
          <Trans>密码</Trans>
        </label>
        <input
          id="password"
          type="password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mb-6 w-full rounded border border-line bg-page px-3 py-2"
        />

        {error && (
          <p role="alert" className="mb-4 text-sm text-crit">
            {error}
          </p>
        )}

        <button
          type="submit"
          disabled={busy}
          className="w-full rounded bg-[var(--pri-bg)] px-4 py-2 text-[var(--pri-ink)] disabled:opacity-60"
        >
          {busy ? <Trans>登录中…</Trans> : <Trans>登录</Trans>}
        </button>
      </form>
    </div>
  )
}
```

- [ ] **Step 5: 写主题与语言开关**

创建 `hub/internal/site/src/components/ThemeToggle.tsx`：

```tsx
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { Monitor, Moon, Sun } from 'lucide-react'
import { $theme, applyTheme, type Theme } from '@/stores/theme'

const options: { value: Theme; icon: typeof Sun }[] = [
  { value: 'light', icon: Sun },
  { value: 'dark', icon: Moon },
  { value: 'system', icon: Monitor },
]

export function ThemeToggle() {
  const theme = useStore($theme)
  return (
    <div role="group" aria-label="theme" className="flex gap-1">
      {options.map(({ value, icon: Icon }) => (
        <button
          key={value}
          type="button"
          aria-pressed={theme === value}
          onClick={() => applyTheme(value)}
          className={`rounded p-1.5 ${theme === value ? 'bg-wash2' : 'hover:bg-wash'}`}
        >
          <Icon size={16} aria-hidden />
          <span className="sr-only">
            {value === 'light' ? <Trans>浅色</Trans> : value === 'dark' ? <Trans>深色</Trans> : <Trans>跟随系统</Trans>}
          </span>
        </button>
      ))}
    </div>
  )
}
```

创建 `hub/internal/site/src/components/LangToggle.tsx`：

```tsx
import { useStore } from '@nanostores/react'
import { $locale, applyLocale } from '@/stores/theme'

export function LangToggle() {
  const locale = useStore($locale)
  return (
    <button
      type="button"
      onClick={() => applyLocale(locale === 'zh' ? 'en' : 'zh')}
      className="rounded px-2 py-1 text-sm hover:bg-wash"
      aria-label="language"
    >
      {locale === 'zh' ? 'EN' : '中'}
    </button>
  )
}
```

- [ ] **Step 6: 接进入口**

`hub/internal/site/src/main.tsx`：

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import './styles/tokens.css'
import { App } from './App'
import { $locale, $theme, applyLocale, applyTheme } from '@/stores/theme'

applyTheme($theme.get())
await applyLocale($locale.get())

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider i18n={i18n}>
      <App />
    </I18nProvider>
  </StrictMode>,
)
```

`hub/internal/site/src/App.tsx`（本任务先只分登录/已登录两态，布局壳下一任务补）：

```tsx
import { useStore } from '@nanostores/react'
import { $authed } from '@/stores/auth'
import { Login } from '@/pages/Login'
import { ThemeToggle } from '@/components/ThemeToggle'
import { LangToggle } from '@/components/LangToggle'

export function App() {
  const authed = useStore($authed)
  if (!authed) return <Login />
  return (
    <div className="p-8">
      <div className="flex gap-2">
        <ThemeToggle />
        <LangToggle />
      </div>
      <p className="mt-4">已登录（布局壳见下一任务）</p>
    </div>
  )
}
```

- [ ] **Step 7: 抽取词条**

```bash
cd hub/internal/site && npx lingui extract && npx lingui compile
```

打开 `src/locales/en.po`，把中文源串逐条翻成英文（M0 的词条不多，几十条）。`zh.po` 的 msgstr 留空即可（源语言）。

- [ ] **Step 8: 验收**

起 `make dev`，在浏览器里逐条确认：

- 未登录时看到登录页；输错密码显示「邮箱或密码不正确」，且不泄露后端原文。
- 用 `orciny superuser create` 建的账号能登录成功，刷新页面保持登录。
- 主题三档切换即时生效，刷新后保持；选「跟随系统」时切换 macOS 外观能跟着变。
- 语言开关切换后全部文案变化，刷新后保持。
- 用键盘 Tab 能走完登录表单并回车提交。

- [ ] **Step 9: 提交**

```bash
git add hub/internal/site
git commit -m "feat: i18n 装配、主题切换与登录页"
```

---

## Task 3: 布局壳与占位页

**Files:**
- Create: `src/components/Shell.tsx`, `src/components/Sidebar.tsx`, `src/components/Placeholder.tsx`, `src/router.tsx`
- Modify: `src/App.tsx`

**Interfaces:**
- Produces:
  ```tsx
  export function Placeholder(props: { milestone: 'M1' | 'M2' | 'M3' }): JSX.Element
  export function Shell(props: { children: React.ReactNode }): JSX.Element
  export type Route = { path: string; title: string; milestone?: 'M1' | 'M2' | 'M3' }
  ```

- [ ] **Step 1: 写占位组件**

创建 `hub/internal/site/src/components/Placeholder.tsx`：

```tsx
import { Trans } from '@lingui/react/macro'

/**
 * 未实现页面的统一空状态（spec §10.2）。
 * 各页面不要自己编空状态——统一在这里，将来批量替换也方便。
 */
export function Placeholder({ milestone }: { milestone: 'M1' | 'M2' | 'M3' }) {
  return (
    <div className="flex h-full items-center justify-center">
      <p className="rounded border border-dashed border-line px-6 py-8 text-sm text-ink3">
        <Trans>该功能将在 {milestone} 提供</Trans>
      </p>
    </div>
  )
}
```

- [ ] **Step 2: 写导航与路由表**

创建 `hub/internal/site/src/router.tsx`：

```tsx
import { atom } from 'nanostores'

/**
 * M0 不引入路由库：一共六个屏，一个 hash 就够，少一个依赖少一份升级负担。
 * M1 页面变多时再换 react-router。
 */
export type RouteKey =
  | 'overview' | 'machines' | 'configsets' | 'inbox'
  | 'usage' | 'subscriptions' | 'credentials' | 'settings'

export const $route = atom<{ key: RouteKey; param?: string }>(parseHash())

function parseHash(): { key: RouteKey; param?: string } {
  const raw = window.location.hash.replace(/^#\/?/, '')
  const [key, param] = raw.split('/')
  const known: RouteKey[] = [
    'overview', 'machines', 'configsets', 'inbox',
    'usage', 'subscriptions', 'credentials', 'settings',
  ]
  if (known.includes(key as RouteKey)) return { key: key as RouteKey, param }
  return { key: 'machines' }
}

window.addEventListener('hashchange', () => $route.set(parseHash()))

export function navigate(key: RouteKey, param?: string) {
  window.location.hash = param ? `#/${key}/${param}` : `#/${key}`
}
```

创建 `hub/internal/site/src/components/Sidebar.tsx`：

```tsx
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import {
  Boxes, CreditCard, Gauge, Inbox, KeyRound, Layers, Server, Settings as Cog,
} from 'lucide-react'
import { $route, navigate, type RouteKey } from '@/router'

const items: { key: RouteKey; icon: typeof Server; label: React.ReactNode; milestone?: string }[] = [
  { key: 'overview', icon: Gauge, label: <Trans>总览</Trans>, milestone: 'M2' },
  { key: 'machines', icon: Server, label: <Trans>机器</Trans> },
  { key: 'configsets', icon: Layers, label: <Trans>配置集</Trans>, milestone: 'M1' },
  { key: 'inbox', icon: Inbox, label: <Trans>收件箱</Trans>, milestone: 'M1' },
  { key: 'usage', icon: Boxes, label: <Trans>用量</Trans>, milestone: 'M2' },
  { key: 'subscriptions', icon: CreditCard, label: <Trans>订阅</Trans>, milestone: 'M2' },
  { key: 'credentials', icon: KeyRound, label: <Trans>凭据</Trans>, milestone: 'M1' },
  { key: 'settings', icon: Cog, label: <Trans>设置</Trans> },
]

export function Sidebar() {
  const route = useStore($route)
  return (
    <nav aria-label="primary" className="w-52 shrink-0 border-r border-line bg-surface p-3">
      <div className="mb-6 px-2 text-lg font-semibold">Orciny</div>
      <ul className="space-y-0.5">
        {items.map(({ key, icon: Icon, label, milestone }) => (
          <li key={key}>
            <button
              type="button"
              aria-current={route.key === key ? 'page' : undefined}
              onClick={() => navigate(key)}
              className={`flex w-full items-center gap-2 rounded px-2 py-1.5 text-sm ${
                route.key === key ? 'bg-accent-soft text-accent' : 'text-ink2 hover:bg-wash'
              }`}
            >
              <Icon size={16} aria-hidden />
              <span className="flex-1 text-left">{label}</span>
              {milestone && <span className="text-[10px] text-ink3">{milestone}</span>}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  )
}
```

- [ ] **Step 3: 写外壳并接进 App**

创建 `hub/internal/site/src/components/Shell.tsx`：

```tsx
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { LogOut } from 'lucide-react'
import { $authEmail, logout } from '@/stores/auth'
import { Sidebar } from './Sidebar'
import { ThemeToggle } from './ThemeToggle'
import { LangToggle } from './LangToggle'

export function Shell({ children }: { children: React.ReactNode }) {
  const email = useStore($authEmail)
  return (
    <div className="flex h-full">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-3 border-b border-line px-6 py-3">
          <div className="flex-1" />
          <ThemeToggle />
          <LangToggle />
          <span className="text-sm text-ink3">{email}</span>
          <button
            type="button"
            onClick={logout}
            className="flex items-center gap-1 rounded px-2 py-1 text-sm hover:bg-wash"
          >
            <LogOut size={14} aria-hidden />
            <Trans>退出</Trans>
          </button>
        </header>
        <main className="min-h-0 flex-1 overflow-auto p-6">{children}</main>
      </div>
    </div>
  )
}
```

改 `hub/internal/site/src/App.tsx`：

```tsx
import { useStore } from '@nanostores/react'
import { $authed } from '@/stores/auth'
import { $route } from '@/router'
import { Login } from '@/pages/Login'
import { Shell } from '@/components/Shell'
import { Placeholder } from '@/components/Placeholder'
import { Machines } from '@/pages/Machines'
import { MachineDetail } from '@/pages/MachineDetail'
import { Settings } from '@/pages/Settings'

export function App() {
  const authed = useStore($authed)
  const route = useStore($route)
  if (!authed) return <Login />

  return (
    <Shell>
      {route.key === 'machines' && (route.param ? <MachineDetail id={route.param} /> : <Machines />)}
      {route.key === 'settings' && <Settings />}
      {(route.key === 'configsets' || route.key === 'inbox' || route.key === 'credentials') && (
        <Placeholder milestone="M1" />
      )}
      {(route.key === 'overview' || route.key === 'usage' || route.key === 'subscriptions') && (
        <Placeholder milestone="M2" />
      )}
    </Shell>
  )
}
```

（`Machines` / `MachineDetail` / `Settings` 在后续任务创建；本任务先建三个只返回 `null` 的占位文件，保证能编译。）

- [ ] **Step 4: 验收**

起 `make dev`，逐条确认：

- 侧栏八项齐全，顺序为 总览/机器/配置集/收件箱/用量/订阅/凭据/设置。
- 点未实现的六项都显示统一的「该功能将在 Mx 提供」，且 M1/M2 标注正确。
- 当前项高亮，`aria-current="page"` 出现在 DOM 里。
- 顶栏显示登录邮箱，点「退出」回到登录页。
- 用键盘 Tab 能遍历侧栏并回车切换。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site
git commit -m "feat: 布局壳、侧栏导航与统一占位页"
```

---

## Task 4: 机器列表（realtime）

**Files:**
- Create: `src/stores/machines.ts`, `src/components/StatusDot.tsx`, `src/pages/Machines.tsx`

**Interfaces:**
- Produces:
  ```ts
  export const $machines: ReadableAtom<MachineRecord[]>
  export const $machinesLoading: ReadableAtom<boolean>
  export function subscribeMachines(): () => void
  export async function renameMachine(id: string, name: string): Promise<void>
  export async function deleteMachine(id: string): Promise<void>
  ```

- [ ] **Step 1: 写 store**

创建 `hub/internal/site/src/stores/machines.ts`：

```ts
import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_MACHINES, type MachineRecord } from '@/types/collections'

/**
 * 机器列表 + realtime 订阅。
 *
 * 组件不直接调 SDK（spec §10.2）：订阅统一收在这里，一处管理生命周期，
 * 也避免每个组件各自维护一份可能不一致的列表。
 */
export const $machines = atom<MachineRecord[]>([])
export const $machinesLoading = atom(true)
export const $machinesError = atom<string>('')

let unsub: (() => void) | null = null
let refCount = 0

async function load() {
  try {
    const list = await pb.collection(COLLECTION_MACHINES).getFullList<MachineRecord>({
      sort: 'name',
    })
    $machines.set(list)
    $machinesError.set('')
  } catch (e) {
    $machinesError.set(String(e))
  } finally {
    $machinesLoading.set(false)
  }
}

/** 订阅机器变化。返回退订函数；多个组件共用一份订阅。 */
export function subscribeMachines() {
  refCount += 1
  if (refCount === 1) {
    void load()
    void pb
      .collection(COLLECTION_MACHINES)
      .subscribe<MachineRecord>('*', (e) => {
        const cur = $machines.get()
        if (e.action === 'delete') {
          $machines.set(cur.filter((m) => m.id !== e.record.id))
          return
        }
        const idx = cur.findIndex((m) => m.id === e.record.id)
        if (idx === -1) $machines.set([...cur, e.record].sort(byName))
        else {
          const next = [...cur]
          next[idx] = e.record
          $machines.set(next.sort(byName))
        }
      })
      .then((fn) => {
        unsub = fn
      })
  }
  return () => {
    refCount -= 1
    if (refCount === 0 && unsub) {
      unsub()
      unsub = null
    }
  }
}

function byName(a: MachineRecord, b: MachineRecord) {
  return (a.name || a.hostname).localeCompare(b.name || b.hostname)
}

export async function renameMachine(id: string, name: string) {
  await pb.collection(COLLECTION_MACHINES).update(id, { name })
}

export async function deleteMachine(id: string) {
  // 删除语义简单，走 PB SDK；hub 侧的 record hook 负责踢掉活跃连接（spec §5.2）。
  await pb.collection(COLLECTION_MACHINES).delete(id)
}
```

- [ ] **Step 2: 写状态点**

创建 `hub/internal/site/src/components/StatusDot.tsx`：

```tsx
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
```

- [ ] **Step 3: 写列表页**

创建 `hub/internal/site/src/pages/Machines.tsx`：

```tsx
import { useEffect, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Plus, Trash2 } from 'lucide-react'
import {
  $machines, $machinesLoading, deleteMachine, renameMachine, subscribeMachines,
} from '@/stores/machines'
import { StatusDot } from '@/components/StatusDot'
import { navigate } from '@/router'
import { AddMachineDialog } from '@/components/AddMachineDialog'

export function Machines() {
  const { t } = useLingui()
  const machines = useStore($machines)
  const loading = useStore($machinesLoading)
  const [adding, setAdding] = useState(false)

  useEffect(() => subscribeMachines(), [])

  return (
    <div>
      <div className="mb-4 flex items-center">
        <h1 className="flex-1 text-lg font-semibold">
          <Trans>机器</Trans>
        </h1>
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="flex items-center gap-1 rounded bg-[var(--pri-bg)] px-3 py-1.5 text-sm text-[var(--pri-ink)]"
        >
          <Plus size={14} aria-hidden />
          <Trans>添加机器</Trans>
        </button>
      </div>

      {loading && <p className="text-sm text-ink3"><Trans>加载中…</Trans></p>}

      {!loading && machines.length === 0 && (
        <p className="rounded border border-dashed border-line p-8 text-center text-sm text-ink3">
          <Trans>还没有机器。点「添加机器」拿到一行安装命令。</Trans>
        </p>
      )}

      {machines.length > 0 && (
        <table className="w-full text-sm">
          <thead className="text-left text-ink3">
            <tr className="border-b border-line">
              <th className="py-2 font-normal"><Trans>名称</Trans></th>
              <th className="py-2 font-normal"><Trans>状态</Trans></th>
              <th className="py-2 font-normal"><Trans>系统</Trans></th>
              <th className="py-2 font-normal"><Trans>agent 版本</Trans></th>
              <th className="py-2 font-normal"><Trans>Claude Code</Trans></th>
              <th className="py-2 font-normal"><Trans>最后心跳</Trans></th>
              <th />
            </tr>
          </thead>
          <tbody>
            {machines.map((m) => (
              <tr key={m.id} className="border-b border-line/60 hover:bg-wash/50">
                <td className="py-2">
                  <input
                    defaultValue={m.name || m.hostname}
                    aria-label={t`机器名称`}
                    onBlur={(e) => {
                      const v = e.target.value.trim()
                      if (v && v !== m.name) void renameMachine(m.id, v)
                    }}
                    className="w-full rounded bg-transparent px-1 py-0.5 hover:bg-wash focus:bg-page"
                  />
                </td>
                <td className="py-2"><StatusDot status={m.status} /></td>
                <td className="py-2 font-mono text-xs text-ink2">{m.os}/{m.arch}</td>
                <td className="py-2 font-mono text-xs text-ink2">{m.agent_version}</td>
                <td className="py-2 font-mono text-xs text-ink2">
                  {m.tool_versions?.['claude-code'] ?? '—'}
                </td>
                <td className="py-2 text-xs text-ink3">
                  {/* online 时不显示秒级心跳时间：last_seen 只在状态变化时写（spec §6.5） */}
                  {m.status === 'online' ? <Trans>持续在线</Trans> : formatTime(m.last_seen)}
                </td>
                <td className="py-2 text-right">
                  <button
                    type="button"
                    onClick={() => navigate('machines', m.id)}
                    className="rounded px-2 py-1 text-xs hover:bg-wash"
                  >
                    <Trans>详情</Trans>
                  </button>
                  <button
                    type="button"
                    aria-label={t`删除机器`}
                    onClick={() => {
                      if (confirm(t`删除后该机器的 agent 会停止重试，需要重新 enroll。确定删除？`)) {
                        void deleteMachine(m.id)
                      }
                    }}
                    className="rounded p-1 text-crit hover:bg-wash"
                  >
                    <Trash2 size={14} aria-hidden />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {adding && <AddMachineDialog onClose={() => setAdding(false)} />}
    </div>
  )
}

function formatTime(v: string) {
  if (!v) return '—'
  return new Date(v).toLocaleString()
}
```

（`AddMachineDialog` 在 Task 6 创建；本任务先建一个返回 `null` 的占位文件。）

- [ ] **Step 4: 验收**

准备：起 `make dev`，用计划 4 的手工步骤接入一台本地 agent（`ORCINY_HOME=/tmp/a1 go run ./cmd/orciny-agent enroll --hub http://127.0.0.1:8090 --token <t>`，再 `... run`）。

逐条确认：

- 列表出现该机器，状态为「在线」。
- **不刷新页面**，`Ctrl-C` 掐掉 agent → 5–10 秒内状态自己变「离线」；重新 `run` → 自己变回「在线」。这是 realtime 生效的证据。
- 在名称格上改名并失焦 → 值保留；刷新页面仍在。
- 删除按钮弹确认框；确认后行消失，且 agent 日志里出现「已被删除，停止重试」。
- 空列表时显示引导文案。

- [ ] **Step 5: 提交**

```bash
git add hub/internal/site
git commit -m "feat: 机器列表、realtime 订阅与行内改名删除"
```

---

## Task 5: 机器详情与事件流

**Files:**
- Create: `src/stores/events.ts`, `src/pages/MachineDetail.tsx`

**Interfaces:**
- Produces:
  ```ts
  export function subscribeEvents(machineId: string): () => void
  export const $events: ReadableAtom<EventRecord[]>
  ```

- [ ] **Step 1: 写事件 store**

创建 `hub/internal/site/src/stores/events.ts`：

```ts
import { atom } from 'nanostores'
import { pb } from '@/lib/pb'
import { COLLECTION_EVENTS, type EventRecord } from '@/types/collections'

export const $events = atom<EventRecord[]>([])
export const $eventsLoading = atom(true)

const PAGE_SIZE = 50

/** 订阅某台机器的事件流。返回退订函数。 */
export function subscribeEvents(machineId: string) {
  let unsub: (() => void) | null = null
  $eventsLoading.set(true)

  void pb
    .collection(COLLECTION_EVENTS)
    .getList<EventRecord>(1, PAGE_SIZE, {
      filter: pb.filter('machine = {:m}', { m: machineId }),
      sort: '-created',
    })
    .then((res) => {
      $events.set(res.items)
    })
    .finally(() => $eventsLoading.set(false))

  void pb
    .collection(COLLECTION_EVENTS)
    .subscribe<EventRecord>('*', (e) => {
      if (e.action !== 'create' || e.record.machine !== machineId) return
      $events.set([e.record, ...$events.get()].slice(0, PAGE_SIZE))
    })
    .then((fn) => {
      unsub = fn
    })

  return () => {
    if (unsub) unsub()
    $events.set([])
  }
}
```

- [ ] **Step 2: 写详情页**

创建 `hub/internal/site/src/pages/MachineDetail.tsx`：

```tsx
import { useEffect } from 'react'
import { useStore } from '@nanostores/react'
import { Trans } from '@lingui/react/macro'
import { ArrowLeft } from 'lucide-react'
import { $machines, subscribeMachines } from '@/stores/machines'
import { $events, subscribeEvents } from '@/stores/events'
import { StatusDot } from '@/components/StatusDot'
import { Placeholder } from '@/components/Placeholder'
import { navigate } from '@/router'
import type { EventKind } from '@/types/collections'

const eventLabel: Record<EventKind, React.ReactNode> = {
  'machine.enrolled': <Trans>已注册</Trans>,
  'machine.re-enrolled': <Trans>重新注册</Trans>,
  'machine.connected': <Trans>已连接</Trans>,
  'machine.disconnected': <Trans>已断开</Trans>,
  'machine.removed': <Trans>已删除</Trans>,
  'token.issued': <Trans>签发注册 token</Trans>,
  'auth.failed': <Trans>认证失败</Trans>,
}

export function MachineDetail({ id }: { id: string }) {
  const machines = useStore($machines)
  const events = useStore($events)
  const machine = machines.find((m) => m.id === id)

  useEffect(() => subscribeMachines(), [])
  useEffect(() => subscribeEvents(id), [id])

  if (!machine) {
    return <p className="text-sm text-ink3"><Trans>找不到这台机器。</Trans></p>
  }

  return (
    <div className="space-y-6">
      <button
        type="button"
        onClick={() => navigate('machines')}
        className="flex items-center gap-1 text-sm text-ink2 hover:text-ink"
      >
        <ArrowLeft size={14} aria-hidden />
        <Trans>返回列表</Trans>
      </button>

      <section className="rounded-lg border border-line bg-surface p-5">
        <div className="mb-4 flex items-center gap-3">
          <h1 className="text-lg font-semibold">{machine.name || machine.hostname}</h1>
          <StatusDot status={machine.status} />
        </div>
        <dl className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm md:grid-cols-3">
          <Field label={<Trans>主机名</Trans>} value={machine.hostname} />
          <Field label={<Trans>系统</Trans>} value={`${machine.os}/${machine.arch}`} />
          <Field label={<Trans>agent 版本</Trans>} value={machine.agent_version} />
          <Field label={<Trans>Claude Code</Trans>} value={machine.tool_versions?.['claude-code'] ?? '—'} />
          <Field label={<Trans>指纹</Trans>} value={machine.fingerprint} mono />
          <Field
            label={<Trans>最后心跳</Trans>}
            value={machine.status === 'online' ? '—' : new Date(machine.last_seen).toLocaleString()}
          />
        </dl>
      </section>

      <div className="grid gap-4 md:grid-cols-3">
        <PlaceholderCard title={<Trans>配置对齐状态</Trans>} />
        <PlaceholderCard title={<Trans>漂移</Trans>} />
        <PlaceholderCard title={<Trans>机器变量</Trans>} />
      </div>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold"><Trans>事件流</Trans></h2>
        {events.length === 0 && (
          <p className="text-sm text-ink3"><Trans>暂无事件。</Trans></p>
        )}
        <ul className="space-y-1.5">
          {events.map((e) => (
            <li key={e.id} className="flex gap-3 text-sm">
              <time className="w-40 shrink-0 font-mono text-xs text-ink3">
                {new Date(e.created).toLocaleString()}
              </time>
              <span>{eventLabel[e.kind] ?? e.kind}</span>
            </li>
          ))}
        </ul>
      </section>
    </div>
  )
}

function Field({ label, value, mono }: { label: React.ReactNode; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-xs text-ink3">{label}</dt>
      <dd className={mono ? 'font-mono text-xs break-all' : ''}>{value}</dd>
    </div>
  )
}

function PlaceholderCard({ title }: { title: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <h2 className="mb-2 text-sm font-semibold">{title}</h2>
      <Placeholder milestone="M1" />
    </section>
  )
}
```

- [ ] **Step 2b: 验收**

- 从列表点「详情」进入，基本信息完整、指纹是 32 字符。
- 事件流里能看到「已注册」「已连接」。
- **不刷新页面**，掐掉 agent → 6 秒后事件流顶部出现「已断开」。
- 三块 M1 占位显示正常。
- 点「返回列表」回到列表页；浏览器后退键也能回去。

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site
git commit -m "feat: 机器详情与实时事件流"
```

---

## Task 6: 添加机器弹窗

**Files:**
- Create: `src/components/AddMachineDialog.tsx`（替换 Task 4 的占位文件）

**Interfaces:**
- Consumes: `POST /api/orciny/enroll-tokens`、`$machines`
- Produces: `export function AddMachineDialog(props: { onClose: () => void }): JSX.Element`

- [ ] **Step 1: 写弹窗**

创建 `hub/internal/site/src/components/AddMachineDialog.tsx`：

```tsx
import { useEffect, useRef, useState } from 'react'
import { useStore } from '@nanostores/react'
import { Trans, useLingui } from '@lingui/react/macro'
import { Check, Copy, X } from 'lucide-react'
import { pb } from '@/lib/pb'
import { $machines } from '@/stores/machines'

interface TokenResponse {
  token: string
  expiresAt: string
  installCommand: string
}

export function AddMachineDialog({ onClose }: { onClose: () => void }) {
  const { t } = useLingui()
  const [data, setData] = useState<TokenResponse | null>(null)
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [remaining, setRemaining] = useState(0)
  const machines = useStore($machines)
  const knownIds = useRef<Set<string>>(new Set(machines.map((m) => m.id)))
  const dialogRef = useRef<HTMLDivElement>(null)

  // 签发 token
  useEffect(() => {
    pb.send<TokenResponse>('/api/orciny/enroll-tokens', { method: 'POST' })
      .then(setData)
      .catch((e) => setError(String(e)))
  }, [])

  // 15 分钟倒计时
  useEffect(() => {
    if (!data) return
    const deadline = new Date(data.expiresAt).getTime()
    const tick = () => setRemaining(Math.max(0, Math.floor((deadline - Date.now()) / 1000)))
    tick()
    const id = setInterval(tick, 1000)
    return () => clearInterval(id)
  }, [data])

  // 新机器上线时自动关闭并高亮 —— 用户不需要猜什么时候刷新页面（spec §10.1）
  useEffect(() => {
    const fresh = machines.find((m) => !knownIds.current.has(m.id))
    if (fresh) {
      window.setTimeout(onClose, 800)
    }
  }, [machines, onClose])

  useEffect(() => {
    dialogRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  async function copy() {
    if (!data) return
    await navigator.clipboard.writeText(data.installCommand)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={t`添加机器`}
        tabIndex={-1}
        className="w-full max-w-2xl rounded-lg border border-line bg-surface p-6"
      >
        <div className="mb-4 flex items-center">
          <h2 className="flex-1 text-base font-semibold"><Trans>添加机器</Trans></h2>
          <button type="button" onClick={onClose} aria-label={t`关闭`} className="rounded p-1 hover:bg-wash">
            <X size={16} aria-hidden />
          </button>
        </div>

        {error && <p role="alert" className="text-sm text-crit">{error}</p>}
        {!data && !error && <p className="text-sm text-ink3"><Trans>正在签发注册 token…</Trans></p>}

        {data && (
          <>
            <p className="mb-3 text-sm text-ink2">
              <Trans>在目标机器上执行这行命令即可接入：</Trans>
            </p>
            <div className="mb-3 flex items-start gap-2 rounded border border-line bg-page p-3">
              <code className="min-w-0 flex-1 break-all font-mono text-xs">{data.installCommand}</code>
              <button
                type="button"
                onClick={copy}
                aria-label={t`复制安装命令`}
                className="shrink-0 rounded p-1.5 hover:bg-wash"
              >
                {copied ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
              </button>
            </div>
            <p className="text-xs text-ink3">
              <Trans>该 token 一次性使用，{formatCountdown(remaining)} 后过期。新机器上线后本窗口会自动关闭。</Trans>
            </p>
          </>
        )}
      </div>
    </div>
  )
}

function formatCountdown(sec: number) {
  const m = Math.floor(sec / 60)
  const s = sec % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
```

- [ ] **Step 2: 验收**

- 点「添加机器」弹出窗口，几百毫秒内出现安装命令，命令里含 `--hub`、`--token`、`--hub-key`。
- 倒计时从 15:00 往下走。
- 复制按钮把命令写进剪贴板（粘到终端里核对）。
- 在另一台机器（或本机另一个 `ORCINY_HOME`）执行该命令接入 → **弹窗自动关闭**，列表里出现新行。
- Esc 键能关闭弹窗；关闭按钮可用键盘触达。

- [ ] **Step 3: 提交**

```bash
git add hub/internal/site
git commit -m "feat: 添加机器弹窗与安装命令签发"
```

---

## Task 7: 设置页与生产内嵌

**Files:**
- Create: `src/pages/Settings.tsx`
- Modify: `Makefile`（`build` 依赖 `build-web`）, `.gitignore`

**Interfaces:**
- Consumes: `GET /api/orciny/hub-info`

- [ ] **Step 1: 写设置页**

创建 `hub/internal/site/src/pages/Settings.tsx`：

```tsx
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
    pb.send<HubInfo>('/api/orciny/hub-info', { method: 'GET' }).then(setInfo).catch(() => setInfo(null))
  }, [])

  return (
    <div className="max-w-3xl space-y-6">
      <h1 className="text-lg font-semibold"><Trans>设置</Trans></h1>

      <section className="rounded-lg border border-line bg-surface p-5">
        <h2 className="mb-3 text-sm font-semibold"><Trans>站点信息</Trans></h2>
        <dl className="space-y-2 text-sm">
          <div className="flex gap-4">
            <dt className="w-32 text-ink3"><Trans>hub 版本</Trans></dt>
            <dd className="font-mono">{info?.version ?? '—'}</dd>
          </div>
          <div className="flex gap-4">
            <dt className="w-32 text-ink3"><Trans>公钥指纹</Trans></dt>
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
        <h2 className="mb-3 text-sm font-semibold"><Trans>备份</Trans></h2>
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
        <h2 className="mb-3 text-sm font-semibold"><Trans>agent 更新策略</Trans></h2>
        <Placeholder milestone="M3" />
      </section>
    </div>
  )
}
```

- [ ] **Step 2: 让 build 带上前端**

改 `Makefile`：

```make
build: build-web build-hub build-agent
```

并确认 `.gitignore` 里保留了：

```gitignore
hub/internal/site/dist/*
!hub/internal/site/dist/index.html
```

- [ ] **Step 3: 生产形态验收**

```bash
make build
./dist/orciny serve --http=127.0.0.1:8090 --dir=/tmp/orciny-prod
```

浏览器访问 `http://127.0.0.1:8090`，确认：

- 看到的是真实前端（不是占位页），登录后六个屏都在。
- 停掉 Vite（此刻本来就没起）也能正常访问——证明前端确实内嵌进了二进制。
- `ls -lh dist/orciny` 小于 30MB。

清理：`rm -rf /tmp/orciny-prod`

- [ ] **Step 4: 提交**

```bash
git add hub Makefile .gitignore
git commit -m "feat: 设置页与前端内嵌构建"
```

---

## 完成检查

spec §10.1 的六个屏逐条对照：

- [ ] 登录：superuser 邮箱 + 密码
- [ ] 布局壳：八项导航、未实现页统一占位、主题切换、语言切换
- [ ] 机器列表：名称/状态/OS-arch/agent 版本/Claude Code 版本/最后心跳，realtime 自动刷新，行内改名与删除
- [ ] 机器详情：基本信息卡 + 本机事件流，三块 M1 占位
- [ ] 添加机器：`/enroll-tokens` → 可拷贝的一行安装命令 + 15 分钟倒计时 + 新机器上线自动关闭
- [ ] 设置：站点信息、hub 版本与公钥指纹、备份入口，agent 更新策略占位

另外：

- [ ] 无硬编码文案（`grep -rn '[一-龥]' src --include='*.tsx' | grep -v Trans | grep -v 't\`'` 应当只剩注释）
- [ ] 组件不直接 import `pb`（除 `stores/` 与两处显式调自定义路由的页面外）
- [ ] 深浅双主题在所有屏上都可读
- [ ] `make build` 产出的二进制内嵌了真实前端
