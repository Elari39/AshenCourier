<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineProps<{ mode: 'login' | 'register' }>()
</script>

<template>
  <section class="account-stage" :class="mode === 'register' ? 'account-stage-register' : ''">
    <div class="container-page">
      <div class="account-shell">
        <aside class="account-poster" :class="mode === 'register' ? 'account-poster-register' : ''">
          <span class="account-edition"
            >ASHENCOURIER / {{ mode === 'login' ? 'WELCOME BACK' : 'GET STARTED' }}</span
          >
          <p class="account-headline">
            {{ mode === 'login' ? '好链接，' : '小链接，' }}<br /><span>{{
              mode === 'login' ? '都在这里。' : '大有可为。'
            }}</span>
          </p>
          <p class="mt-6 max-w-sm text-base leading-relaxed text-ink">
            {{
              mode === 'login'
                ? '回来看看你的分享。管理链接、调整设置，让每一次点击都有迹可循。'
                : '给你的分享一个专属空间。创建账号，把链接和灵感一起收好。'
            }}
          </p>
          <div class="account-benefits">
            <div>
              <span>01</span>
              <p>所有链接，一处管理</p>
            </div>
            <div>
              <span>02</span>
              <p>访问口令，随时掌控</p>
            </div>
            <div>
              <span>03</span>
              <p>点击趋势，清晰可见</p>
            </div>
          </div>
          <div class="account-signoff">
            <span>LESS LINK.<br />MORE POSSIBILITIES.</span><span class="text-5xl" aria-hidden="true">↗</span>
          </div>
        </aside>
        <div class="account-form-panel">
          <nav class="account-tabs" aria-label="账号入口">
            <RouterLink :to="{ name: 'login' }" :aria-current="mode === 'login' ? 'page' : undefined"
              >登录</RouterLink
            >
            <RouterLink :to="{ name: 'register' }" :aria-current="mode === 'register' ? 'page' : undefined"
              >注册</RouterLink
            >
          </nav>
          <div class="account-form-body">
            <p class="account-form-kicker">
              {{ mode === 'login' ? 'YOUR NEXT CLICK STARTS HERE' : 'MAKE ROOM FOR YOUR LINKS' }}
            </p>
            <h1 class="display-lg mt-3" tabindex="-1">
              {{ mode === 'login' ? '登录' : '创建账号' }}
            </h1>
            <p class="mt-3 mb-7 text-sm leading-relaxed text-muted">
              {{
                mode === 'login'
                  ? '输入邮箱和密码，继续管理你的短链。'
                  : '填写下面的信息，开始你的第一次分享。'
              }}
            </p>
            <slot />
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
