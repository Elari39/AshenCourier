/**
 * 极简 toast。
 *
 * 只做三件事：把一句话显示出来、到点自动消失、同屏最多 3 条。
 * 刻意不做操作按钮、不做堆叠展开 —— 需要用户回话的场景交给 useConfirm 的对话框。
 */

import { readonly, ref } from 'vue'

/** toast 的语气，决定配色，也决定播报的紧急程度（见 ToastHost 的两个 live region）。 */
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

/** 同屏上限。 */
export const MAX_TOASTS = 3

const items = ref<Toast[]>([])
let nextID = 1

/**
 * 超出上限时该丢哪几条。
 *
 * 优先丢**最早的非错误项**：错误是要用户读完的，被后面一条 success 顶掉说不过去；
 * 全是错误时才按「最早」丢。这样「上限」不会变成「随机吞掉一条重要提示」。
 *
 * 抽成纯函数是为了能在单测里直接断言 —— 本项目 vitest 跑在 node 环境、
 * 不引 jsdom，组件行为一律交给 e2e，所以这里刻意不碰 DOM 也不碰定时器。
 */
export function trimToLimit(list: readonly Toast[], max: number = MAX_TOASTS): Toast[] {
  const kept = [...list]
  while (kept.length > max) {
    const idx = kept.findIndex((item) => item.kind !== 'error')
    kept.splice(idx === -1 ? 0 : idx, 1)
  }
  return kept
}

/** 弹一条 toast。 */
function push(kind: ToastKind, message: string, durationMs?: number): number {
  const id = nextID++
  items.value = trimToLimit([...items.value, { id, kind, message }])

  const ttl = durationMs ?? DURATIONS[kind]
  // 被上限挤掉的那条，它的定时器到点仍会来调 dismiss —— 按 id 过滤天然幂等，
  // 删一个已经不存在的 id 是空操作，不需要额外清理。
  setTimeout(() => dismiss(id), ttl)
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
