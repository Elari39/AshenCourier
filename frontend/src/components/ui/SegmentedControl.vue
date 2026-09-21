<script setup lang="ts" generic="T extends string | number">
/**
 * 分段控件：一组互斥的切换按钮（统计窗口的 7/30/90 天就是它）。
 *
 * 为什么用 `role="group"` + `aria-pressed` 而不是 `role="radiogroup"`：
 * 后者的键盘约定是「方向键在选项间移动、整组只占一个 Tab 停留点」，
 * 那要自己实现 roving tabindex 并接管键盘事件；而这里的语义就是
 * 「一组可切换的筛选按钮」，`aria-pressed` 表达得更直白，
 * 且每个按钮都能 Tab 到 —— 与浏览器原生行为一致，不用写任何焦点管理代码。
 *
 * 配色取自 DESIGN.md 的 category-tab / category-tab-active：
 * 未选中透明底 + muted 文字，选中 surface-card 底 + ink 文字。
 * 样式集中在 main.css 的 `.segmented*`，且选中态直接绑在 `[aria-pressed='true']` 上 ——
 * 这样「视觉上的选中」与「语义上的选中」共用同一个来源，不可能再各说各话。
 */
interface Option {
  value: T
  label: string
}

withDefaults(
  defineProps<{
    options: Option[]
    /**
     * 整组控件的**可访问名**，会写进 `aria-label`。
     *
     * 刻意不叫 `ariaLabel`：`aria-*` 是 HTML 的保留属性名，Vue 的类型里
     * 已经存在 `aria-label`，用它反而**绑不到这个 prop**（vue-tsc 会报
     * 「缺少 ariaLabel」而模板看着没错）。叫 `label` 与 IconButton 的
     * 「label 就是可访问名」保持一致。
     */
    label: string
    disabled?: boolean
  }>(),
  { disabled: false },
)

const model = defineModel<T>({ required: true })
</script>

<template>
  <div class="segmented" role="group" :aria-label="label">
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      class="segmented-item"
      :disabled="disabled"
      :aria-pressed="model === option.value ? 'true' : 'false'"
      @click="model = option.value"
    >
      {{ option.label }}
    </button>
  </div>
</template>
