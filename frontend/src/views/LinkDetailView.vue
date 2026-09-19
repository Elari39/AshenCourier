<script setup lang="ts">
/**
 * 链接详情 + 统计。
 *
 * 鉴权：登录用户用自己的账号，匿名创建者用 localStorage 里的 manage_key。
 * 后端对「无权限」和「不存在」都回 404，所以这里只需要处理一种失败态。
 */
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import QRCode from 'qrcode'

import { ApiError, linksApi } from '@/api/client'
import type { ClickEvent, Link, Stats } from '@/api/types'
import DistributionList from '@/components/DistributionList.vue'
import StatCard from '@/components/StatCard.vue'
import TrendChart from '@/components/TrendChart.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useAuth } from '@/composables/useAuth'
import { useCopy } from '@/composables/useCopy'
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

const route = useRoute()
const router = useRouter()
const toast = useToast()
const { isAuthenticated, manageKeyFor, forgetManageKey } = useAuth()
const { copied, copy } = useCopy()

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

/** 是否是「匿名创建且我有密钥」——只有这种状态才提示可认领。 */
const canClaim = computed(
  () => isAuthenticated.value && link.value?.anonymous === true && manageKey.value !== null,
)

/** 把逗号分隔的输入拆成标签数组（中文逗号也认）；空输入返回空数组 = 清空标签。 */
function splitTags(raw: string): string[] {
  return raw
    .split(/[,，]/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0)
}

async function loadLink(): Promise<void> {
  loading.value = true
  notFound.value = false
  loadError.value = ''

  try {
    link.value = await linksApi.get(code.value, manageKey.value)
    editTitle.value = link.value.title ?? ''
    editTarget.value = link.value.target_url
    editTags.value = (link.value.tags ?? []).join(', ')
    editStatus.value = link.value.status === 'disabled' ? 'disabled' : 'active'
  } catch (cause) {
    if (cause instanceof ApiError && cause.status === 404) {
      notFound.value = true
    } else {
      loadError.value = cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试'
    }
  } finally {
    loading.value = false
  }
}

async function loadStats(): Promise<void> {
  if (!link.value) return
  loadingStats.value = true
  try {
    stats.value = await linksApi.stats(code.value, statsDays.value, manageKey.value)
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '统计加载失败')
  } finally {
    loadingStats.value = false
  }
}

async function loadClicks(reset = true): Promise<void> {
  if (!link.value) return

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
    )
    clicks.value = reset ? page.clicks : [...clicks.value, ...page.clicks]
    clicksCursor.value = page.next_cursor ?? ''
  } catch (cause) {
    // 明细加载失败不影响页面其余部分：单独报错，统计与趋势照常显示
    clicksError.value = cause instanceof ApiError ? cause.friendly : '点击明细加载失败'
  } finally {
    loadingClicks.value = false
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
      },
      manageKey.value,
    )
    link.value = updated
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

async function handleDelete(): Promise<void> {
  if (!link.value) return
  if (!window.confirm(`确定要删除 /${code.value} 吗？删除后短链立即失效。`)) return

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

async function copyShortURL(): Promise<void> {
  if (!link.value) return
  const ok = await copy(link.value.short_url)
  if (ok) {
    toast.success('短链已复制')
  } else {
    toast.error('复制失败，请手动选中复制')
  }
}

// 切换统计窗口时重拉统计与明细：两者共用同一个窗口，口径必须一致
watch(statsDays, () => {
  void loadStats()
  void loadClicks()
})

onMounted(async () => {
  await loadLink()
  if (link.value) {
    await Promise.all([loadStats(), loadClicks(), renderQR()])
  }
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
        <div class="flex flex-wrap items-start justify-between gap-6">
          <div class="min-w-0">
            <p class="eyebrow">Link detail</p>
            <h1 class="display-lg mt-3 break-anywhere font-mono text-[32px] md:text-[40px]">
              /{{ link.short_code }}
            </h1>
            <p v-if="link.title" class="mt-2 text-[16px] text-body">{{ link.title }}</p>
            <p v-if="link.tags?.length" class="mt-2 flex flex-wrap gap-1.5">
              <span v-for="tag in link.tags" :key="tag" class="badge badge-quiet">#{{ tag }}</span>
            </p>
            <p class="mt-3 break-anywhere font-mono text-[13px] text-muted">{{ link.target_url }}</p>
            <div class="mt-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-[13px] text-muted">
              <span>{{ describeStatus(link.status).label }}</span>
              <span>{{ describeExpiry(link.expires_at) }}</span>
              <span>创建于 {{ formatDateTime(link.created_at) }}</span>
              <span v-if="link.anonymous">匿名创建</span>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2">
            <Button variant="secondary" @click="copyShortURL">{{ copied ? '已复制' : '复制短链' }}</Button>
            <Button variant="secondary" :href="link.short_url">打开</Button>
            <Button variant="secondary" @click="editOpen = !editOpen">
              {{ editOpen ? '取消编辑' : '编辑' }}
            </Button>
            <Button variant="danger" :loading="deleting" @click="handleDelete">删除</Button>
          </div>
        </div>

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
        <div v-if="canClaim" class="card-coral mt-8 flex flex-col items-start justify-between gap-5 md:flex-row md:items-center">
          <div>
            <p class="title-md text-on-primary">这条短链还是匿名状态</p>
            <p class="mt-2 max-w-2xl text-[14px] leading-[1.55] text-on-primary/85">
              你的浏览器里存着它的管理密钥。认领之后它就归属你的账号，换设备登录也能管理。
            </p>
          </div>
          <Button variant="secondary" :loading="claiming" class="shrink-0" @click="handleClaim">
            认领到我的账号
          </Button>
        </div>

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
            <div>
              <label class="field-label" for="detail-status">状态</label>
              <select id="detail-status" v-model="editStatus" class="text-input">
                <option value="active">正常</option>
                <option value="disabled">停用（跳转返回 410）</option>
              </select>
            </div>
            <div class="flex items-end">
              <Button variant="secondary" @click="clearExpiry">改为永久有效</Button>
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
        <Card class="mt-6 p-6 md:p-8">
          <div class="flex flex-wrap items-center justify-between gap-4">
            <p class="eyebrow">Trend</p>
            <div class="flex items-center gap-1">
              <button
                v-for="option in [7, 30, 90]"
                :key="option"
                type="button"
                class="rounded-md px-3.5 py-2 text-[14px] font-medium"
                :class="statsDays === option ? 'bg-surface-card text-ink' : 'text-muted'"
                @click="statsDays = option"
              >
                {{ option }} 天
              </button>
            </div>
          </div>

          <div class="mt-2">
            <Spinner v-if="loadingStats" :size="16">正在更新…</Spinner>
            <template v-else-if="stats">
              <TrendChart :points="stats.daily" />
              <p v-if="peakDay.clicks > 0" class="mt-3 text-[13px] text-muted">
                峰值出现在 {{ peakDay.date }}，共 {{ formatNumber(peakDay.clicks) }} 次点击。
              </p>
            </template>
          </div>
        </Card>

        <!-- 三个分布（同一奶油卡片内并排，维度少不需要拆卡） -->
        <Card class="mt-6 p-6 md:p-8">
          <p class="eyebrow">Distribution</p>
          <div class="mt-6 grid gap-10 md:grid-cols-3">
            <DistributionList
              title="来源"
              :items="refererItems"
              empty-description="Referer 为空的访问会归入「直接访问」。"
            />
            <DistributionList title="设备" :items="deviceItems" />
            <DistributionList title="浏览器" :items="browserItems" />
          </div>
        </Card>

        <!-- 点击明细：与统计同窗口，时间倒序，keyset 分页 -->
        <Card class="mt-6 p-6 md:p-8">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p class="eyebrow">Recent clicks</p>
              <p class="mt-2 text-[13px] text-muted">
                最近 {{ stats?.days ?? statsDays }} 天，时间倒序；IP 只显示到网段（IPv4 /24、IPv6 /64）。
              </p>
            </div>
            <span v-if="clicks.length" class="text-[13px] text-muted">
              已加载 {{ formatNumber(clicks.length) }} 条
            </span>
          </div>

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
            <div class="mt-4 hidden overflow-hidden md:block">
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
              <li v-for="click in clicks" :key="click.id" class="card-cream p-4">
                <div class="flex items-start justify-between gap-3">
                  <span class="font-mono text-[13px] text-ink">
                    {{ formatDateTimeSeconds(click.occurred_at) }}
                  </span>
                  <span class="badge badge-quiet shrink-0">
                    {{ describeDevice(click.device ?? 'unknown') }}
                  </span>
                </div>
                <p class="mt-2 text-[13px] text-muted">
                  {{ describeClient(click.browser, click.os) }}
                </p>
                <p class="mt-1 break-anywhere text-[13px] text-muted">
                  来源：{{ describeReferer(click.referer ?? '') }}
                </p>
                <p class="mt-1 font-mono text-[12px] text-muted-soft">IP {{ click.ip || '—' }}</p>
              </li>
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
        </Card>

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
