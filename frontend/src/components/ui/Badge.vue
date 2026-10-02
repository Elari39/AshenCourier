<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'pill' | 'accent' | 'quiet' | 'error'
  }>(),
  { variant: 'pill' },
)
</script>

<template>
  <span
    class="badge"
    :class="{
      'badge-accent': variant === 'accent',
      'badge-quiet': variant === 'quiet',
      'badge-error': variant === 'error',
    }"
  >
    <slot />
  </span>
</template>
