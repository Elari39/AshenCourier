<script setup lang="ts">
/**
 * 一键复制按钮。
 *
 * 抽出来的不是「按钮长什么样」（那是 Button 的事），而是那段**行为**：
 * 写剪贴板 → 成功提示 / 失败提示，外加按钮文案回闪 1.8 秒的「已复制」。
 * 这段行为原先在三个组件里各写一遍，其中链接列表的桌面分支与窄屏分支
 * 是同一份逻辑写了两遍（改文案就得想起两处）。
 *
 * 成功提示的文案做成 prop：复制短链和复制一次性管理密钥是两件事，
 * 提示里得说清复制到的是什么，否则用户不知道自己刚才存的是哪串东西。
 */
import { useCopy } from '@/composables/useCopy'
import { useToast } from '@/composables/useToast'
import type { ButtonSize, ButtonVariant } from '@/types/ui'

import Button from './Button.vue'

const props = withDefaults(
  defineProps<{
    /** 要复制进剪贴板的文本。 */
    value: string
    /** 常态文案。 */
    label?: string
    /** 刚复制成功时替换成的文案。 */
    copiedLabel?: string
    /** 成功提示。 */
    successMessage?: string
    variant?: ButtonVariant
    size?: ButtonSize
  }>(),
  {
    label: '复制',
    copiedLabel: '已复制',
    successMessage: '短链已复制',
    variant: 'secondary',
    size: 'md',
  },
)

const { copied, copy } = useCopy()
const toast = useToast()

async function handleClick(): Promise<void> {
  const ok = await copy(props.value)
  if (ok) {
    toast.success(props.successMessage)
  } else {
    // 失败原因（非安全上下文 / 权限被拒）对用户没有指导意义，
    // 能做的动作只有一个：自己选中那串字。所以给的是动作而不是原因。
    toast.error('复制失败，请手动选中复制')
  }
}
</script>

<template>
  <Button :variant="variant" :size="size" @click="handleClick">
    {{ copied ? copiedLabel : label }}
  </Button>
</template>
