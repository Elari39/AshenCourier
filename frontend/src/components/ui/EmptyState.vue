<script setup lang="ts">
/**
 * 空态。MVP 也不允许出现「白屏 + 无反馈」，
 * 所以每个列表/图表都必须在无数据时渲染这个组件。
 */
import BrandMark from './BrandMark.vue'

defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <div class="flex flex-col items-center px-6 py-14 text-center">
    <!-- 品牌标记：4 辐放射星，纯装饰。颜色取自 text-primary（珊瑚描边）-->
    <BrandMark variant="outline" class="mb-5 h-7 w-7 text-primary" />

    <p class="title-md">{{ title }}</p>
    <p v-if="description" class="mt-2 max-w-sm text-[14px] text-muted">
      {{ description }}
    </p>

    <div v-if="$slots.default" class="mt-6">
      <slot />
    </div>
  </div>
</template>
