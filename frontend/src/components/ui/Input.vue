<script setup lang="ts">
/**
 * 文本输入框：自带 label、错误提示与无障碍属性绑定。
 *
 * 用 defineModel 承接 v-model；`error` 只影响展示，校验逻辑留在调用方。
 * 错误态只用 DESIGN.md 的 error 色描边 + 一行说明，不加图标堆砌。
 *
 * 关于 $attrs：组件根节点是包裹用的 <div>，而真正需要 aria-* / data-* 的是里面的
 * <input>。默认的「属性透传到根节点」会把 `aria-label` 挂到 div 上 —— 输入框本身
 * 反而没有可访问名（屏幕阅读器只会念「编辑框」）。所以这里关掉自动透传，
 * 手动把 class/style 留给外层（布局相关的类，如 ShortenForm 的 flex-1），
 * 其余属性全部绑到 <input> 上。
 */
import { computed, useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })

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

const attrs = useAttrs()
/** 布局类留给外层容器（调用方写的 class / style 是给外层排版的）。 */
const wrapperAttrs = computed(() => ({ class: attrs.class, style: attrs.style }))
/** 其余属性（aria-*、data-*、autocomplete 等）绑到真正的 <input> 上。 */
const inputAttrs = computed(() => {
  // 用 delete 而不是解构忽略：本仓库的 eslint 配置不允许出现未使用的绑定
  const rest: Record<string, unknown> = { ...attrs }
  delete rest.class
  delete rest.style
  return rest
})
</script>

<template>
  <div v-bind="wrapperAttrs">
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
        v-bind="inputAttrs"
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
