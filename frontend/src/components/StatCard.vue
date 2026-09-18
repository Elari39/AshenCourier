<script setup lang="ts">
/**
 * 指标卡：小写标签 + 衬线大数字。
 *
 * 数字用衬线字体（DESIGN.md 的 takeaway：「当你想强调时，先放大衬线，而不是加粗」）。
 */
import { computed } from 'vue'

import { formatNumber } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    label: string
    value: number | string
    /** 副标题 / 口径说明。 */
    hint?: string
    /** 深色面板上使用。 */
    onDark?: boolean
  }>(),
  { onDark: false },
)

const displayValue = computed(() =>
  typeof props.value === 'number' ? formatNumber(props.value) : props.value,
)
</script>

<template>
  <div
    class="rounded-lg px-6 py-5"
    :class="onDark ? 'bg-surface-dark-elevated' : 'bg-surface-card'"
  >
    <p class="eyebrow" :class="onDark ? 'text-on-dark-soft' : ''">{{ label }}</p>
    <p
      class="mt-2 font-display text-[36px] leading-[1.1] tracking-[-0.014em]"
      :class="onDark ? 'text-on-dark' : 'text-ink'"
    >
      {{ displayValue }}
    </p>
    <p v-if="hint" class="mt-1 text-[13px]" :class="onDark ? 'text-on-dark-soft' : 'text-muted'">
      {{ hint }}
    </p>
  </div>
</template>
