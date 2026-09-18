<script setup lang="ts">
/**
 * toast 宿主。挂在 DefaultLayout 底部，
 * 用深色浮层承载 —— 这是少数几个「深色面板出现在奶油页面上」的合法位置
 * （DESIGN.md 里 cookie-consent-card 就是这么用的）。
 */
import { useToast } from '@/composables/useToast'

const { toasts, dismiss } = useToast()
</script>

<template>
  <div
    class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-6"
    role="status"
    aria-live="polite"
  >
    <TransitionGroup
      enter-active-class="transition duration-150 ease-out"
      enter-from-class="translate-y-2 opacity-0"
      leave-active-class="transition duration-150 ease-in"
      leave-to-class="translate-y-2 opacity-0"
    >
      <button
        v-for="toast in toasts"
        :key="toast.id"
        type="button"
        class="pointer-events-auto flex w-full max-w-md items-start gap-3 rounded-lg px-5 py-4 text-left"
        :class="
          toast.kind === 'error'
            ? 'bg-error text-on-primary'
            : 'bg-surface-dark text-on-dark'
        "
        @click="dismiss(toast.id)"
      >
        <span class="mt-1 h-1.5 w-1.5 shrink-0 rounded-full" :class="toast.kind === 'success' ? 'bg-success' : 'bg-current'" />
        <span class="text-[14px] leading-[1.5]">{{ toast.message }}</span>
      </button>
    </TransitionGroup>
  </div>
</template>
