/**
 * 会话状态的最小存放点。
 *
 * 单独成一个模块，是为了让 `api/client.ts`（需要读 token / 通知 401）
 * 与 `composables/useAuth.ts`（需要写 token）都能引用而不产生循环依赖。
 */

import type { User } from './types'

const TOKEN_KEY = 'ashen:token'
const USER_KEY = 'ashen:user'
const MANAGE_KEYS_KEY = 'ashen:manageKeys'

/** 401 时的回调，由 useAuth 注册（清空本地会话并跳登录页）。 */
let unauthorizedHandler: (() => void) | null = null
// Failed writes must not let a stale persisted token override the current session.
const memory = new Map<string, string | null>()
const volatile = new Set<string>()

function read(key: string): string | null {
  if (volatile.has(key)) return memory.get(key) ?? null
  try {
    const value = window.localStorage.getItem(key)
    memory.set(key, value)
    return value
  } catch {
    return memory.get(key) ?? null
  }
}

function write(key: string, value: string | null): void {
  memory.set(key, value)
  try {
    if (value === null) window.localStorage.removeItem(key)
    else window.localStorage.setItem(key, value)
    volatile.delete(key)
  } catch {
    volatile.add(key)
  }
}

/** 读取当前令牌；存储不可用时使用内存会话。 */
export function loadToken(): string | null {
  return read(TOKEN_KEY) || null
}

/** 写入令牌；传 null 表示登出。 */
export function saveToken(token: string | null): void {
  write(TOKEN_KEY, token)
}

/** 读取缓存的用户对象。 */
export function loadUser(): User | null {
  try {
    const raw = read(USER_KEY)
    const value: unknown = raw ? JSON.parse(raw) : null
    if (!value || typeof value !== 'object' || Array.isArray(value)) return null
    const user = value as Record<string, unknown>
    if (typeof user.id !== 'string' || !user.id || typeof user.email !== 'string') return null
    if (user.display_name !== undefined && typeof user.display_name !== 'string') return null
    if (user.created_at !== undefined && typeof user.created_at !== 'string') return null
    return user as unknown as User
  } catch {
    return null
  }
}

/** 缓存用户对象。 */
export function saveUser(user: unknown | null): void {
  write(USER_KEY, user === null ? null : JSON.stringify(user))
}

/** 读取「短码 → 匿名管理密钥」映射。 */
export function loadManageKeys(): Record<string, string> {
  try {
    const raw = read(MANAGE_KEYS_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : null
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return Object.fromEntries(
        Object.entries(parsed).filter(
          ([code, key]) => /^[A-Za-z0-9_-]{3,32}$/.test(code) && typeof key === 'string' && key.length > 0,
        ),
      )
    }
    return {}
  } catch {
    return {}
  }
}

/** 持久化「短码 → 匿名管理密钥」映射。 */
export function saveManageKeys(keys: Record<string, string>): void {
  write(MANAGE_KEYS_KEY, JSON.stringify(keys))
}

/** 注册 401 处理器；同一次会话只需注册一次。 */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

/** 由 client 在收到 401 时调用。 */
export function notifyUnauthorized(): void {
  unauthorizedHandler?.()
}
