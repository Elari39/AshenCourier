<script setup lang="ts">
/**
 * 404 兜底页。
 *
 * 注意：短码跳转的 404 由后端渲染 HTML（那条路径不经过 SPA），
 * 这个页面只负责「前端路由没匹配上」的情况。
 */
import Button from '@/components/ui/Button.vue'
</script>

<template>
  <div class="bg-canvas py-24 md:py-32">
    <div class="container-page">
      <div class="mx-auto max-w-xl text-center">
        <p class="eyebrow">404 Not found</p>
        <h1 class="display-xl mt-5">这个页面不存在。</h1>
        <p class="mt-6 text-[16px] leading-[1.55] text-body">
          地址可能拼错了，或者这个页面已经被移除。
          如果你是在找一个短链，请确认短码是否正确（区分大小写）。
        </p>
        <div class="mt-8 flex flex-wrap justify-center gap-3">
          <Button :to="{ name: 'landing' }" variant="primary">回首页</Button>
          <Button :to="{ name: 'dashboard' }" variant="secondary">我的链接</Button>
        </div>
      </div>
    </div>
  </div>
</template>
