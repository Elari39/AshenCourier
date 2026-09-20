/**
 * 短链基址（已剥掉结尾斜杠）。
 *
 * 只服务于「手上还没有任何链接」的静态演示 —— 落地页 mockup 要展示短链长什么样，
 * 而此时没有后端响应可用。**有真实链接时一律用后端下发的 `short_url`**：它可能
 * 带自定义域名（domains 表 + PUBLIC_BASE_URL 拼出来的），前端自己拼不出来。
 *
 * 取值优先级：
 *   1. `VITE_SHORT_BASE_URL`（构建期注入）—— 短链域名与控制台域名分离时配它
 *   2. `window.location.origin`           —— 同源部署（默认形态）下它就是短链域名
 *   3. 两者都没有（Node / 单测环境）返回 '' —— 调用方拼出的 `/{code}` 仍是
 *      正确的同源相对路径，不会渲染成 "undefined/7Kd2pQ"
 *
 * ⚠️ 构建期替换：改这个值必须重新 build 镜像。给已构建好的 frontend 容器加
 *    `environment: VITE_SHORT_BASE_URL=...` 是无效的 —— 值已经烤进 JS 里了。
 */
export function shortBase(): string {
  const configured = import.meta.env.VITE_SHORT_BASE_URL?.trim()
  if (configured) return configured.replace(/\/+$/, '')

  const origin = typeof window === 'undefined' ? '' : window.location.origin
  return origin.replace(/\/+$/, '')
}
