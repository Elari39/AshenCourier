import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { authApi } from './client'
import { setUnauthorizedHandler } from './session'

/**
 * 401 的两个分支必须分开。
 *
 * 后端把「凭据不对」和「未认证」拆成了 invalid_credentials / unauthorized
 * 两个 code，前端得真的用上它 —— 否则一次输错密码就可能把用户手里那份
 * 有效令牌清掉（清空会话会连同将来的其它清理逻辑一起生效）。
 *
 * 这里跑在 node 环境：session.ts 读的是 window.localStorage（不可用时按
 * 「未登录」处理），所以要让 token !== null，必须自己给一份最小的 localStorage。
 */
describe('client 收到 401 时的处置', () => {
  let notifyCount = 0

  beforeEach(() => {
    notifyCount = 0
    setUnauthorizedHandler(() => {
      notifyCount += 1
    })
    stubStoredToken('stored-token')
  })

  afterEach(() => {
    setUnauthorizedHandler(null)
    vi.unstubAllGlobals()
  })

  /** 模拟本地已存着一份令牌。 */
  function stubStoredToken(token: string | null): void {
    vi.stubGlobal('window', {
      localStorage: {
        getItem: (key: string) => (key === 'ashen:token' ? token : null),
        setItem: () => {},
        removeItem: () => {},
      },
    })
  }

  /** 让下一次 fetch 返回指定的错误体。 */
  function stubLogin(status: number, body: unknown): void {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })),
    )
  }

  it('凭据错误（invalid_credentials）只报错、不清空会话', async () => {
    stubLogin(401, { error: { code: 'invalid_credentials', message: '邮箱或密码不正确' } })

    await expect(authApi.login({ email: 'a@example.com', password: 'wrong' })).rejects.toThrow(
      '邮箱或密码不正确',
    )
    expect(notifyCount).toBe(0)
  })

  it('令牌失效（unauthorized）触发清空会话', async () => {
    stubLogin(401, { error: { code: 'unauthorized', message: '登录状态无效，请重新登录' } })

    await expect(authApi.login({ email: 'a@example.com', password: 'whatever' })).rejects.toThrow()
    expect(notifyCount).toBe(1)
  })

  it('本地没有令牌时不触发清空（匿名浏览时的 401）', async () => {
    stubStoredToken(null)
    stubLogin(401, { error: { code: 'unauthorized', message: '请先登录' } })

    await expect(authApi.login({ email: 'a@example.com', password: 'whatever' })).rejects.toThrow()
    expect(notifyCount).toBe(0)
  })
})
