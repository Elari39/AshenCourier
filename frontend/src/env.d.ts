/// <reference types="vite/client" />

/**
 * 环境变量类型声明。
 * 只有 VITE_ 前缀的变量会被打进前端产物 —— 绝不要把密钥放进来。
 */
interface ImportMetaEnv {
  /** API 基址，默认 `/api`（同源反代）。 */
  readonly VITE_API_BASE_URL?: string

  /**
   * 短链基址，例：`https://s.ashen.dev`。**只在落地页演示区用到** —— 那是用户
   * 还没创建任何链接、手上没有后端响应可展示的时刻；未设置时回退到
   * `window.location.origin`（同源部署下它就是短链域名）。
   *
   * 它和此前删掉的 `VITE_PUBLIC_BASE_URL` 不是一回事：那个是用来**拼** short_url
   * 前缀的，而前缀由后端按 PUBLIC_BASE_URL 与链接所属域名拼好下发 —— 前端自己拼
   * 既拼不对（自定义域名）也没必要，所以删了。这个只回答「没有任何链接可展示时
   * 那段 mockup 该显示什么域名」，确实没有别的来源。
   */
  readonly VITE_SHORT_BASE_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, unknown>, Record<string, unknown>, unknown>
  export default component
}
