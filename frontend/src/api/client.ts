/**
 * 统一的 fetch 封装。
 *
 * 职责：
 *  - 自动附加 `Authorization: Bearer`（登录态）与 `X-Manage-Key`（匿名管理密钥）
 *  - 统一解析后端的错误体 `{ error: { code, message, field, request_id } }`
 *  - 401 时通知 useAuth 执行登出，避免到处手写判断
 *  - 429 时把 `Retry-After` 带进错误对象，让上层能提示「请 X 秒后重试」
 */

import { loadToken, notifyUnauthorized } from './session'
import type {
  CreateLinkPayload,
  CreateLinkResponse,
  HealthReport,
  Link,
  LinkListResponse,
  LoginPayload,
  RegisterPayload,
  SessionResponse,
  Stats,
  UpdateLinkPayload,
  User,
} from './types'

/** API 基址。默认同源 `/api`，由 nginx / Vite 反代到后端。 */
const API_BASE = (import.meta.env.VITE_API_BASE_URL ?? '/api').replace(/\/+$/, '')

/** 后端统一错误体的类型化表示。 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly field?: string
  readonly requestId?: string
  /** 仅在 429 时有值，单位秒。 */
  readonly retryAfterSeconds?: number

  constructor(init: {
    status: number
    code: string
    message: string
    field?: string
    requestId?: string
    retryAfterSeconds?: number
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.field = init.field
    this.requestId = init.requestId
    this.retryAfterSeconds = init.retryAfterSeconds
  }

  /** 登录态失效。 */
  get isUnauthorized(): boolean {
    return this.status === 401
  }

  /** 被限流。 */
  get isRateLimited(): boolean {
    return this.status === 429
  }

  /** 网络层失败（请求根本没发出去或没有响应）。 */
  get isNetwork(): boolean {
    return this.status === 0
  }

  /** 面向用户的一句话，可直接塞进 toast。 */
  get friendly(): string {
    if (this.isNetwork) return '网络连接失败，请检查网络后重试'
    if (this.isRateLimited && this.retryAfterSeconds) {
      return `${this.message}（约 ${this.retryAfterSeconds} 秒）`
    }
    return this.message
  }
}

/** 单次请求的可选项。 */
interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  body?: unknown
  /** 匿名管理密钥；空表示不带该头。 */
  manageKey?: string | null
  /** 附加的查询参数（值 undefined / '' 的会被跳过）。 */
  query?: Record<string, string | number | undefined>
  signal?: AbortSignal
}

/** 发起请求并解析成 T。 */
async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const url = buildURL(path, options.query)

  const headers: Record<string, string> = { Accept: 'application/json' }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }

  const token = loadToken()
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }
  if (options.manageKey) {
    headers['X-Manage-Key'] = options.manageKey
  }

  let response: Response
  try {
    response = await fetch(url, {
      method: options.method ?? 'GET',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal ?? null,
      credentials: 'same-origin',
    })
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') {
      throw cause
    }
    throw new ApiError({ status: 0, code: 'network_error', message: '无法连接服务器' })
  }

  // 204 无内容（DELETE）直接返回 undefined
  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  const payload: unknown = text ? safeParse(text) : null

  if (!response.ok) {
    // 401 = 令牌失效：通知 useAuth 清空会话（router 里注册的处理器会送回登录页）。
    // 放在这里而不是各处 view：只要有一处忘了判断就会留下「半登录」的脏状态。
    //
    // 但只有「本次请求确实带了令牌」时才算令牌失效。登录/注册接口在口令错误时
    // 同样返回 401，那是凭据不对而不是会话过期 —— 不能因为输错一次密码就去清空
    // 本地会话（否则将来往 clearSession 里加任何清理逻辑都会连带误伤）。
    if (response.status === 401 && token !== null) {
      notifyUnauthorized()
    }
    throw toApiError(response, payload)
  }
  if (payload === null) {
    // 2xx 但没有 body：属于后端异常，明确报错而不是悄悄返回 undefined
    throw new ApiError({ status: response.status, code: 'empty_response', message: '服务器返回了空响应' })
  }
  return payload as T
}

/** 把后端的错误体转成 ApiError；解析不出来时退化成通用错误。 */
function toApiError(response: Response, payload: unknown): ApiError {
  const retryAfterSeconds = parseRetryAfter(response.headers.get('Retry-After'))

  const detail = extractErrorDetail(payload)
  if (detail) {
    return new ApiError({
      status: response.status,
      code: detail.code,
      message: detail.message,
      field: detail.field,
      requestId: detail.request_id,
      retryAfterSeconds,
    })
  }

  return new ApiError({
    status: response.status,
    code: `http_${response.status}`,
    message: `请求失败（HTTP ${response.status}）`,
    retryAfterSeconds,
  })
}

/** 从错误体里取出 error 对象；形状不对返回 null。 */
function extractErrorDetail(payload: unknown): {
  code: string
  message: string
  field?: string
  request_id?: string
} | null {
  if (!payload || typeof payload !== 'object' || !('error' in payload)) {
    return null
  }
  const { error } = payload as { error: unknown }
  if (!error || typeof error !== 'object') {
    return null
  }
  const record = error as Record<string, unknown>
  return {
    code: typeof record.code === 'string' ? record.code : 'unknown',
    message: typeof record.message === 'string' ? record.message : '请求失败',
    field: typeof record.field === 'string' ? record.field : undefined,
    request_id: typeof record.request_id === 'string' ? record.request_id : undefined,
  }
}

/** Retry-After 头解析：只接受秒数形式。 */
function parseRetryAfter(raw: string | null): number | undefined {
  if (!raw) return undefined
  const seconds = Number.parseInt(raw, 10)
  return Number.isFinite(seconds) && seconds > 0 ? seconds : undefined
}

/** 拼接完整 URL。 */
function buildURL(path: string, query?: Record<string, string | number | undefined>): string {
  const base = path.startsWith('/api') ? API_BASE + path.slice(4) : path
  if (!query) return base

  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === '') continue
    search.set(key, String(value))
  }
  const qs = search.toString()
  return qs ? `${base}?${qs}` : base
}

/** 容错的 JSON 解析：解析失败返回 null，由调用方决定怎么处理。 */
function safeParse(text: string): unknown {
  try {
    return JSON.parse(text) as unknown
  } catch {
    return null
  }
}

/** 短链相关接口。 */
export const linksApi = {
  /** 创建短链。匿名调用时会返回一次性的 manage_key。 */
  create(payload: CreateLinkPayload, manageKey?: string | null, signal?: AbortSignal) {
    return request<CreateLinkResponse>('/api/links', { method: 'POST', body: payload, manageKey, signal })
  },

  /** 我的链接列表（需要登录）。 */
  list(params: { limit?: number; cursor?: string; q?: string }, manageKey?: string | null, signal?: AbortSignal) {
    return request<LinkListResponse>('/api/links', {
      query: { limit: params.limit, cursor: params.cursor, q: params.q },
      manageKey,
      signal,
    })
  },

  /** 详情（登录态或 manage key 二选一）。 */
  get(code: string, manageKey?: string | null, signal?: AbortSignal) {
    return request<Link>(`/api/links/${encodeURIComponent(code)}`, { manageKey, signal })
  },

  /** 修改。 */
  update(code: string, payload: UpdateLinkPayload, manageKey?: string | null, signal?: AbortSignal) {
    return request<Link>(`/api/links/${encodeURIComponent(code)}`, {
      method: 'PATCH',
      body: payload,
      manageKey,
      signal,
    })
  },

  /** 软删除。 */
  remove(code: string, manageKey?: string | null, signal?: AbortSignal) {
    return request<void>(`/api/links/${encodeURIComponent(code)}`, { method: 'DELETE', manageKey, signal })
  },

  /** 统计聚合。 */
  stats(code: string, days = 30, manageKey?: string | null, signal?: AbortSignal) {
    return request<Stats>(`/api/links/${encodeURIComponent(code)}/stats`, {
      query: { days },
      manageKey,
      signal,
    })
  },

  /** 认领匿名短链（需要登录 + manage key）。 */
  claim(code: string, manageKey: string, signal?: AbortSignal) {
    return request<Link>(`/api/links/${encodeURIComponent(code)}/claim`, {
      method: 'POST',
      manageKey,
      signal,
    })
  },
}

/** 账号相关接口。 */
export const authApi = {
  register(payload: RegisterPayload, signal?: AbortSignal) {
    return request<SessionResponse>('/api/auth/register', { method: 'POST', body: payload, signal })
  },

  login(payload: LoginPayload, signal?: AbortSignal) {
    return request<SessionResponse>('/api/auth/login', { method: 'POST', body: payload, signal })
  },

  me(signal?: AbortSignal) {
    return request<User>('/api/auth/me', { signal })
  },
}

/** 健康检查（同源，不在 /api 下）。 */
export const systemApi = {
  health(signal?: AbortSignal) {
    return request<HealthReport>('/healthz', { signal })
  },
}

export { API_BASE }
