<script setup lang="ts">
/**
 * 一条 toast。宿主里的两个 live region 共用这一份渲染。
 *
 * 关闭按钮是单选（不是「点整条就关」）：整条可点会让提示里的文字没法选中，
 * 而且读屏报出来的是一句「按钮」而不是提示内容。
 */
import type { Toast } from '@/composables/useToast'

defineProps<{ toast: Toast }>()
defineEmits<{ dismiss: [id: number] }>()
</script>

<template>
  <div class="toast" :class="toast.kind === 'error' ? 'toast-error' : 'toast-quiet'">
    <!-- 小圆点纯装饰：提示内容本身会被读屏念出来，不用它再报一遍 -->
    <span
      class="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full"
      :class="toast.kind === 'success' ? 'bg-success' : 'bg-current'"
      aria-hidden="true"
    />
    <span class="toast-message">{{ toast.message }}</span>
    <button type="button" class="toast-close" aria-label="关闭提示" @click="$emit('dismiss', toast.id)">
      <svg class="h-3.5 w-3.5" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
      </svg>
    </button>
  </div>
</template>
