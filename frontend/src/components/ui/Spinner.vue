<script setup lang="ts">
/**
 * 加载指示器。只用 currentColor，因此放在深色面板上也不用改样式。
 */
withDefaults(
  defineProps<{
    /** 图标边长（px）。 */
    size?: number
    /** 无障碍标签。 */
    label?: string
  }>(),
  { size: 16, label: '加载中' },
)
</script>

<template>
  <span class="inline-flex items-center gap-2 text-muted" role="status" :aria-label="label">
    <svg
      :width="size"
      :height="size"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
      class="animate-spin"
    >
      <circle cx="12" cy="12" r="9" stroke="currentColor" stroke-opacity="0.25" stroke-width="3" />
      <path
        d="M21 12a9 9 0 0 0-9-9"
        stroke="currentColor"
        stroke-width="3"
        stroke-linecap="round"
      />
    </svg>
    <span v-if="$slots.default" class="text-[13px]"><slot /></span>
  </span>
</template>
