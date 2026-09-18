<script setup lang="ts">
/**
 * 文本输入框：自带 label、错误提示与无障碍属性绑定。
 *
 * 用 defineModel 承接 v-model；`error` 只影响展示，校验逻辑留在调用方。
 * 错误态只用 DESIGN.md 的 error 色描边 + 一行说明，不加图标堆砌。
 */
import { computed, useId } from 'vue'

const props = withDefaults(
  defineProps<{
    label?: string
    type?: string
    placeholder?: string
    /** 校验错误信息；有值时输入框转为错误态。 */
    error?: string
    /** 辅助说明；与 error 互斥，error 优先。 */
    hint?: string
    disabled?: boolean
    autocomplete?: string
    /** 前置等宽前缀文案，例如 "ashen.cc/"。 */
    prefix?: string
    maxlength?: number
  }>(),
  { type: 'text' },
)

const model = defineModel<string>({ default: '' })

// useId 保证同一页面上多个输入框的 label/描述关联不串台
const id = useId()
const descId = computed(() => `${id}-desc`)
const hasDesc = computed(() => Boolean(props.error || props.hint))
</script>

<template>
  <div>
    <label v-if="label" :for="id" class="field-label">{{ label }}</label>

    <div class="relative">
      <span
        v-if="prefix"
        class="pointer-events-none absolute inset-y-0 left-3.5 flex items-center font-mono text-[13px] text-muted"
      >
        {{ prefix }}
      </span>
      <input
        :id="id"
        v-model="model"
        :type="type"
        :placeholder="placeholder"
        :disabled="disabled"
        :autocomplete="autocomplete"
        :maxlength="maxlength"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="hasDesc ? descId : undefined"
        class="text-input"
        :class="[prefix ? 'pl-16' : '', error ? 'border-error' : '']"
      />
    </div>

    <p v-if="error" :id="descId" class="mt-1.5 text-[13px] text-error">{{ error }}</p>
    <p v-else-if="hint" :id="descId" class="mt-1.5 text-[13px] text-muted">{{ hint }}</p>
  </div>
</template>
