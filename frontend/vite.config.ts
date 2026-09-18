import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

/**
 * 前端顶级路由白名单。
 *
 * 短码代理正则会命中 /dashboard、/login 这类 SPA 路由，
 * 因此必须显式让它们绕过代理 —— 否则开发时刷新页面会打到后端拿 404。
 *
 * ⚠️ 这里必须与后端 internal/pkg/shortcode/reserved.go 的「前端 SPA 顶级路由」
 *    部分保持同步；后端有单测断言那份清单，改路由时两处一起改。
 */
const SPA_ROUTES = /^\/(login|register|logout|dashboard|links|settings|account|profile|admin|about|help|docs)(\/|$)/

export default defineConfig({
  plugins: [vue(), tailwindcss()],

  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },

  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
      // 短码跳转：与 nginx 的 location 正则保持同一条
      '^/[A-Za-z0-9_-]{3,32}$': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        // bypass 返回字符串 = 不代理，交给 Vite 自己按该 URL 处理
        bypass: (req) => (SPA_ROUTES.test(req.url ?? '') ? (req.url ?? '/') : undefined),
      },
    },
  },

  build: {
    outDir: 'dist',
    sourcemap: false,
    // 手写 SVG 图表 + 少量页面，产物很小；设个阈值提醒别无意间引重依赖
    chunkSizeWarningLimit: 600,
  },
})
