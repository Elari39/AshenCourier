/**
 * 前后端唯一的契约来源。
 *
 * 与后端 `backend/internal/handler/dto.go` 一一对应 —— 改后端 DTO 时这里必须同步改，
 * 否则 TypeScript 的类型护航就形同虚设。
 */

/** 短链状态机。 */
export type LinkStatus = 'active' | 'disabled' | 'deleted'

/** 账号。 */
export interface User {
  id: string
  email: string
  display_name?: string
  created_at?: string
}

/** 短链实体。 */
export interface Link {
  id: string
  short_code: string
  short_url: string
  target_url: string
  title?: string
  /** 标签，统一小写（后端按小写比较；筛选走 ?tag=）。 */
  tags?: string[]
  status: LinkStatus
  click_count: number
  expires_at?: string
  /** 是否为匿名创建（未归属任何账号）。 */
  anonymous: boolean
  created_at?: string
  updated_at?: string
}

/** 注册 / 登录响应。 */
export interface SessionResponse {
  user: User
  token: string
  expires_at?: string
}

/** 创建短链响应。`manage_key` 只在匿名创建时出现一次，且仅此一次。 */
export interface CreateLinkResponse {
  link: Link
  manage_key?: string
}

/** 链接列表响应（keyset 分页）。 */
export interface LinkListResponse {
  links: Link[]
  /** 为空表示已是最后一页。 */
  next_cursor?: string
}

/** 趋势图的一个数据点。 */
export interface DailyPoint {
  date: string
  clicks: number
}

/** 来源分布的一项；`referer` 为空串表示直接访问。 */
export interface RefererBucket {
  referer: string
  clicks: number
}

/** 设备分布的一项。 */
export interface DeviceBucket {
  device: string
  clicks: number
}

/** 浏览器分布的一项。 */
export interface BrowserBucket {
  browser: string
  clicks: number
}

/** 一条点击明细。 */
export interface ClickEvent {
  id: number
  occurred_at?: string
  referer?: string
  user_agent?: string
  /**
   * **掩码后**的网段，不是完整地址：IPv4 到 /24、IPv6 到 /64。
   * 原始 IP 只留在库里（风控/排障直接查库），不经 API 外流。
   */
  ip?: string
  country?: string
  device?: string
  browser?: string
  os?: string
}

/** 点击明细列表响应（keyset 分页）。 */
export interface ClickListResponse {
  clicks: ClickEvent[]
  /** 为空表示已到底。 */
  next_cursor?: string
  /** 本页实际生效的窗口天数与起点（与统计同口径）。 */
  days?: number
  since?: string
}

/** 统计聚合结果。 */
export interface Stats {
  /** PG 基线 + Redis 待同步增量（全量、跨窗口）。 */
  total_clicks: number
  /** 统计窗口内的明细条数。 */
  window_clicks?: number
  days: number
  since?: string
  daily: DailyPoint[]
  top_referers: RefererBucket[]
  devices: DeviceBucket[]
  browsers: BrowserBucket[]
}

/** `/healthz` 响应。 */
export interface HealthReport {
  status: 'ok' | 'degraded'
  version?: string
  postgres: string
  redis: string
  worker_enabled: boolean
  dropped_clicks?: number
  failed_clicks?: number
  queue_len?: number
  stream_len?: number
  stream_pending?: number
  /** 短码缓存未命中、真正回源 PG 的累计次数（缓存击穿的观测口径）。 */
  pg_fallbacks?: number
  rate_limit_degraded?: number
  uptime_seconds?: number
  rate_limit_native_increx?: boolean
  /** 限流应急开关是否被打开（RATE_LIMIT_DISABLED=true，全量放行）。 */
  rate_limit_disabled?: boolean
  consumed_clicks?: number
  worker_errors?: number
  errors?: string[]
}

/** 创建短链入参。 */
export interface CreateLinkPayload {
  target_url: string
  custom_code?: string
  title?: string
  /** 标签：最多 10 个、每个 32 字符；后端会统一转小写。 */
  tags?: string[]
  expires_at?: string | null
}

/** 修改短链入参。字段缺省 = 不修改。 */
export interface UpdateLinkPayload {
  target_url?: string
  title?: string
  /** 传 [] 表示清空标签；不传表示保持原样。 */
  tags?: string[]
  status?: Exclude<LinkStatus, 'deleted'>
  expires_at?: string | null
  /** 为 true 时把 expires_at 置空（改为永久有效）。 */
  clear_expires?: boolean
}

/** 注册入参。 */
export interface RegisterPayload {
  email: string
  password: string
  display_name?: string
}

/** 登录入参。 */
export interface LoginPayload {
  email: string
  password: string
}
