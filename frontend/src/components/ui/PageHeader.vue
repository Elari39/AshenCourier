<script setup lang="ts">
/**
 * 页头：eyebrow + 标题 + 说明，右侧可选操作区。
 *
 * 为什么值得抽出来：这段结构在 5 个视图里各写了一遍。更要紧的是 h1 上挂着一个
 * **跨页面的约定** —— 路由切换后焦点要落到新页面的标题上（见 DefaultLayout），
 * 实现方式是 `tabindex="-1"`。靠每个视图自觉写，迟早漏掉一个，
 * 而漏掉的表现是**静默的**：焦点没移动，界面上什么都看不出来。
 * 收进组件之后，「页面上有一个可聚焦的 h1」变成结构性保证。
 *
 * 两种对齐方式：start（左对齐 + 右侧操作区，应用页用）/ center（居中，登录注册与 404 用）。
 */
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 标题上方那行全大写小字（DESIGN.md 的 caption-uppercase）。 */
    eyebrow: string
    title: string
    size?: 'xl' | 'lg' | 'md'
    align?: 'start' | 'center'
    /**
     * 追加到 h1 上的类。**只给「标题不是普通文案」的例外用** ——
     * 目前只有一处：详情页的标题是一条短码，刻意用等宽字体（DESIGN.md 的衬线大标题
     * 是给自然语言的，短码不是）。
     */
    titleClass?: string
  }>(),
  { size: 'lg', align: 'start', titleClass: '' },
)

const DISPLAY: Record<'xl' | 'lg' | 'md', string> = {
  xl: 'display-xl',
  lg: 'display-lg',
  md: 'display-md',
}

const centered = computed(() => props.align === 'center')
</script>

<template>
  <div :class="centered ? '' : 'flex flex-wrap items-end justify-between gap-6'">
    <div class="min-w-0" :class="centered ? 'text-center' : ''">
      <p class="eyebrow" :class="centered ? 'text-center' : ''">{{ eyebrow }}</p>
      <!-- tabindex="-1"：它是「路由切换后焦点去哪」的落点，见文件头注释 -->
      <h1
        class="mt-3 break-anywhere"
        :class="[DISPLAY[size], titleClass, centered ? 'text-center' : '']"
        tabindex="-1"
      >
        {{ title }}
      </h1>
      <div v-if="$slots.default" class="mt-4 text-[15px] leading-[1.55] text-muted">
        <slot />
      </div>
    </div>

    <div v-if="$slots.actions" class="flex flex-wrap items-center gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
