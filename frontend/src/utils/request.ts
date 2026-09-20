/**
 * 请求并发守卫：让「后发先至」的旧响应无法覆盖新状态。
 *
 * 为什么需要它：`client.ts` 的每个方法都已经接受 `signal`，但组件里没人在用。
 * 于是连续切换统计窗口（7 天 → 90 天）时，两次请求同时在飞，谁先回来谁写进
 * `stats` —— 先回来的那个可能是 7 天的，于是面板上写着「90 天」而画的是 7 天
 * 的数据。列表页同理：翻页的第二页会追加上一次查询的结果。
 *
 * 两件事一起做，缺一不可：
 *   - abort：新请求发起时取消上一个，省掉无谓的等待与带宽；
 *   - 序号：abort 之后旧请求仍可能已经 resolve（取消是尽力而为），
 *     所以还要用序号判断「这份响应是不是最新的」。
 *
 * 用法：
 * ```ts
 * const guard = createRequestGuard()
 * const { signal, isStale } = guard.begin()
 * try {
 *   const res = await api.list(query, signal)
 *   if (isStale()) return          // 有更新的请求在飞，这份结果作废
 *   items.value = res
 * } catch (e) {
 *   if (isStale() || isAbortError(e)) return
 *   showError(e)
 * } finally {
 *   if (!isStale()) loading.value = false   // 别把新请求的 loading 掐掉
 * }
 * ```
 */
export interface RequestTicket {
  /** 传给 API 方法的 signal。 */
  signal: AbortSignal
  /** 这份响应是否已经过期（之后又发起了新请求）。 */
  isStale: () => boolean
}

export interface RequestGuard {
  /** 发起新一轮请求：取消上一轮，并返回只属于这一轮的凭证。 */
  begin(): RequestTicket
  /** 取消当前在飞的请求（组件卸载时调用）。 */
  cancel(): void
}

export function createRequestGuard(): RequestGuard {
  let latest = 0
  let controller: AbortController | undefined

  return {
    begin(): RequestTicket {
      // 先取消上一轮：它拿到的响应无论何时到达都已经没有意义
      controller?.abort()
      controller = new AbortController()

      const seq = ++latest
      return {
        signal: controller.signal,
        isStale: () => seq !== latest,
      }
    },

    cancel(): void {
      controller?.abort()
      controller = undefined
      // 让任何「已在路上」的回调都判定为过期，避免在卸载后的组件上写状态
      latest++
    },
  }
}

/**
 * 判断一个异常是不是「请求被取消」。
 *
 * 取消不是错误：它是我们主动发起的（切窗口、组件卸载），弹 toast 会误导用户。
 * 这里按 name 判定而不是 `instanceof DOMException` —— 后者在 Node（vitest）
 * 与某些 older 浏览器里并不成立。
 */
export function isAbortError(cause: unknown): boolean {
  return cause instanceof Error && cause.name === 'AbortError'
}
