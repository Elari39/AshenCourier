<script setup lang="ts">
/**
 * 内容容器。四种表面模式严格对应 DESIGN.md 的 components：
 *  cream（默认内容卡）· feature（奶油功能卡）· dark（深色产品面板）· coral（珊瑚 callout）
 *
 * 「相邻区块不复用同一 surface 模式」是设计系统的核心节奏，所以这里不做
 * 任意颜色透传 —— 想换表面就换 variant，避免有人随手加第五种底色。
 *
 * `tag` 是唯一的逃生口：卡片样式偶尔要落在语义元素上（列表项 `<li>`）。
 * 只换标签、不换外观 —— 想改外观请改 variant，别拿 tag 做样式变体。
 */
withDefaults(
  defineProps<{
    variant?: 'cream' | 'feature' | 'dark' | 'coral'
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
      'card-coral': variant === 'coral',
    }"
  >
    <slot />
  </component>
</template>
