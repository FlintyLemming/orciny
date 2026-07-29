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
    // 必须显式绑 127.0.0.1：Vite 默认的 'localhost' 在 macOS 上只落到 ::1，
    // 而 site.DevTarget 走的是 127.0.0.1，不钉死会反代到一个空端口（502）。
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    // 开发时前端直连 hub，避免跨域与 cookie 问题
    proxy: {
      '/api': 'http://127.0.0.1:8090',
      '/_': 'http://127.0.0.1:8090',
    },
  },
})
