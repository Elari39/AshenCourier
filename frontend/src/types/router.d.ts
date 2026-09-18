import 'vue-router'

declare module 'vue-router' {
  interface RouteMeta {
    /** 浏览器标题（会拼上站点名后缀）。 */
    title?: string
    /** 需要登录；未登录时重定向到 /login。 */
    requiresAuth?: boolean
    /** 只允许未登录访问；已登录时重定向到 /dashboard。 */
    guestOnly?: boolean
  }
}
