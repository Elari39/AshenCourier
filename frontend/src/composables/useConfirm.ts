/**
 * 确认框。
 *
 * 用法：
 * ```ts
 * const ok = await confirm({ title: '删除短链', message: `确定要删除 /${code} 吗？`, variant: 'danger' })
 * if (!ok) return
 * ```
 *
 * 为什么不用 `window.confirm`：
 *  - 样式不受控 —— 原生灰框跟设计系统无关，深色系统下还会反相成另一套；
 *  - 它会**阻塞主线程**：弹着的时候定时器、动画、请求回调全停；
 *  - 表达不了「这是危险操作」——按钮形态、初始焦点都没得选。
 * 代价是它不再同步，调用点必须 `await`。这是唯一需要改的地方。
 *
 * 状态放在模块级而不是 provide/inject：确认框是「应用级单例」，
 * 与 useToast 同一套路，视图里不必再套一层 Provider。
 */

import { readonly, ref } from 'vue'

/** 确认按钮的形态。危险操作（删除）用 'danger'。 */
export type ConfirmVariant = 'primary' | 'danger'

/** 调用方传入的参数。 */
export interface ConfirmOptions {
  title: string
  message: string
  /** 确认按钮文案，默认「确定」。 */
  confirmText?: string
  /** 取消按钮文案，默认「取消」。 */
  cancelText?: string
  /** 确认按钮形态，默认 'primary'。 */
  variant?: ConfirmVariant
}

/** 正在等待回答的那一个（默认值已补齐）。 */
export interface PendingConfirm {
  id: number
  title: string
  message: string
  confirmText: string
  cancelText: string
  variant: ConfirmVariant
}

const pending = ref<PendingConfirm | null>(null)
let settleCurrent: ((ok: boolean) => void) | null = null
let nextID = 1

/** 弹一个确认框，返回用户的选择。同一时刻只保留一个。 */
function confirm(options: ConfirmOptions): Promise<boolean> {
  // 上一个还没被回答就来了新的：先按「取消」把它结算掉。
  // 不结算的话那个调用方的 await 会永远挂着 —— 后面的代码一行都不会执行，
  // 而这种「静默挂起」在界面上看不出任何异常，最难查。
  settleCurrent?.(false)
  settleCurrent = null

  return new Promise<boolean>((resolve) => {
    settleCurrent = resolve
    pending.value = {
      id: nextID++,
      title: options.title,
      message: options.message,
      confirmText: options.confirmText ?? '确定',
      cancelText: options.cancelText ?? '取消',
      variant: options.variant ?? 'primary',
    }
  })
}

/** 结算并关闭。由对话框组件调用（确认 / 取消 / Esc / 点遮罩都走这里）。 */
function settle(ok: boolean): void {
  const resolve = settleCurrent
  settleCurrent = null
  pending.value = null
  resolve?.(ok)
}

/** 确认框的读接口与推接口。 */
export function useConfirm() {
  return { pending: readonly(pending), confirm, settle }
}
