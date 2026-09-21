<script setup lang="ts">
/**
 * 链接详情 + 统计。
 *
 * 鉴权：登录用户用自己的账号，匿名创建者用 localStorage 里的 manage_key。
 * 后端对「无权限」和「不存在」都回 404，所以这里只需要处理一种失败态。
 */
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import QRCode from 'qrcode'

import { ApiError, linksApi } from '@/api/client'
import type { ClickEvent, Link, Stats } from '@/api/types'
import DistributionList from '@/components/DistributionList.vue'
import StatCard from '@/components/StatCard.vue'
import TrendChart from '@/components/TrendChart.vue'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
import Section from '@/components/ui/Section.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useAuth } from '@/composables/useAuth'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'
import type { DistributionItem } from '@/types/ui'
import {
  describeClient,
  describeDevice,
  describeExpiry,
  describeReferer,
  describeStatus,
  formatDateTime,
  formatDateTimeSeconds,
  formatNumber,
} from '@/utils/format'
import { splitTags } from '@/utils/tags'
import { createRequestGuard, isAbortError } from '@/utils/request'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const { confirm } = useConfirm()
const { isAuthenticated, manageKeyFor, forgetManageKey } = useAuth()

/** 路由参数可能是 string | string[]，这里收敛成 string。 */
const code = computed(() => {
  const raw = route.params.code
  return Array.isArray(raw) ? (raw[0] ?? '') : (raw ?? '')
})

const link = ref<Link | null>(null)
const stats = ref<Stats | null>(null)
const loading = ref(true)
const notFound = ref(false)
const loadError = ref('')

const statsDays = ref(30)
const loadingStats = ref(false)

/** 统计窗口。值是数字 —— 直接喂给接口的 days 参数，label 才带单位。 */
const dayOptions = [
  { value: 7, label: '7 天' },
  { value: 30, label: '30 天' },
  { value: 90, label: '90 天' },
]

// 点击明细（keyset 分页：游标为空 = 已到底）
const clicks = ref<ClickEvent[]>([])
const clicksCursor = ref('')
const loadingClicks = ref(false)
const clicksError = ref('')
/** 每页条数；与后端默认页大小一致，翻页只追加不替换。 */
const CLICK_PAGE_SIZE = 20

// 二维码配色：深墨前景 + 暖奶油底（≈19:1 对比度）。
// 刻意不用珊瑚色（--color-primary）：它是强调色，与奶油底的对比度不足以让
// 扫码器在弱光/贴纸场景下稳定识别 —— 二维码只有「能扫出来」这一个功能。
const QR_DARK = '#141413' // --color-ink
const QR_LIGHT = '#faf9f5' // --color-canvas

const qrCanvas = ref<HTMLCanvasElement | null>(null)
const downloadingQR = ref(false)
const qrError = ref('')

// 编辑表单
const editOpen = ref(false)
const editTitle = ref('')
const editTarget = ref('')
const editTags = ref('')
const editStatus = ref<'active' | 'disabled'>('active')
/** 只暴露两种可编辑状态：status=3（已删除）是不可逆的，编辑面板里不给它入口。 */
const statusOptions = [
  { value: 'active', label: '正常' },
  { value: 'disabled', label: '停用（跳转返回 410）' },
]
/**
 * 新口令输入。刻意**不回填**现有口令：后端只存 bcrypt 摘要，回填等于把摘要
 * 送给前端；留空即「不改口令」。
 */
const editPassword = ref('')
const saving = ref(false)
const editError = ref('')

const deleting = ref(false)
const claiming = ref(false)

/** 当前短码对应的匿名管理密钥（登录用户可能是空）。 */
const manageKey = computed(() => manageKeyFor(code.value) ?? null)

/** 统计卡片：窗口内的点击数由 daily 求和得到，口径与后端一致。 */
const windowClicks = computed(() => stats.value?.window_clicks ?? 0)
const dailyAverage = computed(() => {
  const days = stats.value?.days ?? 0
  return days > 0 ? Math.round(windowClicks.value / days) : 0
})
const peakDay = computed(() => {
  const points = stats.value?.daily ?? []
  return points.reduce(
    (best, point) => (point.clicks > best.clicks ? point : best),
    { date: '', clicks: 0 },
  )
})

const refererItems = computed<DistributionItem[]>(() =>
  (stats.value?.top_referers ?? []).map((item) => ({
    label: describeReferer(item.referer),
    value: item.clicks,
  })),
)
const deviceItems = computed<DistributionItem[]>(() =>
  (stats.value?.devices ?? []).map((item) => ({
    label: describeDevice(item.device),
    value: item.clicks,
  })),
)
const browserItems = computed<DistributionItem[]>(() =>
  (stats.value?.browsers ?? []).map((item) => ({ label: item.browser, value: item.clicks })),
)

/**
 * 国家分布（M5-2）。接口只回**已知国家**，所以「没配 GeoIP 库文件」= 空数组 = 整块隐藏。
 *
 * 直接显示 ISO 3166-1 alpha-2 代码（CN / US），刻意不引一张「代码 → 中文国名」的表：
 * 那表要 250 项才完整，而没覆盖到的国家会退化成显示代码 —— 也就是「一半中文一半代码」，
 * 反而比全用代码更难读。真要中文国名，正确的做法是把它放在后端（与数据同源）。
 */
const countryItems = computed<DistributionItem[]>(() =>
  (stats.value?.countries ?? []).map((item) => ({ label: item.country, value: item.clicks })),
)

/** 是否是「匿名创建且我有密钥」——只有这种状态才提示可认领。 */
const canClaim = computed(
  () => isAuthenticated.value && link.value?.anonymous === true && manageKey.value !== null,
)

/**
 * 详情主体的并发守卫。与下面两个分开：link 决定整页骨架（404 / 编辑表单 / 二维码），
 * 它一旦错位，「保存 / 删除 / 认领」就会打到错的短码上，所以它必须能取消在飞的那一轮。
 */
const linkGuard = createRequestGuard()

async function loadLink(): Promise<void> {
  const { signal, isStale } = linkGuard.begin()
  loading.value = true
  notFound.value = false
  loadError.value = ''

  try {
    const data = await linksApi.get(code.value, manageKey.value, signal)
    if (isStale()) return
    link.value = data
    editTitle.value = data.title ?? ''
    editTarget.value = data.target_url
    editTags.value = (data.tags ?? []).join(', ')
    editStatus.value = data.status === 'disabled' ? 'disabled' : 'active'
    editPassword.value = ''
  } catch (cause) {
    if (isStale() || isAbortError(cause)) return
    if (cause instanceof ApiError && cause.status === 404) {
      notFound.value = true
    } else {
      loadError.value = cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试'
    }
  } finally {
    if (!isStale()) loading.value = false
  }
}

/**
 * 统计与明细各用一个并发守卫（而不是共用一个）：切换统计窗口时两者会同时重发，
 * 但它们彼此独立 —— 明细分页失败不该把统计面板也作废，反之亦然。
 *
 * 没有它们时的症状：连点「7 天 → 90 天」，先回来的 7 天响应会写进标着「90 天」
 * 的面板；「加载更多」在途时切窗口，旧的一页会被追加进已经重置的列表。
 */
const statsGuard = createRequestGuard()
const clicksGuard = createRequestGuard()

async function loadStats(): Promise<void> {
  if (!link.value) return

  const { signal, isStale } = statsGuard.begin()
  loadingStats.value = true
  try {
    const page = await linksApi.stats(code.value, statsDays.value, manageKey.value, signal)
    if (isStale()) return
    stats.value = page
  } catch (cause) {
    // 取消是我们自己发起的（切窗口 / 离开页面），不是故障，不提示
    if (isStale() || isAbortError(cause)) return
    toast.error(cause instanceof ApiError ? cause.friendly : '统计加载失败')
  } finally {
    // 过期的一轮不能掐掉新请求的 loading
    if (!isStale()) loadingStats.value = false
  }
}

async function loadClicks(reset = true): Promise<void> {
  if (!link.value) return

  const { signal, isStale } = clicksGuard.begin()
  loadingClicks.value = true
  clicksError.value = ''
  try {
    const page = await linksApi.clicks(
      code.value,
      {
        limit: CLICK_PAGE_SIZE,
        days: statsDays.value,
        cursor: reset ? undefined : clicksCursor.value,
      },
      manageKey.value,
      signal,
    )
    if (isStale()) return
    clicks.value = reset ? page.clicks : [...clicks.value, ...page.clicks]
    clicksCursor.value = page.next_cursor ?? ''
  } catch (cause) {
    if (isStale() || isAbortError(cause)) return
    // 明细加载失败不影响页面其余部分：单独报错，统计与趋势照常显示
    clicksError.value = cause instanceof ApiError ? cause.friendly : '点击明细加载失败'
  } finally {
    if (!isStale()) loadingClicks.value = false
  }
}

async function renderQR(): Promise<void> {
  // canvas 在 v-else-if="link" 里，要等这次 link 赋值渲染完才存在
  await nextTick()
  const canvas = qrCanvas.value
  if (!canvas || !link.value) return

  qrError.value = ''
  try {
    // 320px 画到 160px 的显示尺寸上：高 DPI 屏与截图放大都不糊
    await QRCode.toCanvas(canvas, link.value.short_url, {
      width: 320,
      margin: 1,
      errorCorrectionLevel: 'M',
      color: { dark: QR_DARK, light: QR_LIGHT },
    })

    // ⚠️ 必须清掉 qrcode 写进来的行内尺寸。
    // 它的 canvas 渲染器为了「像素对齐」会写 canvas.style.width/height = '320px'
    // （见 qrcode/lib/renderer/canvas.js 的 clearCanvas），而行内样式**优先级高于
    // 类选择器** —— 留着的话 Tailwind 的 h-full w-full 完全不生效，画布会以 320px
    // 撑破 160px 的容器，压住右边的文字和下方的卡片。
    // 画布的 width/height 属性（位图分辨率）保持不变，显示尺寸交给 CSS 类。
    canvas.style.width = ''
    canvas.style.height = ''
  } catch {
    qrError.value = '二维码生成失败'
  }
}

/** 下载 1024×1024 的 PNG。 */
async function downloadQR(): Promise<void> {
  if (!link.value) return

  downloadingQR.value = true
  try {
    // 下载件用纯白底而不是屏幕上的暖奶油底：它多半会被打印、复印或贴到别处，
    // 白底在那些场景下的对比度更稳（屏幕上的奶油底只是为了和 DESIGN.md 的面板一致）。
    const dataURL = await QRCode.toDataURL(link.value.short_url, {
      width: 1024,
      margin: 2,
      errorCorrectionLevel: 'M',
      color: { dark: QR_DARK, light: '#ffffff' },
    })

    const anchor = document.createElement('a')
    anchor.href = dataURL
    anchor.download = `ashencourier-${link.value.short_code}.png`
    anchor.click()
    toast.success('二维码已下载（1024×1024 PNG）')
  } catch {
    toast.error('二维码生成失败，请稍后重试')
  } finally {
    downloadingQR.value = false
  }
}

async function saveEdit(): Promise<void> {
  if (!link.value) return

  saving.value = true
  editError.value = ''
  try {
    const updated = await linksApi.update(
      code.value,
      {
        title: editTitle.value.trim(),
        target_url: editTarget.value.trim(),
        tags: splitTags(editTags.value),
        status: editStatus.value,
        // 只在填了的时候发：空串是 422（清除口令要走 clear_password）
        ...(editPassword.value ? { password: editPassword.value } : {}),
      },
      manageKey.value,
    )
    link.value = updated
    editPassword.value = ''
    editOpen.value = false
    toast.success('已保存')
  } catch (cause) {
    editError.value = cause instanceof ApiError ? cause.friendly : '保存失败，请稍后重试'
  } finally {
    saving.value = false
  }
}

async function clearExpiry(): Promise<void> {
  if (!link.value) return
  try {
    link.value = await linksApi.update(code.value, { clear_expires: true }, manageKey.value)
    toast.success('已改为永久有效')
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '操作失败')
  }
}

/** 清除访问口令（与 clearExpiry 同一套路：单独一个动作，不等保存）。 */
async function clearPassword(): Promise<void> {
  if (!link.value) return
  try {
    link.value = await linksApi.update(code.value, { clear_password: true }, manageKey.value)
    editPassword.value = ''
    toast.success('已清除访问口令')
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '操作失败')
  }
}

async function handleDelete(): Promise<void> {
  if (!link.value) return
  const ok = await confirm({
    title: '删除短链',
    message: `确定要删除 /${code.value} 吗？删除后短链立即失效，这个短码也不会再复用。`,
    confirmText: '删除',
    variant: 'danger',
  })
  if (!ok) return

  deleting.value = true
  try {
    await linksApi.remove(code.value, manageKey.value)
    toast.success('已删除')
    await router.push({ name: 'dashboard' })
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '删除失败，请稍后重试')
  } finally {
    deleting.value = false
  }
}

async function handleClaim(): Promise<void> {
  const key = manageKey.value
  if (!key) return

  claiming.value = true
  try {
    link.value = await linksApi.claim(code.value, key)
    // 认领成功后后端会清空 key_hash，本地密钥就没用了
    forgetManageKey(code.value)
    toast.success('已认领到你的账号下')
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '认领失败，请稍后重试')
  } finally {
    claiming.value = false
  }
}

// 切换统计窗口时重拉统计与明细：两者共用同一个窗口，口径必须一致
watch(statsDays, () => {
  void loadStats()
  void loadClicks()
})

/** 加载整页：先拿 link（它决定渲染哪一支分支），再并行拉统计、明细与二维码。 */
async function loadAll(): Promise<void> {
  await loadLink()
  if (link.value) {
    await Promise.all([loadStats(), loadClicks(), renderQR()])
  }
}

/**
 * 路由参数变化（/links/A → /links/B）时把上一条短链的痕迹清干净。
 *
 * `statsDays` 刻意不重置：它是用户的阅读偏好，不是某条短链的属性，带过去更顺手。
 * 清空后 `statsDays` 的 watcher 仍可能跑一次 `loadStats()`，但那时 `link` 还是 null，
 * 两个加载函数开头的 `if (!link.value) return` 会直接返回，不会发出多余请求。
 */
function resetForLinkChange(): void {
  link.value = null
  stats.value = null
  clicks.value = []
  clicksCursor.value = ''
  notFound.value = false
  loadError.value = ''
  clicksError.value = ''
  editOpen.value = false
  editError.value = ''
  editPassword.value = ''
}

onMounted(loadAll)

/**
 * ⚠️ 必须监听路由参数，不能只靠 onMounted。
 *
 * 路由是 `links/:code`，`/links/A` 与 `/links/B` 的路由名与参数形状都相同，
 * 而本组件的 `<RouterView>` 没有 `:key` —— Vue 会**复用同一个组件实例**，
 * onMounted 不会再跑。于是页面继续显示 A 的标题、统计、明细与二维码，
 * 更糟的是「保存 / 删除 / 认领」都会打到 A 的短码上。
 */
watch(code, () => {
  resetForLinkChange()
  void loadAll()
})

// 离开页面时取消在飞的请求：响应回来时组件已经卸载，写状态既无意义也易出错
onUnmounted(() => {
  linkGuard.cancel()
  statsGuard.cancel()
  clicksGuard.cancel()
})
</script>

<template>
  <div class="bg-canvas py-12 md:py-16">
    <div class="container-page">
      <!-- 加载中 -->
      <Spinner v-if="loading" :size="18">正在加载…</Spinner>

      <!-- 404：无权限与不存在在后端是同一个响应，这里也不做区分 -->
      <Card v-else-if="notFound" class="p-8">
        <EmptyState
          title="找不到这条短链"
          description="它可能已被删除；也可能是你换了浏览器或清了缓存，导致匿名管理密钥丢失。"
        >
          <Button :to="{ name: 'landing' }" variant="primary">回首页创建新短链</Button>
        </EmptyState>
      </Card>

      <p v-else-if="loadError" class="text-[14px] text-error">{{ loadError }}</p>

      <template v-else-if="link">
        <!-- 页头 -->
        <PageHeader
          eyebrow="Link detail"
          :title="`/${link.short_code}`"
          title-class="font-mono text-[32px] md:text-[40px]"
        >
          <div class="space-y-2.5">
            <p v-if="link.title" class="text-[16px] text-body">{{ link.title }}</p>
            <p v-if="link.tags?.length" class="flex flex-wrap gap-1.5">
              <Badge v-for="tag in link.tags" :key="tag" variant="quiet">#{{ tag }}</Badge>
            </p>
            <p class="break-anywhere font-mono text-[13px]">{{ link.target_url }}</p>
            <div class="flex flex-wrap items-center gap-x-5 gap-y-2 text-[13px]">
              <span>{{ describeStatus(link.status).label }}</span>
              <span>{{ describeExpiry(link.expires_at) }}</span>
              <span>创建于 {{ formatDateTime(link.created_at) }}</span>
              <span v-if="link.anonymous">匿名创建</span>
              <span v-if="link.password_protected">受口令保护</span>
            </div>
          </div>

          <template #actions>
            <CopyButton variant="secondary" :value="link.short_url" label="复制短链" />
            <Button variant="secondary" :href="link.short_url">打开</Button>
            <Button variant="secondary" @click="editOpen = !editOpen">
              {{ editOpen ? '取消编辑' : '编辑' }}
            </Button>
            <Button variant="danger" :loading="deleting" @click="handleDelete">删除</Button>
          </template>
        </PageHeader>

        <!-- 二维码：扫码打开（深墨前景 + 暖奶油底，对比度 ≈19:1） -->
        <Card class="mt-8 p-6 md:p-8">
          <div class="flex flex-col gap-6 sm:flex-row sm:items-center">
            <div
              class="h-40 w-40 shrink-0 rounded-lg border border-hairline bg-canvas p-2"
            >
              <canvas
                ref="qrCanvas"
                class="h-full w-full"
                aria-label="短链二维码"
                role="img"
              />
            </div>

            <div class="min-w-0">
              <p class="eyebrow">QR code</p>
              <p class="mt-2 text-[14px] leading-[1.55] text-body">
                扫码即可打开这条短链。下载的 PNG 是 1024×1024、纯白底，可直接打印或贴进海报。
              </p>
              <p class="mt-3 break-anywhere font-mono text-[13px] text-muted">
                {{ link.short_url }}
              </p>
              <p v-if="qrError" class="mt-3 text-[13px] text-error">{{ qrError }}</p>
              <div class="mt-4">
                <Button variant="secondary" :loading="downloadingQR" @click="downloadQR">
                  下载二维码
                </Button>
              </div>
            </div>
          </div>
        </Card>

        <!-- 认领提示（珊瑚 callout：全站少数几个允许珊瑚满铺的位置） -->
        <Card
          v-if="canClaim"
          variant="coral"
          class="mt-8 flex flex-col items-start justify-between gap-5 md:flex-row md:items-center"
        >
          <div>
            <p class="title-md text-on-primary">这条短链还是匿名状态</p>
            <p class="mt-2 max-w-2xl text-[14px] leading-[1.55] text-on-primary/85">
              你的浏览器里存着它的管理密钥。认领之后它就归属你的账号，换设备登录也能管理。
            </p>
          </div>
          <Button variant="secondary" :loading="claiming" class="shrink-0" @click="handleClaim">
            认领到我的账号
          </Button>
        </Card>

        <!-- 编辑面板 -->
        <Card v-if="editOpen" class="mt-6 p-6 md:p-8">
          <p class="title-md">修改短链</p>
          <div class="mt-5 grid gap-4 md:grid-cols-2">
            <Input v-model="editTarget" label="目标地址" placeholder="https://example.com/new" />
            <Input v-model="editTitle" label="标题" placeholder="给这条链接起个名字" :maxlength="200" />
            <Input
              v-model="editTags"
              label="标签"
              placeholder="ops, docs"
              hint="逗号分隔；清空即删除全部标签"
            />
            <Select
              v-model="editStatus"
              label="状态"
              :options="statusOptions"
              hint="停用后跳转返回 410，短链本身仍然保留"
            />
            <Input
              v-model="editPassword"
              label="访问口令"
              type="password"
              autocomplete="new-password"
              placeholder="留空表示不改"
              hint="至少 8 位；设置后需凭口令跳转"
            />
            <div class="flex items-end gap-2">
              <Button variant="secondary" @click="clearExpiry">改为永久有效</Button>
              <Button
                v-if="link.password_protected"
                variant="secondary"
                @click="clearPassword"
              >
                清除口令
              </Button>
            </div>
          </div>
          <p v-if="editError" class="mt-3 text-[13px] text-error">{{ editError }}</p>
          <div class="mt-5 flex items-center gap-3">
            <Button :loading="saving" @click="saveEdit">保存</Button>
            <Button variant="text" @click="editOpen = false">取消</Button>
          </div>
          <p class="mt-3 text-[13px] text-muted">
            保存后后端会主动失效该短码的缓存，改动立即可见。
          </p>
        </Card>

        <!-- 统计指标卡 -->
        <div class="mt-8 grid gap-4 sm:grid-cols-3">
          <StatCard label="总点击" :value="stats?.total_clicks ?? link.click_count" hint="全部时间" />
          <StatCard label="窗口内点击" :value="windowClicks" :hint="`最近 ${stats?.days ?? statsDays} 天`" />
          <StatCard label="日均" :value="dailyAverage" :hint="`最近 ${stats?.days ?? statsDays} 天平均`" />
        </div>

        <!-- 趋势图 -->
        <Section eyebrow="Trend">
          <template #actions>
            <SegmentedControl v-model="statsDays" :options="dayOptions" label="统计时间窗口" />
          </template>

          <Spinner v-if="loadingStats" :size="16">正在更新…</Spinner>
          <template v-else-if="stats">
            <TrendChart :points="stats.daily" />
            <p v-if="peakDay.clicks > 0" class="mt-3 text-[13px] text-muted">
              峰值出现在 {{ peakDay.date }}，共 {{ formatNumber(peakDay.clicks) }} 次点击。
            </p>
          </template>
        </Section>

        <!-- 分布（同一奶油卡片内并排，维度少不需要拆卡） -->
        <Section eyebrow="Distribution">
          <!--
            国家这一维只在部署了 GeoIP（且这段时间内确实解析出过国家）时才出现，
            于是栅格列数要跟着变：xl 下 4 列还是 3 列。
            不改默认的 md:grid-cols-3，是为了让「没开 GeoIP」的部署版式与改动前逐字一致。
          -->
          <div
            class="grid gap-10 md:grid-cols-3"
            :class="countryItems.length > 0 ? 'xl:grid-cols-4' : ''"
          >
            <DistributionList
              title="来源"
              :items="refererItems"
              empty-description="Referer 为空的访问会归入「直接访问」。"
            />
            <DistributionList title="设备" :items="deviceItems" />
            <DistributionList title="浏览器" :items="browserItems" />
            <DistributionList
              v-if="countryItems.length > 0"
              title="国家"
              :items="countryItems"
            />
          </div>
        </Section>

        <!-- 点击明细：与统计同窗口，时间倒序，keyset 分页 -->
        <Section eyebrow="Recent clicks">
          <template #description>
            最近 {{ stats?.days ?? statsDays }} 天，时间倒序；IP 只显示到网段（IPv4 /24、IPv6 /64）。
          </template>
          <template #actions>
            <span v-if="clicks.length" class="text-[13px] text-muted">
              已加载 {{ formatNumber(clicks.length) }} 条
            </span>
          </template>

          <Spinner v-if="loadingClicks && clicks.length === 0" :size="16" class="mt-6">
            正在加载…
          </Spinner>
          <p v-else-if="clicksError" class="mt-6 text-[13px] text-error">{{ clicksError }}</p>

          <EmptyState
            v-else-if="clicks.length === 0"
            class="mt-6"
            title="这段时间还没有点击"
            description="明细来自点击事件表，worker 每 2 秒回刷一次；刚发生的跳转可能还要几秒才出现。"
          />

          <template v-else>
            <!-- 桌面端：表格 -->
            <div class="mt-4 hidden overflow-x-auto md:block">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>设备</th>
                    <th>浏览器 / 系统</th>
                    <th>来源</th>
                    <th>IP 网段</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="click in clicks" :key="click.id">
                    <td class="whitespace-nowrap font-mono text-[13px]">
                      {{ formatDateTimeSeconds(click.occurred_at) }}
                    </td>
                    <td class="whitespace-nowrap">{{ describeDevice(click.device ?? 'unknown') }}</td>
                    <td class="whitespace-nowrap text-[13px] text-muted">
                      {{ describeClient(click.browser, click.os) }}
                    </td>
                    <td class="text-[13px]" :title="click.referer">
                      {{ describeReferer(click.referer ?? '') }}
                    </td>
                    <td class="whitespace-nowrap font-mono text-[13px] text-muted">
                      {{ click.ip || '—' }}
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>

            <!-- 窄屏：卡片堆叠（不横向滚动表格，与链接列表同一策略） -->
            <ul class="mt-4 space-y-3 md:hidden">
              <Card v-for="click in clicks" :key="click.id" tag="li" class="p-4">
                <div class="flex items-start justify-between gap-3">
                  <span class="font-mono text-[13px] text-ink">
                    {{ formatDateTimeSeconds(click.occurred_at) }}
                  </span>
                  <Badge variant="quiet" class="shrink-0">
                    {{ describeDevice(click.device ?? 'unknown') }}
                  </Badge>
                </div>
                <p class="mt-2 text-[13px] text-muted">
                  {{ describeClient(click.browser, click.os) }}
                </p>
                <p class="mt-1 break-anywhere text-[13px] text-muted">
                  来源：{{ describeReferer(click.referer ?? '') }}
                </p>
                <p class="mt-1 font-mono text-[12px] text-muted-soft">IP {{ click.ip || '—' }}</p>
              </Card>
            </ul>

            <div class="mt-5 flex items-center gap-3">
              <Button
                v-if="clicksCursor"
                variant="secondary"
                :loading="loadingClicks"
                @click="loadClicks(false)"
              >
                加载更多
              </Button>
              <span v-else class="text-[13px] text-muted">已经到底了。</span>
            </div>
          </template>
        </Section>

        <p class="mt-6 text-[13px] text-muted">
          数字口径：总点击 = 数据库基线 + 待同步增量（worker 每 2 秒回刷）；分布与趋势基于点击明细表。
        </p>

        <p class="mt-8">
          <RouterLink :to="{ name: 'dashboard' }" class="text-link">← 返回我的链接</RouterLink>
        </p>
      </template>
    </div>
  </div>
</template>
