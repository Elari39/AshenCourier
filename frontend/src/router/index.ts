import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { setUnauthorizedHandler } from '@/api/session'
import { clearSession, useAuth } from '@/composables/useAuth'

const SITE_NAME = 'AshenCourier'
const DEFAULT_TITLE = `${SITE_NAME} · 把长链接变短`

/**
 * ⚠️ 这份清单是「单段顶级路由」的**唯一事实来源**：
 *    - `deploy/nginx/nginx.conf` 的 `location = /xxx` 必须与它**不多不少**一致；
 *    - `vite.config.ts` 的 `SPA_ROUTES` 同理（dev proxy 的 bypass 白名单）；
 *    - 后端 `internal/pkg/shortcode/reserved.go` 的保留字表是它的**超集**
 *      （额外那些是「品牌与合规页」的刻意预留，防止别人抢注 `admin` / `terms` 这类词）。
 *
 *    三者由 `backend/internal/pkg/shortcode/routes_sync_test.go` 交叉断言。
 *    约束是「不多不少」而不是「越多越好」：nginx 多写一条前端并不存在的路由，
 *    它就会被 `try_files` 兜成 **200 + NotFound 视图** —— 不存在的页面以成功码返回，
 *    掩盖真实的 404。2026-09-22 的审计据此删掉了 10 条这种占位路由。
 *
 *    新增页面时：在这里加路由 → 同步 nginx 与 vite 白名单 → 需要占住这个词就再加保留字表。
 */
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: () => import('@/layouts/DefaultLayout.vue'),
    children: [
      {
        path: '',
        name: 'landing',
        component: () => import('@/views/LandingView.vue'),
        meta: { title: '把长链接变短' },
      },
      {
        path: 'login',
        name: 'login',
        component: () => import('@/views/LoginView.vue'),
        meta: { title: '登录', guestOnly: true },
      },
      {
        path: 'register',
        name: 'register',
        component: () => import('@/views/RegisterView.vue'),
        meta: { title: '注册', guestOnly: true },
      },
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('@/views/DashboardView.vue'),
        meta: { title: '我的链接', requiresAuth: true },
      },
      {
        path: 'links/:code',
        name: 'link-detail',
        component: () => import('@/views/LinkDetailView.vue'),
        meta: { title: '链接详情' },
      },
      {
        path: ':pathMatch(.*)*',
        name: 'not-found',
        component: () => import('@/views/NotFoundView.vue'),
        meta: { title: '页面不存在' },
      },
    ],
  },
]

export const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

// 路由守卫：未登录进不去 /dashboard，已登录不必再看登录页
router.beforeEach((to) => {
  const { isAuthenticated } = useAuth()

  if (to.meta.requiresAuth && !isAuthenticated.value) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.meta.guestOnly && isAuthenticated.value) {
    return { name: 'dashboard' }
  }
  return true
})

// 标题：每个页面都能自己声明，兜底用站点默认值
router.afterEach((to) => {
  document.title = to.meta.title ? `${to.meta.title} · ${SITE_NAME}` : DEFAULT_TITLE
})

// 令牌失效（任何接口返回 401）→ 清空本地会话并送回登录页
setUnauthorizedHandler(() => {
  clearSession()

  const current = router.currentRoute.value
  if (current.name === 'login') return
  void router.push({ name: 'login', query: { redirect: current.fullPath } })
})
