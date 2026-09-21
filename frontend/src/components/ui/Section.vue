<script setup lang="ts">
/**
 * 内容区块：奶油卡片 + eyebrow 小标题 + 右侧操作区 + 正文。
 *
 * eyebrow 用 `<h2>` 而不是 `<p>`：这些区块在页面上确实是「趋势 / 分布 / 最近点击」
 * 这一层的标题。原来写成 `<p>`，文档大纲里 h1 之后就什么都没有了 ——
 * 读屏用户按标题跳转时会发现整页只有一个标题，只能从头听。
 *
 * 操作区固定在**标题行右侧**（而不是跟着正文走）：详情页那几个区块的操作都是
 * 「作用于整个区块」的（切统计窗口、看已加载条数），摆在标题旁边才对得上。
 */
import Card from './Card.vue'

defineProps<{
  eyebrow: string
}>()
</script>

<template>
  <Card class="mt-6 p-6 md:p-8">
    <div class="flex flex-wrap items-center justify-between gap-4">
      <h2 class="eyebrow">{{ eyebrow }}</h2>
      <div v-if="$slots.actions" class="flex flex-wrap items-center gap-3">
        <slot name="actions" />
      </div>
    </div>

    <p v-if="$slots.description" class="mt-2 text-[13px] text-muted">
      <slot name="description" />
    </p>

    <div class="mt-5">
      <slot />
    </div>
  </Card>
</template>
