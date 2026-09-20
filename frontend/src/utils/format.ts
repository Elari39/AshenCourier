/**
 * 展示层的格式化工具。
 *
 * 全部是纯函数，方便在组件里直接调用，也方便将来加单测。
 */

const DATE_TIME_FORMATTER = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

// 明细列表专用：同一分钟里常有多条点击，只到分钟会看起来像重复行
const DATE_TIME_SECONDS_FORMATTER = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})

/** 把 ISO 时间格式化成 `2026/09/18 13:05`；空值返回占位符。 */
export function formatDateTime(iso?: string | null, fallback = '—'): string {
  if (!iso) return fallback
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return fallback
  return DATE_TIME_FORMATTER.format(date)
}

/** 精确到秒的 `2026/09/18 13:05:42`，用于点击明细。 */
export function formatDateTimeSeconds(iso?: string | null, fallback = '—'): string {
  if (!iso) return fallback
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return fallback
  return DATE_TIME_SECONDS_FORMATTER.format(date)
}

/**
 * 把 ISO 日期格式化成 `09-18`，用于趋势图刻度。
 *
 * 刻意**不经过 Date**：`new Date('2026-09-18')` 按 UTC 午夜解析，
 * 再用本地的 `getMonth()` / `getDate()` 取值 —— 在负偏移时区（美洲）它会
 * 落到 9 月 17 日 19:00，刻度整体差一天。而后端是按 UTC 日界聚合的
 * （`date_trunc('day', occurred_at AT TIME ZONE 'UTC')`），所以这里显示的
 * 也应当就是 UTC 的那一天：直接取日期部分，既正确又与时区无关。
 *
 * 老实现没被单测抓住，是因为用例给的是带时间的串（`2026-09-08T10:00:00`）——
 * 那种串按本地时间解析，任何时区下都还是 09-08；**真正出问题的恰恰是后端
 * 实际返回的纯日期串**（`"date": "2026-09-01"`）。
 */
export function formatShortDate(iso: string): string {
  const parts = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso.trim())
  if (!parts) return iso
  return `${parts[2]}-${parts[3]}`
}

/** 千分位数字。 */
export function formatNumber(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}

/** 大数压缩：1284 → 1.3k，1234567 → 1.2M。 */
export function formatCompact(value: number): string {
  if (Math.abs(value) < 1000) return String(value)
  if (Math.abs(value) < 1_000_000) return `${(value / 1000).toFixed(1)}k`
  return `${(value / 1_000_000).toFixed(1)}M`
}

/** 从 URL 里取出主机名，用于列表里紧凑展示目标地址。 */
export function hostOf(url: string): string {
  try {
    return new URL(url).hostname
  } catch {
    return url
  }
}

/** 中段省略：保留首尾，中间用 … —— 长 URL 用尾部省略会丢掉最关键的域名后缀。 */
export function truncateMiddle(input: string, head = 34, tail = 18): string {
  if (input.length <= head + tail + 1) return input
  return `${input.slice(0, head)}…${input.slice(-tail)}`
}

/** 是否已过期。 */
export function isExpired(iso?: string | null): boolean {
  if (!iso) return false
  const date = new Date(iso)
  return !Number.isNaN(date.getTime()) && date.getTime() <= Date.now()
}

/** 剩余有效期的中文描述。 */
export function describeExpiry(iso?: string | null): string {
  if (!iso) return '永久有效'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '有效期未知'

  const remainingMs = date.getTime() - Date.now()
  if (remainingMs <= 0) return '已过期'

  const days = Math.floor(remainingMs / 86_400_000)
  if (days >= 1) return `${days} 天后过期`
  const hours = Math.floor(remainingMs / 3_600_000)
  if (hours >= 1) return `${hours} 小时后过期`
  return '不到 1 小时后过期'
}

/** 状态的中文标签与配色语气。 */
export function describeStatus(status: string): { label: string; tone: 'ok' | 'muted' | 'error' } {
  switch (status) {
    case 'active':
      return { label: '正常', tone: 'ok' }
    case 'disabled':
      return { label: '已停用', tone: 'muted' }
    case 'deleted':
      return { label: '已删除', tone: 'error' }
    default:
      return { label: status, tone: 'muted' }
  }
}

/** 来源标识的中文名：空串代表直接访问。 */
export function describeReferer(referer: string): string {
  return referer === '' ? '直接访问' : referer
}

/** 设备标识的中文名。 */
export function describeDevice(device: string): string {
  switch (device) {
    case 'desktop':
      return '桌面端'
    case 'mobile':
      return '手机'
    case 'tablet':
      return '平板'
    case 'bot':
      return '爬虫 / 机器人'
    case 'unknown':
      return '未知'
    default:
      return device
  }
}

/**
 * 浏览器 / 系统的组合展示。
 * 两边都是 unknown 时只显示一个「未知」——「未知 / 未知」既啰嗦又像出了错。
 */
export function describeClient(browser?: string, os?: string): string {
  const parts = [browser, os].filter((part): part is string => Boolean(part) && part !== 'unknown')
  return parts.length > 0 ? parts.join(' / ') : '未知'
}

/** 把日期字符串转成本地时区的 `YYYY-MM-DD`（趋势图 x 轴用）。 */
export function toISODate(date: Date): string {
  const year = date.getFullYear()
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${year}-${month}-${day}`
}
