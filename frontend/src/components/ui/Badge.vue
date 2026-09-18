<script setup lang="ts">
/**
 * 小徽章。三种形态：pill（默认奶油）/ coral（强调，全大写字距 1.5px）/ quiet（更弱）。
 */
withDefaults(
  defineProps<{
    variant?: 'pill' | 'coral' | 'quiet'
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
    }"
  >
    <slot />
  </span>
</template>
