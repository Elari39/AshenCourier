<script setup lang="ts">
/**
 * 空态。MVP 也不允许出现「白屏 + 无反馈」，
 * 所以每个列表/图表都必须在无数据时渲染这个组件。
 */
withDefaults(
  defineProps<{
    title: string
    description?: string
    /** 深色面板上的空态需要反相的文字色。 */
    onDark?: boolean
  }>(),
  { onDark: false },
)
</script>

<template>
  <div class="flex flex-col items-center px-6 py-14 text-center">
    <!-- 品牌标记：4 辐放射星，纯装饰 -->
    <svg class="mb-5 h-7 w-7" viewBox="0 0 32 32" aria-hidden="true">
      <path
        d="M16 4l2.1 9.9L28 16l-9.9 2.1L16 28l-2.1-9.9L4 16l9.9-2.1z"
        fill="none"
        stroke="#cc785c"
        stroke-width="1.6"
      />
    </svg>

    <p class="title-md" :class="onDark ? 'text-on-dark' : ''">{{ title }}</p>
    <p v-if="description" class="mt-2 max-w-sm text-[14px]" :class="onDark ? 'text-on-dark-soft' : 'text-muted'">
      {{ description }}
    </p>

    <div v-if="$slots.default" class="mt-6">
      <slot />
    </div>
  </div>
</template>
