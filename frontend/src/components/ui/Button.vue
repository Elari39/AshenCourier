<script setup lang="ts">
/**
 * 按钮。三种渲染形态：`to`（RouterLink）/ `href`（外链）/ 原生 button。
 *
 * 交互态只有 default 与 active 两态 —— DESIGN.md 明确不做花哨 hover 动画，
 * 且从未定义 hover 样式。
 */
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import Spinner from './Spinner.vue'

type Variant = 'primary' | 'secondary' | 'secondary-dark' | 'text' | 'danger'

const props = withDefaults(
  defineProps<{
    variant?: Variant
    size?: 'md' | 'sm'
    type?: 'button' | 'submit'
    /** 传入则渲染成 RouterLink。 */
    to?: RouteLocationRaw
    /** 传入则渲染成 <a>。 */
    href?: string
    disabled?: boolean
    loading?: boolean
    /** 撑满父容器宽度。 */
    block?: boolean
  }>(),
  { variant: 'primary', size: 'md', type: 'button' },
)

const VARIANT_CLASS: Record<Variant, string> = {
  primary: 'btn-primary',
  secondary: 'btn-secondary',
  'secondary-dark': 'btn-secondary-dark',
  text: 'btn-text',
  danger: 'btn-danger',
}

const classes = computed(() => [
  'btn',
  VARIANT_CLASS[props.variant],
  props.size === 'sm' ? 'btn-sm' : '',
  props.block ? 'w-full' : '',
])

const inactive = computed(() => props.disabled === true || props.loading === true)
</script>

<template>
  <RouterLink v-if="to && !inactive" :to="to" :class="classes">
    <slot />
  </RouterLink>

  <a v-else-if="href" :href="href" target="_blank" rel="noopener noreferrer" :class="classes">
    <slot />
  </a>

  <button
    v-else
    :type="type"
    :class="classes"
    :disabled="inactive"
    :aria-busy="loading ? 'true' : undefined"
  >
    <Spinner v-if="loading" :size="14" />
    <slot />
  </button>
</template>
