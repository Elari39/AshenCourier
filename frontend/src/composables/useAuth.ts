/**
 * 登录态与匿名管理密钥。
 *
 * 刻意不引入 Pinia：MVP 的全局状态只有「当前用户」与「短码→管理密钥」这两块，
 * 用模块级 ref 做单例（ESM 天然只求值一次）就够了，少一层依赖与心智负担。
 */

import { computed, ref } from 'vue'

import { authApi } from '@/api/client'
import {
  loadManageKeys,
  loadToken,
  loadUser,
  saveManageKeys,
  saveToken,
  saveUser,
} from '@/api/session'
import type { LoginPayload, RegisterPayload, SessionResponse, User } from '@/api/types'

const token = ref<string | null>(loadToken())
const user = ref<User | null>(loadUser<User>())
/** 短码 → 匿名管理密钥。匿名创建的链接靠它才能再管理。 */
const manageKeys = ref<Record<string, string>>(loadManageKeys())

/** 是否持有登录令牌。 */
const isAuthenticated = computed(() => token.value !== null)

/** 顶栏展示用的名字：昵称优先，其次邮箱前缀。 */
const displayName = computed(() => {
  if (!user.value) return '我的账号'
  if (user.value.display_name) return user.value.display_name
  return user.value.email.split('@')[0] ?? user.value.email
})

/** 写入会话（注册 / 登录成功、或 /me 刷新后调用）。 */
function applySession(session: SessionResponse): void {
  token.value = session.token
  user.value = session.user
  saveToken(session.token)
  saveUser(session.user)
}

/** 只更新用户对象（例如 /me 返回了更新的资料）。 */
function applyUser(next: User): void {
  user.value = next
  saveUser(next)
}

/** 清空登录态。401 处理与主动登出都走这里。 */
export function clearSession(): void {
  token.value = null
  user.value = null
  saveToken(null)
  saveUser(null)
}

/** 记住某条匿名短链的管理密钥。 */
function rememberManageKey(code: string, key: string): void {
  manageKeys.value = { ...manageKeys.value, [code]: key }
  saveManageKeys(manageKeys.value)
}

/** 忘记某条短链的管理密钥（认领成功后调用）。 */
function forgetManageKey(code: string): void {
  if (!(code in manageKeys.value)) return
  const next = { ...manageKeys.value }
  delete next[code]
  manageKeys.value = next
  saveManageKeys(next)
}

/** 取某条短链的管理密钥。 */
function manageKeyFor(code: string): string | undefined {
  return manageKeys.value[code]
}

/** 登录态与匿名密钥的操作集合。 */
export function useAuth() {
  /** 注册并直接登录。 */
  async function register(payload: RegisterPayload): Promise<SessionResponse> {
    const session = await authApi.register(payload)
    applySession(session)
    return session
  }

  /** 登录。 */
  async function login(payload: LoginPayload): Promise<SessionResponse> {
    const session = await authApi.login(payload)
    applySession(session)
    return session
  }

  /** 主动登出（不调后端：JWT 无状态，删掉本地令牌即可）。 */
  function logout(): void {
    clearSession()
  }

  /** 用 /api/auth/me 校验令牌是否仍然有效，并刷新用户资料。 */
  async function refreshMe(): Promise<User | null> {
    if (!token.value) return null
    const me = await authApi.me()
    applyUser(me)
    return me
  }

  return {
    token,
    user,
    manageKeys,
    isAuthenticated,
    displayName,
    register,
    login,
    logout,
    refreshMe,
    rememberManageKey,
    forgetManageKey,
    manageKeyFor,
  }
}
