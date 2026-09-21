<script setup lang="ts">
/**
 * 品牌标记：4 辐放射星。
 *
 * 抽出来的理由不是「好看」而是「一处抄了三遍」：顶栏、footer、空态各有一份
 * 逐字相同的路径数据，颜色还各写一个内联 hex（`fill="#141413"` / `fill="#faf9f5"`
 * / `stroke="#cc785c"`）。DESIGN.md 明写 never inline hex，而 SVG 的 fill/stroke
 * 写在**呈现属性**里根本引不到 var() —— 于是只能靠「谁记得改」。
 *
 * 现在颜色走 currentColor：调用方用 text-ink / text-on-dark / text-primary 决定，
 * 与页面其余部分共用同一套颜色体系，这个组件自己一个色值都不碰。
 */
withDefaults(
  defineProps<{
    /** solid：实心（顶栏与 footer）；outline：描边（空态用的珊瑚细线）。 */
    variant?: 'solid' | 'outline'
  }>(),
  { variant: 'solid' },
)
</script>

<template>
  <svg viewBox="0 0 32 32" aria-hidden="true">
    <path
      d="M16 4l2.1 9.9L28 16l-9.9 2.1L16 28l-2.1-9.9L4 16l9.9-2.1z"
      :fill="variant === 'outline' ? 'none' : 'currentColor'"
      :stroke="variant === 'outline' ? 'currentColor' : 'none'"
      :stroke-width="variant === 'outline' ? 1.6 : 0"
    />
  </svg>
</template>
