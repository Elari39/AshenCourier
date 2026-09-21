<script setup lang="ts">
/**
 * 小徽章。四种形态：pill（默认奶油）/ coral（强调，全大写字距 1.5px）/
 * quiet（更弱）/ error（错误态，例如「已删除」）。
 *
 * variant 刻意做成**语义**命名而不是颜色命名：调用方给的是「这条状态有多强」，
 * 具体底色由 main.css 决定。表格里要换配色时只动 CSS，不用改每个调用点。
 */
withDefaults(
  defineProps<{
    variant?: 'pill' | 'coral' | 'quiet' | 'error'
  }>(),
  { variant: 'pill' },
)
</script>

<template>
  <span
    class="badge"
    :class="{
      'badge-coral': variant === 'coral',
      'badge-quiet': variant === 'quiet',
      'badge-error': variant === 'error',
    }"
  >
    <slot />
  </span>
</template>
