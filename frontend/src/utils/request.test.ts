import { describe, expect, it } from 'vitest'

import { createRequestGuard, isAbortError } from './request'

describe('createRequestGuard', () => {
  it('发起新一轮会取消上一轮', () => {
    const guard = createRequestGuard()

    const first = guard.begin()
    expect(first.signal.aborted).toBe(false)

    guard.begin()
    expect(first.signal.aborted).toBe(true)
  })

  it('只有最新那一轮的凭证是新鲜的', () => {
    const guard = createRequestGuard()

    const first = guard.begin()
    const second = guard.begin()

    expect(first.isStale()).toBe(true)
    expect(second.isStale()).toBe(false)
  })

  it('第三轮发起后，前两轮都过期', () => {
    const guard = createRequestGuard()

    const first = guard.begin()
    const second = guard.begin()
    const third = guard.begin()

    expect(first.isStale()).toBe(true)
    expect(second.isStale()).toBe(true)
    expect(third.isStale()).toBe(false)
  })

  it('cancel 之后当前凭证立即过期，且之后 begin 出来的仍然新鲜', () => {
    const guard = createRequestGuard()

    const before = guard.begin()
    guard.cancel()
    expect(before.signal.aborted).toBe(true)
    expect(before.isStale()).toBe(true)

    const after = guard.begin()
    expect(after.isStale()).toBe(false)
  })

  it('cancel 之后 begin 出来的 signal 不会被上一次的 cancel 误伤', () => {
    const guard = createRequestGuard()

    guard.begin()
    guard.cancel()

    const next = guard.begin()
    expect(next.signal.aborted).toBe(false)
  })

  it('每轮拿到的是不同的 signal', () => {
    const guard = createRequestGuard()
    expect(guard.begin().signal).not.toBe(guard.begin().signal)
  })
})

describe('isAbortError', () => {
  it('认出主动取消', () => {
    const err = new Error('aborted')
    err.name = 'AbortError'
    expect(isAbortError(err)).toBe(true)
  })

  it('不把普通错误当成取消', () => {
    expect(isAbortError(new Error('boom'))).toBe(false)
    expect(isAbortError(new TypeError('boom'))).toBe(false)
  })

  it('非 Error 一律不算取消（不会被 undefined / 字符串骗到）', () => {
    expect(isAbortError(undefined)).toBe(false)
    expect(isAbortError(null)).toBe(false)
    expect(isAbortError('AbortError')).toBe(false)
    expect(isAbortError({ name: 'AbortError' })).toBe(false)
  })
})
