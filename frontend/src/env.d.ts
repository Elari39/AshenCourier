/// <reference types="vite/client" />

/**
 * 环境变量类型声明。
 * 只有 VITE_ 前缀的变量会被打进前端产物 —— 绝不要把密钥放进来。
 */
interface ImportMetaEnv {
  /**
   * API 基址，默认 `/api`（同源反代）。
   *
   * 刻意**不再**声明 `VITE_PUBLIC_BASE_URL`：短链前缀由后端按 `PUBLIC_BASE_URL`
   * 与链接所属域名拼进 `short_url` 返回，前端从不自己拼 —— 声明一个没人读的变量，
   * 只会让人以为「改这个就能换前缀」，而实际改了不起作用。
   */
  readonly VITE_API_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}
