import { afterEach, expect, it, vi } from 'vitest'

afterEach(() => vi.unstubAllGlobals())

it('storage failure keeps the API authenticated and logout clears the in-memory token', async () => {
  vi.resetModules()
  vi.stubGlobal('window', {
    get localStorage() {
      throw new Error('blocked')
    },
  })
  const session = await import('./session')
  session.saveToken('memory-token')
  expect(session.loadToken()).toBe('memory-token')
  const fetch = vi.fn<typeof globalThis.fetch>(
    async () => new Response(JSON.stringify({ id: 'u', email: 'u@example.com' })),
  )
  vi.stubGlobal('fetch', fetch)
  const { authApi } = await import('./client')
  await authApi.me()
  expect(fetch.mock.calls[0]?.[1]).toMatchObject({ headers: { Authorization: 'Bearer memory-token' } })
  session.saveToken(null)
  expect(session.loadToken()).toBeNull()
})

it('failed token deletion cannot resurrect an old persisted session', async () => {
  vi.resetModules()
  vi.stubGlobal('window', {
    localStorage: {
      getItem: () => 'old-token',
      removeItem: () => {
        throw new Error('readonly')
      },
    },
  })
  const session = await import('./session')
  expect(session.loadToken()).toBe('old-token')
  session.saveToken(null)
  expect(session.loadToken()).toBeNull()
})

it('rejects malformed user objects and filters malformed management keys', async () => {
  vi.resetModules()
  vi.stubGlobal('window', {
    localStorage: {
      getItem: (key: string) =>
        key === 'ashen:user'
          ? '{"id":"u","email":42}'
          : '{"good-code":"secret","bad-code":42,"__proto__":"secret"}',
    },
  })
  const session = await import('./session')
  expect(session.loadUser()).toBeNull()
  expect(session.loadManageKeys()).toEqual(
    Object.fromEntries([
      ['good-code', 'secret'],
      ['__proto__', 'secret'],
    ]),
  )
})
