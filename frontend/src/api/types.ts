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
  /**
   * 跳转是否需要口令。
   * 后端只回这个布尔，**绝不回摘要**；字段为 omitzero，缺席即「不需要口令」。
   */
  password_protected?: boolean
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

/** 国家分布的一项（`country` 是 ISO 3166-1 alpha-2 代码，大写）。 */
export interface CountryBucket {
  country: string
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
  /**
   * 国家分布（ISO 3166-1 alpha-2 代码）。
   *
   * **只含已知国家**：部署没有配置 GeoIP 库文件时是空数组，此时详情页整块隐藏这一项
   * （而不是画一个 100% 的「未知」条 —— 那会让人以为是解析失败）。
   */
  countries: CountryBucket[]
}

/** 创建短链入参。 */
export interface CreateLinkPayload {
  target_url: string
  custom_code?: string
  title?: string
  /** 标签：最多 10 个、每个 32 字符；后端会统一转小写。 */
  tags?: string[]
  expires_at?: string | null
  /** 可选访问口令（至少 8 位）；留空表示不设口令。后端只落 bcrypt 摘要。 */
  password?: string
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
  /** 新口令；不传表示保持原样（传空串是 422，清除请用 clear_password）。 */
  password?: string
  /** 为 true 时清除口令（改为无需口令）。 */
  clear_password?: boolean
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
