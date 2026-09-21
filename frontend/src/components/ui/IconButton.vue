<script setup lang="ts">
/**
 * 圆形图标按钮（DESIGN.md 的 `button-icon-circular`，36×36）。
 *
 * `label` 是**必填** prop，不是可选的：图标按钮没有可见文字，
 * 漏掉可访问名之后它在读屏里就只剩一个光秃秃的「按钮」。
 * 做成必填就能让「忘记写 aria-label」在类型检查阶段就暴露，
 * 而不是等到有人用读屏打开页面才发现。
 */
withDefaults(
  defineProps<{
    /** 可访问名。同时用作 aria-label 与 title（鼠标悬停也能看到它在做什么）。 */
    label: string
    disabled?: boolean
  }>(),
  { disabled: false },
)
</script>

<template>
  <button
    type="button"
    class="btn-icon"
    :aria-label="label"
    :title="label"
    :disabled="disabled"
  >
    <slot />
  </button>
</template>
