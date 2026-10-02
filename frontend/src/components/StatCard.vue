<script setup lang="ts">
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
  <div class="stat-tile card-cream px-6 py-5">
    <p class="eyebrow">{{ label }}</p>
    <p class="mt-2 font-display font-extrabold text-[48px] leading-[1.1] tracking-[-0.014em] text-ink">
      {{ displayValue }}
    </p>
    <p v-if="hint" class="mt-1 text-[13px] text-muted">{{ hint }}</p>
  </div>
</template>
