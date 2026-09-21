<script setup lang="ts">
/**
 * 404 兜底页。
 *
 * 注意：短码跳转的 404 由后端渲染 HTML（那条路径不经过 SPA），
 * 这个页面只负责「前端路由没匹配上」的情况。
 */
import Button from '@/components/ui/Button.vue'
import PageHeader from '@/components/ui/PageHeader.vue'
</script>

<template>
  <div class="bg-canvas py-24 md:py-32">
    <div class="container-page">
      <div class="mx-auto max-w-xl text-center">
        <PageHeader eyebrow="404 Not found" title="这个页面不存在。" size="xl" align="center">
          地址可能拼错了，或者这个页面已经被移除。
          如果你是在找一个短链，请确认短码是否正确（区分大小写）。
          <template #actions>
            <Button :to="{ name: 'landing' }" variant="primary">回首页</Button>
            <Button :to="{ name: 'dashboard' }" variant="secondary">我的链接</Button>
          </template>
        </PageHeader>
      </div>
    </div>
  </div>
</template>
