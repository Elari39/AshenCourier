/// <reference types="vite/client" />

/**
 * 环境变量类型声明。
 * 只有 VITE_ 前缀的变量会被打进前端产物 —— 绝不要把密钥放进来。
 */
interface ImportMetaEnv {
  /** API 基址，默认 `/api`（同源反代）。 */
  readonly VITE_API_BASE_URL?: string
  /** 站点对外基址，用于展示短链。留空时用 window.location.origin。 */
  readonly VITE_PUBLIC_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}
