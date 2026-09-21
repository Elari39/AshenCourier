<script setup lang="ts">
/**
 * 默认版式：cream 顶栏 + 页面内容 + 深色 footer。
 *
 * 顶栏 / footer 是全站唯二固定出现的区块，其余页面的「奶油 → 奶油卡片 →
 * 深色 mockup → 珊瑚 callout」节奏由各 view 自己安排。
 *
 * 这里同时承担三件**跨页面**的键盘/读屏约定（放在各 view 里做不到，因为它们是「页面之间」的事）：
 *  ① 跳到主要内容：全站第一个可聚焦元素，绕过顶栏导航；
 *  ② aria-current：告诉读屏「你现在在哪一页」；
 *  ③ 路由切换后把焦点移到新页面的 h1 —— SPA 的客户端跳转不刷新页面，
 *     不主动移动焦点的话，读屏停留在导航链接上、不会念出新页面是什么。
 */
import { computed, nextTick, ref, useId, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import IconButton from '@/components/ui/IconButton.vue'
import ToastHost from '@/components/ui/ToastHost.vue'
import { useAuth } from '@/composables/useAuth'
import { useScrollLock } from '@/composables/useScrollLock'
import { useToast } from '@/composables/useToast'

const route = useRoute()
const router = useRouter()
const { isAuthenticated, displayName, logout } = useAuth()
const toast = useToast()
const { lock, unlock } = useScrollLock()

const menuOpen = ref(false)
/** 菜单面板的 id：给汉堡按钮的 aria-controls 一个目标。 */
const menuId = useId()
/** 汉堡按钮：菜单被 Esc 关掉时，焦点要还给它。 */
const menuButton = ref<InstanceType<typeof IconButton> | null>(null)

/** 首页与「我的链接」的高亮/aria-current 判定。详情页属于「我的链接」这一块。 */
const onLanding = computed(() => route.name === 'landing')
const inLinksSection = computed(() => route.name === 'dashboard' || route.name === 'link-detail')

/** 当前页的导航语义值：aria-current 只在「就是这一页」时才给。 */
function currentFor(active: boolean): 'page' | undefined {
  return active ? 'page' : undefined
}

// 路由变化时收起移动端菜单，否则点完链接菜单还盖着页面
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false
  },
)

/**
 * 客户端跳转到**另一个页面**时，把焦点移到新页面的标题上。
 *
 * 这里盯的是 `route.path` 而不是 `fullPath`：只改 hash 或只改 query 不算「换了页面」，
 * 不该抢焦点。最典型的反例就是「跳到主要内容」—— 它只改 hash，
 * 用户刚把焦点放到主区域，再被顶回标题就南辕北辙了。
 *
 * watcher 不带 immediate，所以首次加载不会触发：那时用户可能正在地址栏里，
 * 抢焦点只会碍事，而原生页面加载本来就会把页面标题念出来。
 */
watch(
  () => route.path,
  async () => {
    await nextTick()
    await focusPageHeading()
  },
)

/** 等一帧。用 setTimeout 而不是 requestAnimationFrame：无头 Chrome 下 rAF 的节流不确定。 */
function waitFrame(): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, 30)
  })
}

/**
 * 把焦点移到当前页面的 h1 上。
 *
 * 两件容易漏的事：
 *  ① h1 默认不可聚焦，得靠 `tabindex="-1"`。PageHeader 里已经写了，这里再兜一次 ——
 *     「漏写」的表现是完全静默的（焦点没动，页面看着一切正常），兜这一下只要两行。
 *  ② 视图是**懒加载**的：路由确认之后 chunk 还要一会儿才挂上，一次 nextTick 常常
 *     还看不见 h1。所以要退让重试，而不是「试一次、没找到就算了」——
 *     后者在本地快机器上大概率能成，到了 CI 就变成偶发不生效。
 */
async function focusPageHeading(retries = 20): Promise<void> {
  const heading = document.querySelector<HTMLElement>('#main-content h1')
  if (heading) {
    if (!heading.hasAttribute('tabindex')) heading.setAttribute('tabindex', '-1')
    // preventScroll：滚动交给路由的 scrollBehavior，这里再滚一次会打架
    heading.focus({ preventScroll: true })
    return
  }
  if (retries <= 0) return
  await waitFrame()
  return focusPageHeading(retries - 1)
}

// 移动端菜单：开着的时候锁滚动（它是一整屏面板），Esc 关掉并把焦点还给汉堡按钮
watch(menuOpen, (open) => {
  if (open) {
    lock()
    document.addEventListener('keydown', onMenuKeydown, true)
    return
  }
  unlock()
  document.removeEventListener('keydown', onMenuKeydown, true)
})

function onMenuKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  menuOpen.value = false
  const el = menuButton.value?.$el as HTMLElement | undefined
  el?.focus({ preventScroll: true })
}

function handleLogout(): void {
  logout()
  toast.info('已退出登录')
  void router.push({ name: 'landing' })
}
</script>

<template>
  <div class="flex min-h-screen flex-col bg-canvas">
    <!-- 键盘用户的第一个落点：跳过顶栏那一串导航 -->
    <a class="skip-link" href="#main-content">跳到主要内容</a>

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
        <nav class="hidden items-center gap-1 md:flex" aria-label="主导航">
          <RouterLink
            :to="{ name: 'landing' }"
            class="rounded-md px-3.5 py-2 text-[14px] font-medium text-ink"
            :class="onLanding ? 'bg-surface-card' : ''"
            :aria-current="currentFor(onLanding)"
          >
            首页
          </RouterLink>
          <RouterLink
            :to="{ name: 'dashboard' }"
            class="rounded-md px-3.5 py-2 text-[14px] font-medium text-ink"
            :class="inLinksSection ? 'bg-surface-card' : ''"
            :aria-current="currentFor(inLinksSection)"
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
            ref="menuButton"
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

    <!-- 移动端菜单：整屏奶油面板。
         ⚠️ 定位只给 `inset-x-0 top-16 bottom-0`，不要再给 `inset-16` ——
         `inset-16` 会把四个方向都设成 64px（top 又被 top-16 覆盖），
         于是面板缩成四周各留 64px 的一个小方块：既盖不住页面，
         露出来的那一圈还能点到后面的内容。 -->
    <div v-if="menuOpen" :id="menuId" class="fixed inset-x-0 top-16 bottom-0 z-30 bg-canvas md:hidden">
      <nav class="container-page flex flex-col gap-1 py-6" aria-label="移动端导航">
        <RouterLink
          :to="{ name: 'landing' }"
          class="rounded-md px-3 py-3 text-[16px] text-ink"
          :aria-current="currentFor(onLanding)"
        >
          首页
        </RouterLink>
        <RouterLink
          :to="{ name: 'dashboard' }"
          class="rounded-md px-3 py-3 text-[16px] text-ink"
          :aria-current="currentFor(inLinksSection)"
        >
          我的链接
        </RouterLink>
      </nav>
    </div>

    <!-- ---------- 页面内容 ----------
         id 是跳转链接的落点；tabindex="-1" 让它可以被脚本/片段链接聚焦
         （确认框关闭时如果触发它的元素已经不在文档里，焦点也会退到这里）。 -->
    <main id="main-content" class="flex-1" tabindex="-1">
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
