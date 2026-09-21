<script setup lang="ts">
/**
 * 默认版式：cream 顶栏 + 页面内容 + 深色 footer。
 *
 * 顶栏 / footer 是全站唯二固定出现的区块，其余页面的「奶油 → 奶油卡片 →
 * 深色 mockup → 珊瑚 callout」节奏由各 view 自己安排。
 */
import { ref, useId, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import IconButton from '@/components/ui/IconButton.vue'
import ToastHost from '@/components/ui/ToastHost.vue'
import { useAuth } from '@/composables/useAuth'
import { useToast } from '@/composables/useToast'

const route = useRoute()
const router = useRouter()
const { isAuthenticated, displayName, logout } = useAuth()
const toast = useToast()

const menuOpen = ref(false)
/** 菜单面板的 id：给汉堡按钮的 aria-controls 一个目标。 */
const menuId = useId()

// 路由变化时收起移动端菜单，否则点完链接菜单还盖着页面
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)

function handleLogout(): void {
  logout()
  toast.info('已退出登录')
  void router.push({ name: 'landing' })
}
</script>

<template>
  <div class="flex min-h-screen flex-col bg-canvas">
    <!-- ---------- 顶栏：64px 奶油固定条 ---------- -->
    <header class="sticky top-0 z-40 h-16 border-b border-hairline bg-canvas">
      <div class="container-page flex h-full items-center gap-6">
        <RouterLink :to="{ name: 'landing' }" class="flex items-center gap-2.5">
          <!-- 品牌标记 + 字标。标记永远是深色，不反相 -->
          <svg class="h-5 w-5" viewBox="0 0 32 32" aria-hidden="true">
            <path d="M16 4l2.1 9.9L28 16l-9.9 2.1L16 28l-2.1-9.9L4 16l9.9-2.1z" fill="#141413" />
          </svg>
          <span class="text-[15px] font-medium tracking-tight text-ink">AshenCourier</span>
        </RouterLink>

        <!-- 桌面端菜单 -->
        <nav class="hidden items-center gap-1 md:flex">
          <RouterLink
            :to="{ name: 'landing' }"
            class="rounded-md px-3.5 py-2 text-[14px] font-medium text-ink"
            :class="route.name === 'landing' ? 'bg-surface-card' : ''"
          >
            首页
          </RouterLink>
          <RouterLink
            :to="{ name: 'dashboard' }"
            class="rounded-md px-3.5 py-2 text-[14px] font-medium text-ink"
            :class="route.name === 'dashboard' ? 'bg-surface-card' : ''"
          >
            我的链接
          </RouterLink>
        </nav>

        <div class="ml-auto flex items-center gap-3">
          <template v-if="isAuthenticated">
            <span class="hidden text-[14px] text-muted sm:inline">{{ displayName }}</span>
            <button type="button" class="btn btn-text" @click="handleLogout">退出</button>
          </template>
          <template v-else>
            <RouterLink :to="{ name: 'login' }" class="btn btn-text">登录</RouterLink>
            <RouterLink :to="{ name: 'register' }" class="btn btn-primary">免费开始</RouterLink>
          </template>

          <!-- 移动端汉堡。label 随开合状态变：读屏听到的是「关闭菜单」而不是
               「打开菜单」—— 同一个按钮在两种状态下的下一步动作是相反的。 -->
          <IconButton
            class="md:hidden"
            :label="menuOpen ? '关闭菜单' : '打开菜单'"
            :aria-expanded="menuOpen"
            :aria-controls="menuId"
            @click="menuOpen = !menuOpen"
          >
            <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                :d="menuOpen ? 'M3 3l10 10M13 3L3 13' : 'M2 5h12M2 11h12'"
                stroke="currentColor"
                stroke-width="1.5"
                stroke-linecap="round"
              />
            </svg>
          </IconButton>
        </div>
      </div>
    </header>

    <!-- 移动端菜单：整屏奶油面板 -->
    <div v-if="menuOpen" :id="menuId" class="fixed inset-16 top-16 z-30 bg-canvas md:hidden">
      <nav class="container-page flex flex-col gap-1 py-6">
        <RouterLink :to="{ name: 'landing' }" class="rounded-md px-3 py-3 text-[16px] text-ink">
          首页
        </RouterLink>
        <RouterLink :to="{ name: 'dashboard' }" class="rounded-md px-3 py-3 text-[16px] text-ink">
          我的链接
        </RouterLink>
      </nav>
    </div>

    <!-- ---------- 页面内容 ----------
         tabindex="-1" 不是给鼠标用的：确认框关闭时如果触发它的元素已经不在文档里
         （例如「删除成功 → 那一行没了」），焦点要有个地方可去 —— 落到这里总好过
         掉回 <body> 让键盘用户从页首重新 Tab 一遍。 -->
    <main class="flex-1" tabindex="-1">
      <RouterView />
    </main>

    <!-- ---------- 深色 footer：永不反相 ---------- -->
    <footer class="bg-surface-dark text-on-dark-soft">
      <div class="container-page grid gap-10 py-16 md:grid-cols-4">
        <div class="md:col-span-2">
          <div class="flex items-center gap-2.5">
            <svg class="h-5 w-5" viewBox="0 0 32 32" aria-hidden="true">
              <path d="M16 4l2.1 9.9L28 16l-9.9 2.1L16 28l-2.1-9.9L4 16l9.9-2.1z" fill="#faf9f5" />
            </svg>
            <span class="text-[15px] font-medium text-on-dark">AshenCourier</span>
          </div>
          <p class="mt-4 max-w-sm text-[14px] leading-[1.55]">
            一个克制的短链服务：匿名即可用，登录后可集中管理并查看点击统计。
          </p>
        </div>

        <div>
          <p class="eyebrow text-on-dark-soft">产品</p>
          <ul class="mt-4 space-y-2.5 text-[14px]">
            <li><RouterLink :to="{ name: 'landing' }" class="text-on-dark">首页</RouterLink></li>
            <li><RouterLink :to="{ name: 'dashboard' }" class="text-on-dark">我的链接</RouterLink></li>
            <li><RouterLink :to="{ name: 'register' }" class="text-on-dark">注册</RouterLink></li>
          </ul>
        </div>

        <div>
          <p class="eyebrow text-on-dark-soft">技术</p>
          <ul class="mt-4 space-y-2.5 text-[14px]">
            <li class="font-mono text-[13px]">Go 1.27 · net/http</li>
            <li class="font-mono text-[13px]">PostgreSQL 18 · Redis 8</li>
            <li class="font-mono text-[13px]">Vue 3 · Tailwind v4</li>
          </ul>
        </div>
      </div>

      <div class="border-t border-surface-dark-elevated">
        <div class="container-page py-6 text-[13px]">
          © {{ new Date().getFullYear() }} AshenCourier · 仅允许 http/https 目标地址
        </div>
      </div>
    </footer>

    <ToastHost />
    <ConfirmDialog />
  </div>
</template>
