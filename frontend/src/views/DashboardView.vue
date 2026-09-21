<script setup lang="ts">
/**
 * 我的链接（需要登录）。
 *
 * 数据源只有 `GET /api/links` 一个接口：顶部三张指标卡的数字直接从当前
 * 已加载的列表算出来 —— 不再为此新增一个「账号级汇总」接口，
 * MVP 阶段多一个接口就多一份要维护的契约。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'

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
import PageHeader from '@/components/ui/PageHeader.vue'
import Skeleton from '@/components/ui/Skeleton.vue'
import { useAuth } from '@/composables/useAuth'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'
import { createRequestGuard, isAbortError } from '@/utils/request'

const PAGE_SIZE = 20

const toast = useToast()
const { confirm } = useConfirm()
const { refreshMe } = useAuth()

const links = ref<Link[]>([])
const nextCursor = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')
const query = ref('')
const tagFilter = ref('')
const deletingCode = ref('')
const latest = ref<{ link: Link; manageKey?: string } | null>(null)

/** 指标卡口径：都基于「已加载的列表」，界面上会写清楚。 */
const totalLinks = computed(() => links.value.length)
const totalClicks = computed(() => links.value.reduce((sum, link) => sum + link.click_count, 0))
const activeLinks = computed(() => links.value.filter((link) => link.status === 'active').length)

const hasMore = computed(() => nextCursor.value !== '')

/** 列表为空时区分三种情况：没有链接 / 搜不到 / 标签筛不到。 */
const hasFilter = computed(() => query.value.trim() !== '' || tagFilter.value.trim() !== '')
const emptyTitle = computed(() => (hasFilter.value ? '没有匹配的链接' : '还没有任何链接'))
const emptyDescription = computed(() => {
  if (tagFilter.value.trim()) {
    return `没有带「${tagFilter.value.trim()}」标签的链接。清空标签筛选可以看到全部。`
  }
  if (query.value.trim()) {
    return '换个关键词试试，短码、标题与目标地址都会被搜索。'
  }
  return '用上面的表单创建第一条短链吧。'
})

/**
 * 列表的并发守卫：reload 与 loadMore **共用一个**。
 *
 * 共用一个是刻意的 —— 两者写的是同一份 `links`：搜索条件变了之后的 reload 必须
 * 能取消在途的 loadMore，否则「翻页的第二页」会追加到「新查询的第一页」后面，
 * 同一个列表里混进两组不同查询的结果。分开用两个守卫就挡不住这种情况。
 *
 * 原来这里只有 300ms 防抖，那只能压住「重复发送」，取消不了已经发出去的请求。
 */
const listGuard = createRequestGuard()

/** 首次加载 / 搜索变化时重置列表。 */
async function reload(): Promise<void> {
  const { signal, isStale } = listGuard.begin()
  loading.value = true
  loadError.value = ''
  // ⚠️ 必须在这里把 loadMore 的 loading 复位。
  //
  // begin() 会把在飞的 loadMore 判成 stale（这是共用守卫的目的：新查询必须能取消
  // 旧的翻页），于是 loadMore 的 `finally { if (!isStale()) ... }` 不会执行 ——
  // 而它那句正是唯一复位 loadingMore 的地方。漏了这一步的效果是：
  // loadingMore 永久为 true，而 loadMore() 开头的守卫直接 return，
  // 「加载更多」按钮一直转圈且再也点不动，只能刷新页面。
  //
  // 这里复位不会掐掉别人的 loading：reload 与 loadMore 抢的是同一份列表，
  // 被取消的那一轮已经是最后一个 loadMore，不存在「更新的一轮」需要保护。
  loadingMore.value = false
  try {
    const result = await linksApi.list(
      {
        limit: PAGE_SIZE,
        q: query.value.trim() || undefined,
        tag: tagFilter.value.trim() || undefined,
      },
      null,
      signal,
    )
    if (isStale()) return
    links.value = result.links
    nextCursor.value = result.next_cursor ?? ''
  } catch (cause) {
    if (isStale() || isAbortError(cause)) return
    loadError.value = cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试'
  } finally {
    if (!isStale()) loading.value = false
  }
}

/** 追加下一页。 */
async function loadMore(): Promise<void> {
  if (!hasMore.value || loadingMore.value) return

  const { signal, isStale } = listGuard.begin()
  loadingMore.value = true
  try {
    const result = await linksApi.list(
      {
        limit: PAGE_SIZE,
        cursor: nextCursor.value,
        q: query.value.trim() || undefined,
        tag: tagFilter.value.trim() || undefined,
      },
      null,
      signal,
    )
    if (isStale()) return
    links.value = [...links.value, ...result.links]
    nextCursor.value = result.next_cursor ?? ''
  } catch (cause) {
    if (isStale() || isAbortError(cause)) return
    toast.error(cause instanceof ApiError ? cause.friendly : '加载失败，请稍后重试')
  } finally {
    if (!isStale()) loadingMore.value = false
  }
}

/** 删除一条（软删除）。 */
async function handleDelete(code: string): Promise<void> {
  // 说清楚「短码不会再被复用」是必要的：用户以为删了就能把码腾出来，
  // 是最容易产生误解的一点（见 README「已知限制」）。
  const ok = await confirm({
    title: '删除短链',
    message: `确定要删除 /${code} 吗？删除后短链立即失效，这个短码也不会再复用。`,
    confirmText: '删除',
    variant: 'danger',
  })
  if (!ok) return

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

// 搜索 / 标签筛选：300ms 防抖，避免每敲一个字就打一次接口
let searchTimer: number | undefined
watch([query, tagFilter], () => {
  window.clearTimeout(searchTimer)
  searchTimer = window.setTimeout(() => void reload(), 300)
})

onMounted(async () => {
  // 并行：刷新用户资料（令牌可能已过期）+ 拉列表。令牌失效时 401 处理器会接管跳转
  void refreshMe().catch(() => undefined)
  await reload()
})

// 离开页面时：清掉防抖定时器（否则它会在组件销毁后触发一次 reload），
// 并取消在飞的列表请求。
onUnmounted(() => {
  window.clearTimeout(searchTimer)
  listGuard.cancel()
})
</script>

<template>
  <div class="bg-canvas py-12 md:py-16">
    <div class="container-page">
      <!-- 页头 -->
      <PageHeader eyebrow="Dashboard" title="我的链接">
        <template #actions>
          <p class="text-[13px] text-muted">共加载 {{ totalLinks }} 条</p>
        </template>
      </PageHeader>

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
          <div class="flex w-full flex-col gap-3 sm:w-auto sm:flex-row">
            <div class="w-full sm:w-40">
              <Input
                v-model="tagFilter"
                placeholder="按标签筛选"
                aria-label="按标签筛选"
              />
            </div>
            <div class="w-full sm:w-64">
              <Input v-model="query" placeholder="搜索短码 / 标题 / 目标地址" aria-label="搜索链接" />
            </div>
          </div>
        </div>

        <div class="mt-6">
          <Skeleton v-if="loading" :lines="4" height="h-14" />

          <p v-else-if="loadError" class="text-[14px] text-error">{{ loadError }}</p>

          <EmptyState
            v-else-if="links.length === 0"
            :title="emptyTitle"
            :description="emptyDescription"
          />

          <template v-else>
            <LinkTable
              :links="links"
              :pending-code="deletingCode"
              @delete="handleDelete"
              @filter-tag="(tag) => (tagFilter = tag)"
            />

            <div v-if="hasMore" class="mt-6 flex justify-center">
              <Button variant="secondary" :loading="loadingMore" @click="loadMore">加载更多</Button>
            </div>
          </template>
        </div>
      </Card>
    </div>
  </div>
</template>
