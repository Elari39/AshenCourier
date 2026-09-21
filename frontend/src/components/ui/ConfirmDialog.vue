<script setup lang="ts">
/**
 * 确认框宿主。**挂在 DefaultLayout 里**（与 ToastHost 同级），
 * 页面通过 `useConfirm().confirm()` 调用，不直接引用本组件。
 *
 * 键盘与读屏行为照 WAI-ARIA 的 alertdialog 模式做：
 *  - `role="alertdialog"` + `aria-modal`，标题与正文分别用 aria-labelledby /
 *    aria-describedby 关联（读屏进框时会先把这两段念出来）
 *  - 打开时焦点落在**「取消」**上：破坏性操作的默认落点必须是「不做什么」
 *  - Tab / Shift+Tab 在框内循环，跑不出去
 *  - Esc 与点遮罩都算取消
 *  - 关闭后焦点还给触发它的那个元素；若它已经不在文档里（最典型的就是
 *    「删除成功 → 那一行被移出列表」），退到页面主区域，
 *    而不是把焦点丢在 <body> 上让键盘用户从页首重新 Tab 一遍
 */
import { nextTick, ref, useId, watch } from 'vue'

import Button from './Button.vue'
import { useConfirm } from '@/composables/useConfirm'

const { pending, settle } = useConfirm()

const titleId = useId()
const messageId = useId()

/** 面板：用来圈定 Tab 的循环范围。 */
const panel = ref<HTMLElement | null>(null)
/** 「取消」按钮：初始焦点落点。 */
const cancelRef = ref<InstanceType<typeof Button> | null>(null)

/** 可聚焦元素清单。与 WAI-ARIA APG 给的那份一致。 */
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/** 打开前的焦点位置，关闭后还给它。 */
let returnFocusTo: HTMLElement | null = null
/** 打开前的 body overflow，关掉时原样写回（可能是空串）。 */
let previousOverflow = ''

function focusCancel(): void {
  const el = cancelRef.value?.$el as HTMLElement | undefined
  el?.focus({ preventScroll: true })
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    settle(false)
    return
  }
  if (event.key !== 'Tab') return

  const root = panel.value
  if (!root) return

  const focusables = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
  const first = focusables.at(0)
  const last = focusables.at(-1)
  if (!first || !last) {
    // 框里没有任何可聚焦元素（理论上不会发生）：干脆拦住 Tab，别让焦点漏到背景页
    event.preventDefault()
    return
  }

  const active = document.activeElement
  const inside = active instanceof HTMLElement && root.contains(active)

  if (event.shiftKey) {
    if (!inside || active === first) {
      event.preventDefault()
      last.focus()
    }
    return
  }
  if (!inside || active === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(pending, async (now, before) => {
  if (now && !before) {
    const active = document.activeElement
    returnFocusTo = active instanceof HTMLElement ? active : null

    // capture：确认框开着的时候，Esc 必须先归它处理
    document.addEventListener('keydown', onKeydown, true)

    // 滚动锁：遮罩盖住了页面，但滚轮照样会滚底下那一层
    previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    await nextTick()
    focusCancel()
    return
  }

  if (!now && before) {
    document.removeEventListener('keydown', onKeydown, true)
    document.body.style.overflow = previousOverflow

    const target = returnFocusTo
    returnFocusTo = null
    // preventScroll：底下的页面一步都没动过，焦点归位不该顺带把页面滚一下
    if (target?.isConnected) {
      target.focus({ preventScroll: true })
      return
    }
    document.querySelector<HTMLElement>('main')?.focus({ preventScroll: true })
  }
})
</script>

<template>
  <Transition
    enter-active-class="transition duration-150 ease-out"
    enter-from-class="opacity-0"
    leave-active-class="transition duration-150 ease-in"
    leave-to-class="opacity-0"
  >
    <!-- .self：点遮罩算取消，点面板本身不算（否则框内空白处一按就关了） -->
    <div v-if="pending" class="dialog-backdrop" @click.self="settle(false)">
      <div
        ref="panel"
        class="dialog-panel"
        role="alertdialog"
        aria-modal="true"
        :aria-labelledby="titleId"
        :aria-describedby="messageId"
      >
        <h2 :id="titleId" class="title-lg">{{ pending.title }}</h2>
        <p :id="messageId" class="mt-2 text-[14px] leading-[1.55] text-body">
          {{ pending.message }}
        </p>

        <div class="dialog-actions">
          <Button ref="cancelRef" variant="secondary" @click="settle(false)">
            {{ pending.cancelText }}
          </Button>
          <Button
            :variant="pending.variant === 'danger' ? 'danger' : 'primary'"
            @click="settle(true)"
          >
            {{ pending.confirmText }}
          </Button>
        </div>
      </div>
    </div>
  </Transition>
</template>
