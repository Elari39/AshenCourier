/**
 * 极简 toast。
 *
 * 只做一件事：把一句话显示出来然后自动消失。
 * MVP 不需要堆叠队列、操作按钮这些复杂度。
 */

import { readonly, ref } from 'vue'

/** toast 的语气，决定配色。 */
export type ToastKind = 'info' | 'success' | 'error'

/** 一条 toast。 */
export interface Toast {
  id: number
  kind: ToastKind
  message: string
}

/** 默认停留时长（毫秒）。错误停留久一点，用户需要读完。 */
const DURATIONS: Record<ToastKind, number> = {
  info: 3200,
  success: 3200,
  error: 6000,
}

const items = ref<Toast[]>([])
let nextID = 1

/** 弹一条 toast。 */
function push(kind: ToastKind, message: string, durationMs?: number): number {
  const id = nextID++
  items.value = [...items.value, { id, kind, message }]

  const ttl = durationMs ?? DURATIONS[kind]
  window.setTimeout(() => dismiss(id), ttl)
  return id
}

/** 手动关掉一条。 */
function dismiss(id: number): void {
  items.value = items.value.filter((item) => item.id !== id)
}

/** toast 的读接口与推接口。 */
export function useToast() {
  return {
    toasts: readonly(items),
    push,
    dismiss,
    info: (message: string) => push('info', message),
    success: (message: string) => push('success', message),
    error: (message: string) => push('error', message),
  }
}
