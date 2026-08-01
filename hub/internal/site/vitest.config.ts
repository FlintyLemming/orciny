import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// 测试需要 lingui macro 转译（Trans / t 标签），但不需要 tailwind。
export default defineConfig({
  plugins: [
    react({ babel: { plugins: ['@lingui/babel-plugin-lingui-macro'] } }),
  ],
  resolve: { alias: { '@': path.resolve(__dirname, 'src') } },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
