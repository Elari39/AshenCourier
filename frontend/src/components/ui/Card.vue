<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'cream' | 'feature' | 'dark' | 'accent'
    /** 渲染成哪个标签。默认 div；列表里用 li，保持语义正确。 */
    tag?: string
  }>(),
  { variant: 'cream', tag: 'div' },
)
</script>

<template>
  <component
    :is="tag"
    :class="{
      'card-cream': variant === 'cream',
      'card-feature': variant === 'feature',
      'card-dark': variant === 'dark',
      'card-accent': variant === 'accent',
    }"
  >
    <slot />
  </component>
</template>
