<script setup lang="ts">
/**
 * 内容容器。四种表面模式严格对应 DESIGN.md 的 components：
 *  cream（默认内容卡）· feature（奶油功能卡）· dark（深色产品面板）· coral（珊瑚 callout）
 *
 * 「相邻区块不复用同一 surface 模式」是设计系统的核心节奏，所以这里不做
 * 任意颜色透传 —— 想换表面就换 variant，避免有人随手加第五种底色。
 */
withDefaults(
  defineProps<{
    variant?: 'cream' | 'feature' | 'dark' | 'coral'
  }>(),
  { variant: 'cream' },
)
</script>

<template>
  <div
    :class="{
      'card-cream': variant === 'cream',
      'card-feature': variant === 'feature',
      'card-dark': variant === 'dark',
      'card-coral': variant === 'coral',
    }"
  >
    <slot />
  </div>
</template>
