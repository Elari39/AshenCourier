/**
 * 页面滚动锁。
 *
 * 用**计数**而不是布尔：确认框与移动端菜单都会锁滚动，两者在真实使用里可能前后脚
 * 出现（菜单开着时点了个要确认的操作）。用布尔的话，先解锁的那个会把另一个的锁
 * 一起放掉 —— 表现是「关掉确认框之后，移动端菜单盖着的页面能滚了」，
 * 而这种串扰只在特定的操作顺序下才出现，很难复现。
 *
 * 保存/还原的是**行内样式的原值**而不是「锁没锁」这个状态：页面本来就没锁的时候
 * 要还原成空串，而不是写死一个 `visible`（那会盖掉将来别人写在样式表里的规则）。
 */

/** 当前持有锁的人数。 */
let holders = 0
/** 第一次加锁时 body 的行内 overflow 原值。 */
let savedOverflow = ''

export function useScrollLock() {
  /** 加锁。必须与 unlock 配对。 */
  function lock(): void {
    holders += 1
    if (holders > 1) return
    savedOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }

  /** 解锁。计数归零时才真正还原。 */
  function unlock(): void {
    if (holders === 0) return
    holders -= 1
    if (holders > 0) return
    document.body.style.overflow = savedOverflow
  }

  return { lock, unlock }
}
