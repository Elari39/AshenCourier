<script setup lang="ts">
/**
 * 落地页。
 *
 * 表面节奏（DESIGN.md 的核心节奏，相邻区块绝不复用同一 surface）：
 *   奶油 hero-band → 奶油卡片 features → 深色 mockup → 珊瑚 callout
 * （深色 footer 由 DefaultLayout 收尾）
 */
import { ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { Link } from '@/api/types'
import ResultCard from '@/components/ResultCard.vue'
import ShortenForm from '@/components/ShortenForm.vue'
import Button from '@/components/ui/Button.vue'
import { useAuth } from '@/composables/useAuth'
import { shortBase } from '@/utils/shortlink'

const { isAuthenticated } = useAuth()

/**
 * 演示用短链：域名取自 VITE_SHORT_BASE_URL，未配置时回退同源（拼出 `/7Kd2pQ`）。
 *
 * ⚠️ 真实链接一律展示后端返回的 `short_url` —— 链接可能挂在自定义域名下，
 *    前端拿域名自己拼是拼不出来的。这里只是「还没有任何链接」时的静态示意。
 */
const demoShortLink = `${shortBase()}/7Kd2pQ`

/** 最近一次创建的结果；有值就把右栏的静态 mockup 换成真实结果卡。 */
const latest = ref<{ link: Link; manageKey?: string } | null>(null)

function onCreated(payload: { link: Link; manageKey?: string }): void {
  latest.value = payload
}

/** 三条能力说明 —— 都对应真实实现，不写营销话术。 */
const features = [
  {
    title: '跳转不落库',
    body: 'GET /{code} 全程只读 Redis：命中缓存直接 302，未命中才回源一次 PostgreSQL。写统计走 Redis 增量 + 异步队列，不阻塞跳转。',
  },
  {
    title: '匿名可用，也能认领',
    body: '不注册就能生成短链，返回一次性管理密钥；登录后带上密钥即可把匿名链接认领到自己账号下集中管理。',
  },
  {
    title: '点击统计跟手',
    body: '总点击 = 数据库基线 + 待同步增量，worker 每 2 秒回刷，界面数字不会「卡住」；趋势 / 来源 / 设备三维分布都有。',
  },
]
</script>

<template>
  <!-- ---------- ① hero-band：奶油画布 + 6/6 栅格 ---------- -->
  <section class="bg-canvas py-16 md:py-24">
    <div class="container-page grid items-start gap-12 lg:grid-cols-2 lg:gap-16">
      <!-- 左：衬线大标题 + 创建表单 -->
      <div>
        <span class="badge badge-coral">Short Link</span>
        <h1 class="display-xl mt-6">把长链接，<br />收成一条短链。</h1>
        <p class="mt-6 max-w-lg text-[16px] leading-[1.55] text-body">
          粘贴任意 http/https 链接，立刻拿到短链。无需注册；登录后可集中管理所有链接，
          并查看按天趋势、来源与设备分布。
        </p>

        <div class="mt-8">
          <ShortenForm @created="onCreated" />
        </div>

        <div class="mt-6 flex flex-wrap items-center gap-x-6 gap-y-2 text-[13px] text-muted">
          <span>只允许 http / https</span>
          <span>短码 3.5 万亿种组合</span>
          <span>302 跳转不缓存</span>
        </div>
      </div>

      <!-- 右：结果卡（有结果）或深色 mockup（无结果） -->
      <div class="lg:pt-14">
        <ResultCard v-if="latest" :link="latest.link" :manage-key="latest.manageKey" />

        <div v-else class="code-window">
          <div class="flex items-center gap-2 pb-4">
            <span class="h-2.5 w-2.5 rounded-full bg-surface-dark-elevated" />
            <span class="h-2.5 w-2.5 rounded-full bg-surface-dark-elevated" />
            <span class="h-2.5 w-2.5 rounded-full bg-surface-dark-elevated" />
            <span class="ml-2 text-[12px] text-on-dark-soft">ashen-courier — 短链预览</span>
          </div>

          <div class="code-window-inner">
            <p class="text-on-dark-soft">$ ashen shorten https://example.com/2026/09/a-very-long-article</p>
            <p class="mt-3 text-on-dark">
              <span class="text-primary">✓</span> 短链已生成
            </p>
            <p class="mt-1 text-on-dark">{{ demoShortLink }}</p>
            <p class="mt-1 text-on-dark-soft">→ 302 Location: https://example.com/2026/09/a-very-long-article</p>
          </div>

          <div class="mt-5 space-y-2.5 text-[13px]">
            <div class="flex items-center justify-between">
              <span class="text-on-dark-soft">总点击</span>
              <span class="font-mono text-on-dark">1,284</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-on-dark-soft">近 30 天</span>
              <span class="font-mono text-on-dark">342</span>
            </div>
            <div class="flex items-center justify-between">
              <span class="text-on-dark-soft">缓存命中</span>
              <span class="font-mono text-on-dark">99.2%</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>

  <!-- ---------- ② 奶油功能卡 3-up ---------- -->
  <section class="bg-canvas pb-16 md:pb-24">
    <div class="container-page grid gap-6 md:grid-cols-3">
      <article v-for="feature in features" :key="feature.title" class="card-feature">
        <h2 class="title-md">{{ feature.title }}</h2>
        <p class="mt-3 text-[15px] leading-[1.6] text-body">{{ feature.body }}</p>
      </article>
    </div>
  </section>

  <!-- ---------- ③ 深色 mockup：展示真实接口 ---------- -->
  <section class="bg-canvas pb-16 md:pb-24">
    <div class="container-page">
      <div class="card-dark grid gap-10 lg:grid-cols-2">
        <div>
          <p class="eyebrow text-on-dark-soft">REST API</p>
          <h2 class="display-md mt-4 text-on-dark">一行 curl，就是一条短链。</h2>
          <p class="mt-5 text-[15px] leading-[1.6] text-on-dark-soft">
            整个服务只有 11 个接口，覆盖创建、跳转、管理、统计与认领。
            请求与响应都是 <span class="font-mono text-on-dark">application/json</span>，
            错误体统一为
            <span class="font-mono text-on-dark">{ error: { code, message, request_id } }</span>，
            报障时把 request_id 发过来就能直接定位日志。
          </p>
          <div class="mt-8 flex flex-wrap gap-3">
            <Button :to="{ name: isAuthenticated ? 'dashboard' : 'register' }" variant="primary">
              {{ isAuthenticated ? '进入控制台' : '免费注册' }}
            </Button>
            <RouterLink :to="{ name: 'login' }" class="btn btn-secondary-dark">已有账号，登录</RouterLink>
          </div>
        </div>

        <div class="code-window-inner self-start text-[13px]">
          <p class="text-on-dark-soft"># 创建（匿名也会返回一次性 manage_key）</p>
          <p class="mt-2 text-on-dark">$ curl -X POST /api/links \</p>
          <p class="pl-4 text-on-dark">-H 'Content-Type: application/json' \</p>
          <p class="pl-4 text-on-dark">-d '{"target_url":"https://example.com/x"}'</p>

          <p class="mt-4 text-on-dark-soft"># 跳转（只读 Redis，不写库）</p>
          <p class="mt-2 text-on-dark">$ curl -i /7Kd2pQ</p>
          <p class="pl-4 text-on-dark-soft">HTTP/1.1 302 Found</p>
          <p class="pl-4 text-on-dark-soft">Location: https://example.com/x</p>

          <p class="mt-4 text-on-dark-soft"># 统计</p>
          <p class="mt-2 text-on-dark">$ curl /api/links/7Kd2pQ/stats?days=30 \</p>
          <p class="pl-4 text-on-dark">-H 'X-Manage-Key: kQ8…'</p>
        </div>
      </div>
    </div>
  </section>

  <!-- ---------- ④ 珊瑚 callout band ---------- -->
  <section class="bg-canvas pb-16 md:pb-24">
    <div class="container-page">
      <div class="card-coral flex flex-col items-start justify-between gap-6 md:flex-row md:items-center">
        <div>
          <h2 class="display-sm text-on-primary">准备好把你的第一条长链接收短了吗？</h2>
          <p class="mt-3 max-w-xl text-[15px] text-on-primary/85">
            匿名就能用，不需要信用卡，也不需要邮箱。
          </p>
        </div>
        <RouterLink
          :to="{ name: isAuthenticated ? 'dashboard' : 'register' }"
          class="btn btn-secondary shrink-0"
        >
          {{ isAuthenticated ? '进入控制台' : '创建第一条短链' }}
        </RouterLink>
      </div>
    </div>
  </section>
</template>
