/**
 * 真浏览器输入：合成鼠标点击与按键（CDP 的输入管线，等价于真人操作）。
 *
 * 为什么不直接用 `element.click()` / `new KeyboardEvent()`：
 *  - 程序化的 `click()` **不移动焦点**，也绕过命中测试 —— 元素被别的东西盖住、
 *    或者尺寸为 0，它照样「点得中」；
 *  - 在页面里造一个 KeyboardEvent 只会触发你监听的那一个 handler，
 *    浏览器的**默认行为**（Tab 移动焦点、Esc 关掉原生浮层）一概不发生。
 * 需要断言「焦点在哪」「Tab 跑不跑得出去」的时候，这两条差别就是全部。
 */
import { assert } from './harness.mjs'

/**
 * 真的用鼠标点一下。`locateExpr` 是返回元素（或 null）的表达式；
 * 也可以直接给 `{x, y}` 绕过定位（点遮罩、点空白处要用）。
 */
export async function realClick(session, locateExpr, label, { x, y } = {}) {
  if (x === undefined) {
    const point = await session.evaluate(`(() => {
      const el = (${locateExpr})
      if (!el) return null
      // behavior:'instant' 覆盖全局的 scroll-behavior: smooth ——
      // 平滑滚动途中量到的矩形是错的，会点到别的元素上
      el.scrollIntoView({ block: 'center', behavior: 'instant' })
      const rect = el.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, w: rect.width, h: rect.height }
    })()`)
    assert(point && point.w > 0 && point.h > 0, `点不到「${label}」：元素不存在或尺寸为 0`)
    x = Math.round(point.x)
    y = Math.round(point.y)
  }

  const common = { x, y, button: 'left', clickCount: 1 }
  await session.send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...common, button: 'none' })
  await session.send('Input.dispatchMouseEvent', { type: 'mousePressed', ...common })
  await session.send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...common })
}

/** 发一次真实的按键。modifiers 的位：Alt=1 Ctrl=2 Meta=4 Shift=8。 */
export async function pressKey(session, { key, code, keyCode, modifiers = 0 }) {
  const base = { key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers }
  await session.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...base })
  await session.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base })
}

export const TAB = { key: 'Tab', code: 'Tab', keyCode: 9 }
export const SHIFT_TAB = { ...TAB, modifiers: 8 }
export const ESCAPE = { key: 'Escape', code: 'Escape', keyCode: 27 }
