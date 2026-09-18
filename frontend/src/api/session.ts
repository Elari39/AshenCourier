/**
 * 会话状态的最小存放点。
 *
 * 单独成一个模块，是为了让 `api/client.ts`（需要读 token / 通知 401）
 * 与 `composables/useAuth.ts`（需要写 token）都能引用而不产生循环依赖。
 */

const TOKEN_KEY = 'ashen:token'
const USER_KEY = 'ashen:user'
const MANAGE_KEYS_KEY = 'ashen:manageKeys'

/** 401 时的回调，由 useAuth 注册（清空本地会话并跳登录页）。 */
let unauthorizedHandler: (() => void) | null = null

/** 读取 localStorage 里的令牌；不可用时返回 null。 */
export function loadToken(): string | null {
  try {
    return window.localStorage.getItem(TOKEN_KEY)
  } catch {
    // 隐私模式下 localStorage 可能抛错，按「未登录」处理
    return null
  }
}

/** 写入令牌；传 null 表示登出。 */
export function saveToken(token: string | null): void {
  try {
    if (token === null) {
      window.localStorage.removeItem(TOKEN_KEY)
    } else {
      window.localStorage.setItem(TOKEN_KEY, token)
    }
  } catch {
    /* 存储不可用时静默降级为「仅本次会话有效」 */
  }
}

/** 读取缓存的用户对象。 */
export function loadUser<T>(): T | null {
  try {
    const raw = window.localStorage.getItem(USER_KEY)
    return raw ? (JSON.parse(raw) as T) : null
  } catch {
    return null
  }
}

/** 缓存用户对象。 */
export function saveUser(user: unknown | null): void {
  try {
    if (user === null) {
      window.localStorage.removeItem(USER_KEY)
    } else {
      window.localStorage.setItem(USER_KEY, JSON.stringify(user))
    }
  } catch {
    /* 同上 */
  }
}

/** 读取「短码 → 匿名管理密钥」映射。 */
export function loadManageKeys(): Record<string, string> {
  try {
    const raw = window.localStorage.getItem(MANAGE_KEYS_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : null
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as Record<string, string>
    }
    return {}
  } catch {
    return {}
  }
}

/** 持久化「短码 → 匿名管理密钥」映射。 */
export function saveManageKeys(keys: Record<string, string>): void {
  try {
    window.localStorage.setItem(MANAGE_KEYS_KEY, JSON.stringify(keys))
  } catch {
    /* 同上 */
  }
}

/** 注册 401 处理器；同一次会话只需注册一次。 */
export function setUnauthorizedHandler(handler: (() => void) | null): void {
  unauthorizedHandler = handler
}

/** 由 client 在收到 401 时调用。 */
export function notifyUnauthorized(): void {
  unauthorizedHandler?.()
}
