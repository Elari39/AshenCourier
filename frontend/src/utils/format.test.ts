/**
 * `@/utils/format` 的单测。
 *
 * 两条刻意的写法：
 *
 * 1. **时间输入不带时区后缀**（`2026-09-18T13:05:42` 而不是 `…Z`）。JS 对无时区串
 *    按本地时间解析，格式化回来仍是同一个钟点 —— 于是断言与本机 / CI 的 TZ 无关。
 *    写成带 `Z` 的串会让这套用例在 `Asia/Shanghai`（本机）与 `UTC`（CI）上得到不同结果。
 * 2. `isExpired` / `describeExpiry` 依赖 `Date.now()`，用假定时器把「现在」钉死，
 *    否则「3 天后过期」这类用例会在执行时间的边界上偶发红。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  describeClient,
  describeDevice,
  describeExpiry,
  describeReferer,
  describeStatus,
  formatDateTime,
  formatDateTimeSeconds,
  formatNumber,
  formatShortDate,
  hostOf,
  isExpired,
  truncateMiddle,
} from '@/utils/format'

afterEach(() => {
  vi.useRealTimers()
})

/** 把「现在」固定在 2026-09-18 12:00:00（本地时区）。 */
function freezeNow(): void {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-09-18T12:00:00'))
}

describe('formatDateTime / formatDateTimeSeconds', () => {
  it('按 zh-CN 的 24 小时制格式化', () => {
    expect(formatDateTime('2026-09-18T13:05:42')).toBe('2026/09/18 13:05')
    expect(formatDateTimeSeconds('2026-09-18T13:05:42')).toBe('2026/09/18 13:05:42')
  })

  it('空值与非法值都退到占位符，而不是抛异常或显示 Invalid Date', () => {
    expect(formatDateTime(undefined)).toBe('—')
    expect(formatDateTime(null)).toBe('—')
    expect(formatDateTime('')).toBe('—')
    expect(formatDateTime('not-a-date')).toBe('—')
    expect(formatDateTimeSeconds('not-a-date')).toBe('—')
  })

  it('占位符可覆盖', () => {
    expect(formatDateTime(undefined, '无')).toBe('无')
    expect(formatDateTimeSeconds(null, '无')).toBe('无')
  })
})

describe('formatShortDate', () => {
  it('补零成 MM-DD（趋势图刻度用）', () => {
    expect(formatShortDate('2026-09-08T10:00:00')).toBe('09-08')
  })

  // 后端 daily[].date 就是这种**纯日期**串。老实现用 new Date() 解析它，
  // 在负偏移时区（美洲）会整体差一天 —— 而带时间的串不会，所以那条用例
  // 从来没抓住这个 bug。这条是真正的回归守卫。
  it('纯日期串不会随时区偏移一天（后端 daily.date 就是这个形状）', () => {
    expect(formatShortDate('2026-09-01')).toBe('09-01')
    expect(formatShortDate('2026-01-01')).toBe('01-01')
    expect(formatShortDate('2026-12-31')).toBe('12-31')
  })

  it('带时间与时区后缀时取的仍是 UTC 那一天', () => {
    expect(formatShortDate('2026-09-01T00:00:00Z')).toBe('09-01')
    expect(formatShortDate('2026-09-01T23:59:59Z')).toBe('09-01')
  })

  it('解析不了时原样返回 —— 图上宁可显示原始串，也不要 Invalid Date', () => {
    expect(formatShortDate('nope')).toBe('nope')
    expect(formatShortDate('')).toBe('')
  })

  // 上面那条「纯日期串」在 UTC 与 Asia/Shanghai 下**即使代码有 bug 也是绿的**
  // （正偏移时区里 UTC 午夜还是同一天）。真正会踩的是美洲那种负偏移，
  // 所以这里临时把 TZ 切到 America/New_York 复现它 —— 否则这条用例等于没写。
  it('负偏移时区（America/New_York）下纯日期串不提前一天', () => {
    const prev = process.env.TZ
    process.env.TZ = 'America/New_York'
    try {
      expect(formatShortDate('2026-09-01')).toBe('09-01')
    } finally {
      process.env.TZ = prev
    }
  })
})

describe('formatNumber', () => {
  it('千分位', () => {
    expect(formatNumber(1234567)).toBe('1,234,567')
    expect(formatNumber(0)).toBe('0')
  })
})

describe('hostOf', () => {
  it('取主机名', () => {
    expect(hostOf('https://news.example.com/post/1')).toBe('news.example.com')
  })

  it('不是 URL 时原样返回', () => {
    expect(hostOf('随手写的一串字')).toBe('随手写的一串字')
  })
})

describe('truncateMiddle', () => {
  it('长度不超过 head+tail+1 时不动', () => {
    const exact = 'a'.repeat(53) // 默认 head=34 / tail=18
    expect(truncateMiddle(exact)).toBe(exact)
  })

  it('超过一个字符就开始中段省略（保留首尾，域名后缀不会被吃掉）', () => {
    const longer = `${'a'.repeat(34)}${'b'.repeat(20)}` // 54 字符，刚好越过 53 的阈值
    expect(truncateMiddle(longer)).toBe(`${'a'.repeat(34)}…${'b'.repeat(18)}`)
  })

  it('首尾长度可覆盖', () => {
    expect(truncateMiddle('0123456789', 3, 2)).toBe('012…89')
  })
})

describe('isExpired', () => {
  it('没设过期时间 = 永不过期', () => {
    expect(isExpired(undefined)).toBe(false)
    expect(isExpired(null)).toBe(false)
  })

  it('非法值不算过期（不能因为解析失败就把链接判死）', () => {
    expect(isExpired('nope')).toBe(false)
  })

  it('过去为真、未来为假；恰好等于现在算已过期', () => {
    freezeNow()
    expect(isExpired('2026-09-18T11:59:59')).toBe(true)
    expect(isExpired('2026-09-18T12:00:00')).toBe(true)
    expect(isExpired('2026-09-18T12:00:01')).toBe(false)
  })
})

describe('describeExpiry', () => {
  it('无值与非法值各有各的文案', () => {
    expect(describeExpiry(undefined)).toBe('永久有效')
    expect(describeExpiry('nope')).toBe('有效期未知')
  })

  it('按「天 → 小时 → 不到 1 小时」分档', () => {
    freezeNow()
    expect(describeExpiry('2026-09-17T12:00:00')).toBe('已过期')
    expect(describeExpiry('2026-09-21T12:00:00')).toBe('3 天后过期')
    expect(describeExpiry('2026-09-19T12:00:00')).toBe('1 天后过期')
    expect(describeExpiry('2026-09-18T18:30:00')).toBe('6 小时后过期')
    expect(describeExpiry('2026-09-18T12:30:00')).toBe('不到 1 小时后过期')
  })
})

describe('describeStatus', () => {
  it('三种已知状态', () => {
    expect(describeStatus('active')).toEqual({ label: '正常', tone: 'ok' })
    expect(describeStatus('disabled')).toEqual({ label: '已停用', tone: 'muted' })
    expect(describeStatus('deleted')).toEqual({ label: '已删除', tone: 'error' })
  })

  it('未知状态原样透出（宁可显示原始值，也不要静默说成「正常」）', () => {
    expect(describeStatus('archived')).toEqual({ label: 'archived', tone: 'muted' })
  })
})

describe('describeReferer / describeDevice / describeClient', () => {
  it('空 referer 是直接访问', () => {
    expect(describeReferer('')).toBe('直接访问')
    expect(describeReferer('https://t.co/abc')).toBe('https://t.co/abc')
  })

  it('设备标识译成中文，未知项原样透出', () => {
    expect(describeDevice('desktop')).toBe('桌面端')
    expect(describeDevice('mobile')).toBe('手机')
    expect(describeDevice('tablet')).toBe('平板')
    expect(describeDevice('bot')).toBe('爬虫 / 机器人')
    expect(describeDevice('unknown')).toBe('未知')
    expect(describeDevice('tv')).toBe('tv')
  })

  it('浏览器 / 系统组合：两边都缺只显示一个「未知」', () => {
    expect(describeClient('Chrome', 'macOS')).toBe('Chrome / macOS')
    expect(describeClient('Chrome', 'unknown')).toBe('Chrome')
    expect(describeClient('unknown', 'iOS')).toBe('iOS')
    expect(describeClient('unknown', 'unknown')).toBe('未知')
    expect(describeClient(undefined, undefined)).toBe('未知')
    expect(describeClient('', '')).toBe('未知')
  })
})
