<script setup lang="ts">
/**
 * 按天趋势图 —— 纯手写 SVG，零依赖。
 *
 * 为什么不用 ECharts：我们只需要「面积 + 折线 + 稀疏刻度」这一种图，
 * 手写能完全贴合 DESIGN.md 的克制风格（珊瑚描边 + 奶油卡片填充 + 无网格线），
 * 也不用为了一格图引入几百 KB 的依赖。
 *
 * 移动端：整张图放进可横向滚动的容器，最小宽度 560px，保证折线不被压成锯齿。
 */
import { computed, useId } from 'vue'

import type { DailyPoint } from '@/api/types'
import EmptyState from '@/components/ui/EmptyState.vue'
import { formatNumber, formatShortDate } from '@/utils/format'

const props = defineProps<{
  points: DailyPoint[]
}>()

const VIEW_WIDTH = 880
const VIEW_HEIGHT = 240
const PAD_TOP = 16
const PAD_BOTTOM = 28
const PAD_LEFT = 8
const PAD_RIGHT = 8

/** 绘图区高度。 */
const plotHeight = VIEW_HEIGHT - PAD_TOP - PAD_BOTTOM

/**
 * 面积渐变的 id 必须**每个实例各不相同**。
 *
 * 原先写死 `id="trend-fill"`：一个页面上只有一张图时看不出问题，但 SVG 的 id
 * 是文档级的 —— 两张图会拿到同一个 id，`url(#trend-fill)` 一律指向文档里第一个
 * 渐变节点，后一张图的填充就跟着第一张走（同一份配色时连症状都没有，
 * 改了一张的配色才会发现另一张跟着变）。useId 给的是组件级唯一值。
 */
const fillId = `trend-fill-${useId()}`

const total = computed(() => props.points.reduce((sum, point) => sum + point.clicks, 0))

/** y 轴最大值：给个 1 的下限，避免全 0 时除零。 */
const maxClicks = computed(() => Math.max(1, ...props.points.map((point) => point.clicks)))

/** 数据点。后端已把空缺日期补成 0，这里直接按原顺序绘制即可。 */
const series = computed(() => props.points)

/** 每个点的 x 坐标。只有一个点时放在正中间。 */
function xAt(index: number): number {
  const count = series.value.length
  if (count <= 1) return VIEW_WIDTH / 2
  const usable = VIEW_WIDTH - PAD_LEFT - PAD_RIGHT
  return PAD_LEFT + (usable * index) / (count - 1)
}

/** 每个点的 y 坐标。 */
function yAt(clicks: number): number {
  const ratio = clicks / maxClicks.value
  return PAD_TOP + plotHeight * (1 - ratio)
}

/** 折线路径。 */
const linePath = computed(() => {
  if (series.value.length === 0) return ''
  return series.value
    .map((point, index) => `${index === 0 ? 'M' : 'L'}${xAt(index).toFixed(2)},${yAt(point.clicks).toFixed(2)}`)
    .join(' ')
})

/** 面积路径：折线 + 回到底边闭合。 */
const areaPath = computed(() => {
  if (series.value.length === 0) return ''
  const base = PAD_TOP + plotHeight
  const firstX = xAt(0).toFixed(2)
  const lastX = xAt(series.value.length - 1).toFixed(2)
  return `${linePath.value} L${lastX},${base} L${firstX},${base} Z`
})

/** x 轴稀疏刻度：首、中、尾（点数多时才显示中间那条）。 */
const ticks = computed(() => {
  const count = series.value.length
  if (count === 0) return []
  const indexes = count > 6 ? [0, Math.floor((count - 1) / 2), count - 1] : [0, count - 1]
  return [...new Set(indexes)].map((index) => ({
    x: xAt(index),
    label: formatShortDate(series.value[index]?.date ?? ''),
  }))
})

/** y 轴只标最大值与 0，足够读懂量级又不喧哗。 */
const yTicks = computed(() => [
  { y: PAD_TOP, label: formatNumber(maxClicks.value) },
  { y: PAD_TOP + plotHeight, label: '0' },
])
</script>

<template>
  <div>
    <div class="flex flex-wrap items-baseline justify-between gap-2">
      <p class="title-md">按天趋势</p>
      <p class="text-[13px] text-muted">
        窗口内共 <span class="text-ink">{{ formatNumber(total) }}</span> 次点击
      </p>
    </div>

    <EmptyState
      v-if="series.length === 0"
      title="还没有数据"
      description="把短链发出去后，这里会按天累积点击趋势。"
    />

    <!-- 窄屏允许横向滚动，而不是把折线压扁 -->
    <div v-else class="mt-4 -mx-1 overflow-x-auto px-1">
      <!-- 有 viewBox 且不指定 height 时，SVG 会按 viewBox 比例自适应高度：
           宽屏等比放大、窄屏等比缩小，不会出现描边被拉伸变形 -->
      <svg
        class="h-auto w-full min-w-[560px]"
        :viewBox="`0 0 ${VIEW_WIDTH} ${VIEW_HEIGHT}`"
        role="img"
        aria-label="按天点击趋势折线图"
      >
        <defs>
          <linearGradient :id="fillId" x1="0" y1="0" x2="0" y2="1">
            <stop class="chart-stop" offset="0%" stop-opacity="0.22" />
            <stop class="chart-stop" offset="100%" stop-opacity="0.02" />
          </linearGradient>
        </defs>

        <!-- y 轴：只保留 0 与最大值两条极淡的参考线 -->
        <line
          v-for="tick in yTicks"
          :key="`y-${tick.y}`"
          :x1="PAD_LEFT"
          :x2="VIEW_WIDTH - PAD_RIGHT"
          :y1="tick.y"
          :y2="tick.y"
          class="chart-guide"
          stroke-width="1"
        />

        <path :d="areaPath" :fill="`url(#${fillId})`" />
        <path
          :d="linePath"
          class="chart-line"
          fill="none"
          stroke-width="2"
          stroke-linejoin="round"
          stroke-linecap="round"
        />

        <!-- x 轴刻度 -->
        <text
          v-for="tick in ticks"
          :key="`x-${tick.x}`"
          :x="tick.x"
          :y="VIEW_HEIGHT - 8"
          class="chart-tick"
          text-anchor="middle"
        >
          {{ tick.label }}
        </text>

        <!-- y 轴刻度值放在左上 / 左下 -->
        <text
          v-for="tick in yTicks"
          :key="`yl-${tick.y}`"
          :x="VIEW_WIDTH - PAD_RIGHT"
          :y="tick.y - 4"
          class="chart-tick"
          text-anchor="end"
        >
          {{ tick.label }}
        </text>
      </svg>
    </div>
  </div>
</template>
