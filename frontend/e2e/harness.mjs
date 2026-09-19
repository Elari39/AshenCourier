/**
 * e2e 的断言、报告与 HTTP 播种工具。
 *
 * 「播种」全由 Node 直接打 HTTP 接口完成，而不是在页面里点按钮：浏览器验收要断言的是
 * **页面渲染与版式**，把数据准备也塞进 UI 只会让失败原因变含糊 —— 分不清是
 * 「创建功能坏了」还是「版式坏了」。创建功能本身由 `cmd/smoke` 覆盖。
 *
 * 断言刻意写得比单测宽松（只做正则形状与相对关系，不钉死像素值）：
 * 这些用例要同时跑在 Windows 本机与 Linux CI 上，字体渲染与滚动条宽度都不一样。
 */

/** 断言条件为真。 */
export function assert(condition, message) {
  if (!condition) throw new Error(message)
}

/** 断言严格相等（用 JSON 输出便于看差异）。 */
export function assertEqual(actual, expected, message) {
  if (actual !== expected) {
    throw new Error(
      `${message}\n      期望：${JSON.stringify(expected)}\n      实际：${JSON.stringify(actual)}`,
    )
  }
}

/** 断言浮点数在容差内（版式断言专用；容差 1px 吸收亚像素取整）。 */
export function assertClose(actual, expected, tolerance, message) {
  if (typeof actual !== 'number' || Number.isNaN(actual) || Math.abs(actual - expected) > tolerance) {
    throw new Error(`${message}\n      期望：${expected} ±${tolerance}\n      实际：${actual}`)
  }
}

/** 断言字符串匹配正则。 */
export function assertMatch(actual, pattern, message) {
  if (typeof actual !== 'string' || !pattern.test(actual)) {
    throw new Error(`${message}\n      期望匹配：${pattern}\n      实际：${JSON.stringify(actual)}`)
  }
}

/** 检查收集器：跑完所有检查再统一汇总，有失败就非零退出。 */
export class Checks {
  constructor() {
    this.results = []
  }

  async run(name, fn) {
    try {
      const detail = await fn()
      this.results.push({ name, ok: true })
      console.log(`  ✓ ${name}${detail ? ` —— ${detail}` : ''}`)
    } catch (error) {
      this.results.push({ name, ok: false, error })
      console.log(`  ✗ ${name}`)
      console.log(`      ${error.message}`)
    }
  }

  /** 打印汇总并返回失败数。 */
  summary() {
    const failed = this.results.filter((result) => !result.ok)
    console.log(`\n${this.results.length} 项检查，${failed.length} 项失败。`)
    for (const result of failed) console.log(`  失败：${result.name}`)
    return failed.length
  }
}

/** 生成随机小写字母数字后缀，用于自定义短码（避免重复运行时撞 409）。 */
export function randomSuffix(length = 6) {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let out = ''
  for (let i = 0; i < length; i++) out += alphabet[Math.floor(Math.random() * alphabet.length)]
  return out
}

/** 桌面端 UA：让 device 落在 desktop 而不是 unknown。 */
export const UA_DESKTOP =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36'

/** 极简 JSON 客户端。`redirect: 'manual'` 让我们能看见 302 而不是被静默跟随。 */
export class Api {
  constructor(base) {
    this.base = base.replace(/\/+$/, '')
  }

  async json(method, path, { body, headers } = {}) {
    const res = await fetch(this.base + path, {
      method,
      headers: {
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'manual',
    })
    const text = await res.text()
    let parsed = null
    try {
      parsed = text ? JSON.parse(text) : null
    } catch {
      parsed = null
    }
    return { status: res.status, body: parsed }
  }

  get(path, headers) {
    return this.json('GET', path, { headers })
  }

  post(path, body, headers) {
    return this.json('POST', path, { body, headers })
  }

  patch(path, body, headers) {
    return this.json('PATCH', path, { body, headers })
  }
}

/**
 * 造一条匿名短链（用自定义短码，避免与同一个 job 里的 smoke 抢自动生成的码）。
 *
 * ⚠️ 整个套件只能建**一条**：`POST /api/links` 是 10 次 / 分钟 / IP 的硬配额，
 * 而 `cmd/smoke` 的最后一项检查会故意把这条配额打满（它要断言 429）。
 * 所以这个套件排在 smoke 之前，且只花掉一个配额（见 .github/workflows/ci.yml）。
 * 口令也走 PATCH 设在这一条上，不另建链接。
 */
export async function createLink(api, { code, target, tags = [] }) {
  const res = await api.post('/api/links', {
    target_url: target,
    custom_code: code,
    title: 'e2e 浏览器验收',
    tags,
  })
  assertEqual(res.status, 201, `创建短链失败：HTTP ${res.status} ${JSON.stringify(res.body)}`)
  return { code: res.body.link.short_code, manageKey: res.body.manage_key, link: res.body.link }
}

/**
 * 打一次跳转（不跟随 302），用于播种点击明细。
 *
 * `referer` 建议每次都不同：连续跳转都发生在同一秒、同一个 /24 网段，明细行渲染出来
 * 会**逐字相同**，于是「翻页不重不漏」这件事在页面上没法验证。后端确实按次记录
 * `r.Referer()`，所以给一个唯一的来源就能让每行可区分。
 */
export async function hit(api, code, { referer, userAgent = UA_DESKTOP } = {}) {
  const res = await fetch(`${api.base}/${code}`, {
    redirect: 'manual',
    headers: {
      'user-agent': userAgent,
      ...(referer === undefined ? {} : { referer }),
    },
  })
  assertEqual(res.status, 302, `跳转 /${code} 期望 302，实际 ${res.status}`)
}

/**
 * 等点击明细真正落库。
 *
 * 为什么门控用明细接口而不是 `stats` 的 `total_clicks`：后者是「PG 基线 + Redis 待同步增量」，
 * worker 还没回刷时它就已经等于最终值了 —— 拿它做门控会让断言在明细还没落库时假通过。
 * 明细接口读的是 `click_events`，落库了才看得见。
 */
export async function waitForClicks(api, code, manageKey, expected, { timeoutMs = 90000 } = {}) {
  const deadline = Date.now() + timeoutMs
  let seen = 0
  for (;;) {
    const res = await api.get(`/api/links/${code}/clicks?limit=100&days=30`, {
      'X-Manage-Key': manageKey,
    })
    assertEqual(res.status, 200, `读点击明细失败：HTTP ${res.status} ${JSON.stringify(res.body)}`)
    seen = (res.body.clicks ?? []).length
    if (seen >= expected) return seen
    if (Date.now() >= deadline) {
      throw new Error(`等 ${expected} 条明细落库超时（${timeoutMs}ms），只看到 ${seen} 条`)
    }
    // 明细与统计共用「120 次 / 分钟 / IP×短码」这一条限流规则，
    // 所以放慢到 1.5s 一次（≈40 次/分钟），别把配额烧在轮询上。
    await new Promise((resolve) => setTimeout(resolve, 1500))
  }
}
