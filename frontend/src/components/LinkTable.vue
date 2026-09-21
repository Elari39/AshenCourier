<script setup lang="ts">
/**
 * 链接列表。
 *
 * 桌面端是表格；窄屏（<768px）退化成卡片堆叠 —— DESIGN.md 的响应式策略是
 * 「减少列数而不是把卡片压小」，这里同理：不横向滚动表格，而是换一种排布。
 */
import { RouterLink } from 'vue-router'

import type { Link } from '@/api/types'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import CopyButton from '@/components/ui/CopyButton.vue'
import {
  describeExpiry,
  describeStatus,
  formatDateTime,
  formatNumber,
  hostOf,
  truncateMiddle,
} from '@/utils/format'

const props = defineProps<{
  links: Link[]
  /** 正在删除中的短码，用于禁用按钮。 */
  pendingCode?: string
}>()

const emit = defineEmits<{
  (event: 'delete', code: string): void
  /** 点击某个标签 → 让父组件按该标签筛选（列表页才有意义）。 */
  (event: 'filter-tag', tag: string): void
}>()

/**
 * 状态 → 徽章形态。
 *
 * 桌面表格与窄屏卡片**共用这一个映射**：原先桌面分支用 statusClass() 拼 class 字符串、
 * 窄屏分支直接写 <Badge>，同一个语义有两份实现 —— 加一个状态就得想起两处，
 * 而且「text-error」根本用不上 badge-error 的底色。
 */
function statusVariant(status: string): 'pill' | 'quiet' | 'error' {
  const { tone } = describeStatus(status)
  if (tone === 'error') return 'error'
  if (tone === 'muted') return 'quiet'
  return 'pill'
}
</script>

<template>
  <div>
    <!-- ---------- 桌面端：表格 ---------- -->
    <div class="hidden overflow-x-auto md:block">
      <table class="data-table">
        <thead>
          <tr>
            <th>短码 / 标题</th>
            <th>目标地址</th>
            <th>状态</th>
            <th class="text-right">点击</th>
            <th>创建时间</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="link in props.links" :key="link.id">
            <td>
              <RouterLink
                :to="{ name: 'link-detail', params: { code: link.short_code } }"
                class="link-quiet font-mono text-[13px] text-ink"
              >
                /{{ link.short_code }}
              </RouterLink>
              <p v-if="link.title" class="mt-0.5 text-[13px] text-muted">{{ link.title }}</p>
              <p v-if="link.tags?.length" class="mt-1 flex flex-wrap gap-1">
                <button
                  v-for="tag in link.tags"
                  :key="tag"
                  type="button"
                  class="badge badge-quiet badge-action"
                  :title="`按标签「${tag}」筛选`"
                  @click="emit('filter-tag', tag)"
                >
                  #{{ tag }}
                </button>
              </p>
            </td>
            <td>
              <span class="text-[13px] text-muted" :title="link.target_url">
                {{ hostOf(link.target_url) }}
              </span>
              <p class="mt-0.5 font-mono text-[12px] text-muted-soft">
                {{ truncateMiddle(link.target_url, 26, 12) }}
              </p>
            </td>
            <td>
              <Badge :variant="statusVariant(link.status)">
                {{ describeStatus(link.status).label }}
              </Badge>
              <p class="mt-1 text-[12px] text-muted-soft">{{ describeExpiry(link.expires_at) }}</p>
            </td>
            <td class="text-right font-mono text-[13px] text-ink">
              {{ formatNumber(link.click_count) }}
            </td>
            <td class="text-[13px] text-muted">{{ formatDateTime(link.created_at) }}</td>
            <td>
              <div class="flex items-center justify-end gap-1">
                <CopyButton :value="link.short_url" variant="text" size="sm" />
                <Button variant="text" size="sm" :href="link.short_url">打开</Button>
                <Button
                  variant="danger"
                  size="sm"
                  :disabled="props.pendingCode === link.short_code"
                  @click="emit('delete', link.short_code)"
                >
                  删除
                </Button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- ---------- 窄屏：卡片 ---------- -->
    <ul class="space-y-3 md:hidden">
      <Card v-for="link in props.links" :key="link.id" tag="li" class="p-5">
        <div class="flex items-start justify-between gap-3">
          <RouterLink
            :to="{ name: 'link-detail', params: { code: link.short_code } }"
            class="font-mono text-[14px] text-ink"
          >
            /{{ link.short_code }}
          </RouterLink>
          <Badge :variant="statusVariant(link.status)">
            {{ describeStatus(link.status).label }}
          </Badge>
        </div>

        <p v-if="link.title" class="mt-2 text-[14px] text-ink">{{ link.title }}</p>
        <p v-if="link.tags?.length" class="mt-1.5 flex flex-wrap gap-1">
          <button
            v-for="tag in link.tags"
            :key="tag"
            type="button"
            class="badge badge-quiet badge-action"
            @click="emit('filter-tag', tag)"
          >
            #{{ tag }}
          </button>
        </p>
        <p class="mt-2 break-anywhere font-mono text-[12px] text-muted">
          {{ truncateMiddle(link.target_url, 30, 14) }}
        </p>

        <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted">
          <span>{{ formatNumber(link.click_count) }} 次点击</span>
          <span>{{ describeExpiry(link.expires_at) }}</span>
          <span>{{ formatDateTime(link.created_at) }}</span>
        </div>

        <div class="mt-4 flex items-center gap-2">
          <CopyButton :value="link.short_url" variant="secondary" size="sm" />
          <Button variant="secondary" size="sm" :href="link.short_url">打开</Button>
          <Button
            variant="danger"
            size="sm"
            class="ml-auto"
            :disabled="props.pendingCode === link.short_code"
            @click="emit('delete', link.short_code)"
          >
            删除
          </Button>
        </div>
      </Card>
    </ul>
  </div>
</template>
