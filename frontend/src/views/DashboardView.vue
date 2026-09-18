<script setup lang="ts">
/**
 * 我的链接（需要登录）。
 *
 * 数据源只有 `GET /api/links` 一个接口：顶部三张指标卡的数字直接从当前
 * 已加载的列表算出来 —— 不再为此新增一个「账号级汇总」接口，
 * MVP 阶段多一个接口就多一份要维护的契约。
 */
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, linksApi } from '@/api/client'
import type { Link } from '@/api/types'
import LinkTable from '@/components/LinkTable.vue'
import ResultCard from '@/components/ResultCard.vue'
import ShortenForm from '@/components/ShortenForm.vue'
import StatCard from '@/components/StatCard.vue'
import Button from '@/components/ui/Button.vue'
import Card from '@/components/ui/Card.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Input from '@/components/ui/Input.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useAuth } from '@/composables/useAuth'
import { useToast } from '@/composables/useToast'

const PAGE_SIZE = 20

const toast = useToast()
const { refreshMe } = useAuth()

const links = ref<Link[]>([])
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')
const query = ref('')
const deletingCode = ref('')
const latest = ref<{ link: Link; manageKey?: string } | null>(null)

/** 指标卡口径：都基于「已加载的列表」，界面上会写清楚。 */
const totalLinks = computed(() => links.value.length)
const totalClicks = computed(() => links.value.reduce((sum, link) => sum + link.click_count, 0))
const activeLinks = computed(() => links.value.filter((link) => link.status === 'active').length)

const hasMore = computed(() => nextCursor.value !== '')

/** 首次加载 / 搜索变化时重置列表。 */
async function reload(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const result = await linksApi.list({ limit: PAGE_SIZE, q: query.value.trim() || undefined })
    links.value = result.links
    nextCursor.value = result.next_cursor ?? ''
  } catch (cause) {
    loadError.value = cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试'
  } finally {
    loading.value = false
  }
}

/** 追加下一页。 */
async function loadMore(): Promise<void> {
  if (!hasMore.value || loadingMore.value) return

  loadingMore.value = true
  try {
    const result = await linksApi.list({
      limit: PAGE_SIZE,
      cursor: nextCursor.value,
      q: query.value.trim() || undefined,
    })
    links.value = [...links.value, ...result.links]
    nextCursor.value = result.next_cursor ?? ''
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试')
  } finally {
    loadingMore.value = false
  }
}

/** 删除一条（软删除）。 */
async function handleDelete(code: string): Promise<void> {
  if (!window.confirm(`确定要删除 /${code} 吗？删除后短链立即失效。`)) return

  deletingCode.value = code
  try {
    await linksApi.remove(code)
    links.value = links.value.filter((link) => link.short_code !== code)
    toast.success('已删除')
  } catch (cause) {
    toast.error(cause instanceof ApiError ? cause.friendly : '删除失败，请稍后重试')
  } finally {
    deletingCode.value = ''
  }
}

function onCreated(payload: { link: Link; manageKey?: string }): void {
  latest.value = payload
  toast.success('短链已创建')
  // 新建的链接排在最前（后端按 created_at DESC），直接插到列表头部
  links.value = [payload.link, ...links.value]
}

// 搜索：300ms 防抖，避免每敲一个字就打一次接口
let searchTimer: number | undefined
watch(query, () => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => void reload(), 300)
})

onMounted(async () => {
  // 并行：刷新用户资料（令牌可能已过期）+ 拉列表。令牌失效时 401 处理器会接管跳转
  void refreshMe().catch(() => undefined)
  await reload()
})
</script>

<template>
  <div class="bg-canvas py-12 md:py-16">
    <div class="container-page">
      <!-- 页头 -->
      <div class="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="eyebrow">Dashboard</p>
          <h1 class="display-lg mt-3">我的链接</h1>
        </div>
        <p class="text-[13px] text-muted">共加载 {{ totalLinks }} 条</p>
      </div>

      <!-- 创建区（奶油卡片） -->
      <Card class="mt-8 p-6 md:p-8">
        <p class="title-md">新建短链</p>
        <p class="mt-1.5 text-[14px] text-muted">登录状态下创建的链接会直接归属到你的账号。</p>
        <div class="mt-5">
          <ShortenForm @created="onCreated" />
        </div>
      </Card>

      <!-- 刚创建的结果卡（深色） -->
      <div v-if="latest" class="mt-6">
        <ResultCard :link="latest.link" :manage-key="latest.manageKey" />
      </div>

      <!-- 指标卡 3-up -->
      <div class="mt-6 grid gap-4 sm:grid-cols-3">
        <StatCard label="已加载链接" :value="totalLinks" hint="当前列表中的条数" />
        <StatCard label="累计点击" :value="totalClicks" hint="已加载链接的点击之和" />
        <StatCard label="正常状态" :value="activeLinks" hint="status = active" />
      </div>

      <!-- 列表 + 搜索 -->
      <Card class="mt-6 p-6 md:p-8">
        <div class="flex flex-wrap items-center justify-between gap-4">
          <p class="title-md">链接列表</p>
          <div class="w-full sm:w-72">
            <Input v-model="query" placeholder="搜索短码 / 标题 / 目标地址" aria-label="搜索链接" />
          </div>
        </div>

        <div class="mt-6">
          <Spinner v-if="loading" :size="18">正在加载…</Spinner>

          <p v-else-if="loadError" class="text-[14px] text-error">{{ loadError }}</p>

          <EmptyState
            v-else-if="links.length === 0"
            :title="query.trim() ? '没有匹配的链接' : '还没有任何链接'"
            :description="
              query.trim()
                ? '换个关键词试试，短码、标题与目标地址都会被搜索。'
                : '用上面的表单创建第一条短链吧。'
            "
          />

          <template v-else>
            <LinkTable :links="links" :pending-code="deletingCode" @delete="handleDelete" />

            <div v-if="hasMore" class="mt-6 flex justify-center">
              <Button variant="secondary" :loading="loadingMore" @click="loadMore">加载更多</Button>
            </div>
          </template>
        </div>
      </Card>
    </div>
  </div>
</template>
