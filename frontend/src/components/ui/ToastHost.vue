<script setup lang="ts">
/**
 * toast 宿主。挂在 DefaultLayout 底部。
 *
 * 这里是**两个 live region，不是两个视觉区块**：
 *  - 错误进 assertive，会打断读屏当前正在念的那句；
 *  - 提示（info / success）进 polite，等当前这句念完再说。
 * 全塞进同一个 region 里，要么全都打断、要么全都排队 —— 两种都是错的。
 *
 * 视觉上仍是「一列从底部往上堆」：最外层只管排版与定位，
 * 播报粒度由两个内层 region 决定，两者互不干扰。
 */
import { computed } from 'vue'

import ToastItem from './ToastItem.vue'
import { useToast } from '@/composables/useToast'

const { toasts, dismiss } = useToast()

const alerts = computed(() => toasts.value.filter((item) => item.kind === 'error'))
const notices = computed(() => toasts.value.filter((item) => item.kind !== 'error'))
</script>

<template>
  <div class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-6">
    <!-- 错误排在提示上方：最需要读的那条离视线最近 -->
    <div class="flex w-full flex-col items-center gap-2" role="alert" aria-live="assertive">
      <TransitionGroup
        enter-active-class="transition duration-150 ease-out"
        enter-from-class="translate-y-2 opacity-0"
        leave-active-class="transition duration-150 ease-in"
        leave-to-class="translate-y-2 opacity-0"
      >
        <ToastItem v-for="toast in alerts" :key="toast.id" :toast="toast" @dismiss="dismiss" />
      </TransitionGroup>
    </div>

    <div class="flex w-full flex-col items-center gap-2" role="status" aria-live="polite">
      <TransitionGroup
        enter-active-class="transition duration-150 ease-out"
        enter-from-class="translate-y-2 opacity-0"
        leave-active-class="transition duration-150 ease-in"
        leave-to-class="translate-y-2 opacity-0"
      >
        <ToastItem v-for="toast in notices" :key="toast.id" :toast="toast" @dismiss="dismiss" />
      </TransitionGroup>
    </div>
  </div>
</template>
