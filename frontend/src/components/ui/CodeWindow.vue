<script setup lang="ts">
/**
 * 深色代码窗。
 *
 * 两种形态，共用同一个内层：
 *  - 不给 `label`：就是 `.code-window-inner` 那一层内嵌面板（结果卡里承载短链、
 *    落地页里展示 curl 示例）；
 *  - 给 `label`：外面再套一层 `.code-window`，画上三个圆点与右上角标签
 *    （落地页那个「短链预览」窗），`#footer` 插槽用来放窗外的补充信息。
 *
 * 抽它的理由：`.code-window-inner` 这层在三个地方各写了一遍，而它承载的是一条
 * 设计约定（深色面板就是产品 chrome，DESIGN.md 的 surface 节奏）——
 * 哪天它要改（比如换圆角），散着写就得挨个找。
 */
defineProps<{
  /** 顶栏标签。给了才渲染带圆点的外框。 */
  label?: string
}>()
</script>

<template>
  <!-- 单一根节点：调用方会往它身上挂 class（`mt-5 flex …`），多根节点时
       Vue 不会做属性透传，那些布局类会静默丢掉 -->
  <div :class="label ? 'code-window' : 'code-window-inner'">
    <template v-if="label">
      <div class="flex items-center gap-2 pb-4">
        <span v-for="dot in 3" :key="dot" class="h-2.5 w-2.5 rounded-full bg-surface-dark-elevated" />
        <span class="ml-2 text-[12px] text-on-dark-soft">{{ label }}</span>
      </div>

      <div class="code-window-inner">
        <slot />
      </div>

      <slot name="footer" />
    </template>

    <slot v-else />
  </div>
</template>
