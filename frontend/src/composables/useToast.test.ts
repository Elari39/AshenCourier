import { afterEach, describe, expect, it, vi } from 'vitest'

import { MAX_TOASTS, trimToLimit, useToast, type Toast } from './useToast'

/** 造一条 toast。 */
function make(id: number, kind: Toast['kind'] = 'info'): Toast {
  return { id, kind, message: `第 ${id} 条` }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('trimToLimit', () => {
  it('没超上限时原样返回，且不返回入参那个数组本身', () => {
    const input = [make(1), make(2)]
    const out = trimToLimit(input)
    expect(out.map((item) => item.id)).toEqual([1, 2])
    // 返回新数组而不是入参：调用方拿着 readonly 的一份，不该被别处的改动影响
    expect(out).not.toBe(input)
    expect(input).toHaveLength(2)
  })

  it('超出上限时丢掉最早的那条', () => {
    const out = trimToLimit([make(1), make(2), make(3), make(4)], 3)
    expect(out.map((item) => item.id)).toEqual([2, 3, 4])
  })

  it('有错误项时优先丢非错误项，而不是按时间丢', () => {
    // 第 1 条是最早的、但它是 error。按时间丢就会把它丢掉，
    // 而错误恰恰是唯一需要用户读完的一条。
    const out = trimToLimit([make(1, 'error'), make(2), make(3), make(4)], 3)
    expect(out.map((item) => item.id)).toEqual([1, 3, 4])
  })

  it('全是错误项时只能按时间丢最早的那条', () => {
    const out = trimToLimit([make(1, 'error'), make(2, 'error')], 1)
    expect(out.map((item) => item.id)).toEqual([2])
  })

  it('默认上限就是 MAX_TOASTS', () => {
    const many = Array.from({ length: MAX_TOASTS + 2 }, (_, i) => make(i + 1))
    expect(trimToLimit(many)).toHaveLength(MAX_TOASTS)
  })
})

describe('useToast 的推入', () => {
  it('同屏最多 3 条，被挤掉的是最早的提示而不是刚推的那条', () => {
    // 用假定时器：真定时器会让这个用例结束后仍有悬挂句柄（toast 是模块级单例，
    // 到点回调还挂在事件循环上），也可能让「刚推的那条还在不在」变成随机结果。
    vi.useFakeTimers()
    const { toasts, push, dismiss } = useToast()

    const ids = [
      push('info', '第一条', 10_000),
      push('success', '第二条', 10_000),
      push('info', '第三条', 10_000),
      push('info', '第四条', 10_000),
    ]

    expect(toasts.value).toHaveLength(MAX_TOASTS)
    expect(toasts.value.map((item) => item.message)).toEqual(['第二条', '第三条', '第四条'])

    ids.forEach(dismiss)
    expect(toasts.value).toHaveLength(0)
  })
})
