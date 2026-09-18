import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import { setUnauthorizedHandler } from '@/api/session'
import { clearSession, useAuth } from '@/composables/useAuth'

const SITE_NAME = 'AshenCourier'
const DEFAULT_TITLE = `${SITE_NAME} · 把长链接变短`

/**
 * ⚠️ 顶级路径清单必须与后端 `internal/pkg/shortcode/reserved.go` 的
 *    「前端 SPA 顶级路由」一致（那边有单测断言）。新增页面时两处一起改，
 *    否则新路由会被短码跳转"吃掉"。
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
