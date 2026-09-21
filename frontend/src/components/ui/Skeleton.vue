<script setup lang="ts">
/**
 * 骨架屏。
 *
 * 复用 main.css 里唯一的动画 `.animate-breathe`，而不是新造一个 shimmer：
 * 那条动画已经被 `prefers-reduced-motion` 关掉，新造一个就得记得也关一遍。
 *
 * 尺寸交给调用方（`height` 传 Tailwind 的 h-* 值、`lines` 传行数）：
 * 列表、表格、统计卡需要的高度都不一样，在这里预设几种尺寸反而不如直接透传。
 */
withDefaults(
  defineProps<{
    /** 行数。 */
    lines?: number
    /** 单行高度类，例如 'h-4' / 'h-6'。 */
    height?: string
  }>(),
  { lines: 3, height: 'h-4' },
)
</script>

<template>
  <div class="animate-breathe space-y-3" role="status" aria-label="正在加载">
    <!-- 最后一行收短一点：等宽的一叠灰条看起来像表格错位，不像占位 -->
    <div
      v-for="line in lines"
      :key="line"
      class="rounded-md bg-surface-card"
      :class="[height, lines > 1 && line === lines ? 'w-2/3' : 'w-full']"
    />
  </div>
</template>
