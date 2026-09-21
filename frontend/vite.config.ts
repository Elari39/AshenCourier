import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
// 用 vitest 的 defineConfig：它在 Vite 配置上多一个 `test` 字段，
// 于是别名（@ → src）只有一处定义，测试与构建不会各配一份。
import { defineConfig } from 'vitest/config'

/**
 * 前端顶级路由白名单。
 *
 * 短码代理正则会命中 /dashboard、/login 这类 SPA 路由，
 * 因此必须显式让它们绕过代理 —— 否则开发时刷新页面会打到后端拿 404。
 *
 * ⚠️ 这一份必须与 `deploy/nginx/nginx.conf` 的 `location = /xxx` 清单**完全一致**
 *    （后端 `internal/pkg/shortcode/reserved.go` 的保留字表则是它的超集，可以多不能少）。
 *    这个不变量现在有自动化守卫：`backend/internal/pkg/shortcode/routes_sync_test.go`
 *    会同时读这三个文件做交叉比对，漏改哪一处都会让 `go test ./...` 红 ——
 *    原来只靠注释提醒，结果 terms / privacy 就在这里漂移过（nginx 与保留字表都有、
 *    只有这一份没有，表现为本地 `pnpm dev` 下刷新 /terms 被当成短码打到后端 404）。
 */
const SPA_ROUTES =
  /^\/(login|register|logout|dashboard|links|settings|account|profile|admin|about|help|docs|terms|privacy)(\/|$)/

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

  test: {
    // 只跑纯函数单测，环境用 node：不引 jsdom / @vue/test-utils。
    // 组件的正确性（渲染、交互、版式）交给 e2e/ 下那套真浏览器脚本断言 ——
    // 「图能解码、接口 200」不等于「页面对不对」，见 README 七条踩过的坑第 6 条。
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
