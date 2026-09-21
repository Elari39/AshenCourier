<script setup lang="ts">
/**
 * 指标卡：小写标签 + 衬线大数字。
 *
 * 数字用衬线字体（DESIGN.md 的 takeaway：「当你想强调时，先放大衬线，而不是加粗」）。
 */
import { computed } from 'vue'

import { formatNumber } from '@/utils/format'

const props = defineProps<{
  label: string
  value: number | string
  /** 副标题 / 口径说明。 */
  hint?: string
}>()

const displayValue = computed(() =>
  typeof props.value === 'number' ? formatNumber(props.value) : props.value,
)
</script>

<template>
  <div class="rounded-lg bg-surface-card px-6 py-5">
    <p class="eyebrow">{{ label }}</p>
    <p class="mt-2 font-display text-[36px] leading-[1.1] tracking-[-0.014em] text-ink">
      {{ displayValue }}
    </p>
    <p v-if="hint" class="mt-1 text-[13px] text-muted">{{ hint }}</p>
  </div>
</template>
