<script setup lang="ts">
/**
 * 分布列表：来源 / 设备 / 浏览器共用。
 *
 * 用「标题 + 计数 + 一条等宽进度条」而不是饼图 —— 维度少、基数小时，
 * 横向条比饼图更容易比较，也更贴 DESIGN.md 的克制风格。
 */
import { computed } from 'vue'

import EmptyState from '@/components/ui/EmptyState.vue'
import type { DistributionItem } from '@/types/ui'
import { formatNumber } from '@/utils/format'

const props = defineProps<{
  title: string
  items: DistributionItem[]
  /** 无数据时的说明文案。 */
  emptyDescription?: string
}>()

/** 用于计算条宽的比例基准：取最大值，让榜首占满。 */
const peak = computed(() => Math.max(1, ...props.items.map((item) => item.value)))

function widthOf(value: number): string {
  return `${Math.max(2, (value / peak.value) * 100)}%`
}
</script>

<template>
  <div>
    <p class="title-md">{{ title }}</p>

    <EmptyState
      v-if="items.length === 0"
      title="暂无数据"
      :description="emptyDescription ?? '有访问量之后这里才会有分布。'"
    />

    <ul v-else class="mt-5 space-y-4">
      <li v-for="item in items" :key="item.label">
        <div class="flex items-baseline justify-between gap-3 text-[14px]">
          <span class="min-w-0 truncate text-ink" :title="item.label">{{ item.label }}</span>
          <span class="shrink-0 text-muted">{{ formatNumber(item.value) }}</span>
        </div>
        <!-- 轨道用奶油卡片色，填充用珊瑚。只有这一处允许珊瑚做数据色 -->
        <div class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-surface-card">
          <div class="h-full rounded-full bg-primary" :style="{ width: widthOf(item.value) }" />
        </div>
      </li>
    </ul>
  </div>
</template>
