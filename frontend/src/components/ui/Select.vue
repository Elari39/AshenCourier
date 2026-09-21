<script setup lang="ts" generic="T extends string">
/**
 * 下拉选择：与 Input 共用同一套 label / error / hint 与无障碍绑定，
 * 只是把 `<input>` 换成 `<select>`。
 *
 * 三个刻意的选择：
 *
 * 1. **泛型化取值**。修改面板的状态字段类型是 `'active' | 'disabled'` 这样的窄联合，
 *    如果这里只收 `string`，`v-model` 绑过去就会因为「string 不能赋给窄联合」而报错，
 *    只能靠调用方转型绕过 —— 那等于把类型安全让给了 cast。
 * 2. **自绘箭头而不是用原生箭头**。原生箭头由浏览器绘制、颜色不受任何 token 控制，
 *    在奶油画布上是一块突兀的系统灰；这里用 currentColor 画一个，
 *    深色面板上也能跟着走，不需要为它再造一个 token。
 * 3. **关掉属性自动透传**，与 Input 同理：根节点是包裹用的 `<div>`，
 *    而 `aria-*` 必须落到真正的 `<select>` 上，否则控件本身没有可访问名
 *    （读屏只会念出「组合框」）。
 */
import { computed, useAttrs, useId } from 'vue'

defineOptions({ inheritAttrs: false })

interface Option {
  value: string
  label: string
}

const props = withDefaults(
  defineProps<{
    options: Option[]
    label?: string
    /** 校验错误信息；有值时控件转为错误态。 */
    error?: string
    /** 辅助说明；与 error 互斥，error 优先。 */
    hint?: string
    disabled?: boolean
  }>(),
  { disabled: false },
)

const model = defineModel<T>({ required: true })

const id = useId()
const descId = computed(() => `${id}-desc`)
const hasDesc = computed(() => Boolean(props.error || props.hint))

const attrs = useAttrs()
/** 布局类留给外层容器（调用方写的 class / style 是给外层排版的）。 */
const wrapperAttrs = computed(() => ({ class: attrs.class, style: attrs.style }))
/** 其余属性（aria-*、data-* 等）绑到真正的 <select> 上。 */
const selectAttrs = computed(() => {
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
      <select
        :id="id"
        v-model="model"
        v-bind="selectAttrs"
        :disabled="disabled"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="hasDesc ? descId : undefined"
        class="select-input"
        :class="error ? 'border-error' : ''"
      >
        <option v-for="option in options" :key="option.value" :value="option.value">
          {{ option.label }}
        </option>
      </select>

      <svg
        class="pointer-events-none absolute top-1/2 right-3.5 h-3 w-3 -translate-y-1/2 text-muted"
        viewBox="0 0 12 12"
        fill="none"
        aria-hidden="true"
      >
        <path
          d="M2.5 4.5L6 8l3.5-3.5"
          stroke="currentColor"
          stroke-width="1.4"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
    </div>

    <p v-if="error" :id="descId" class="mt-1.5 text-[13px] text-error">{{ error }}</p>
    <p v-else-if="hint" :id="descId" class="mt-1.5 text-[13px] text-muted">{{ hint }}</p>
  </div>
</template>
