#!/usr/bin/env node
/**
 * 浏览器级验收（真 Chrome + CDP，零依赖）。
 *
 * 为什么需要它：`cmd/smoke` 覆盖的是**接口**的正确性，而有一类问题只在真浏览器里存在 ——
 * 二维码画布撑破容器就是最典型的例子（当时只解码 `toDataURL()` 的像素，那永远是 320×320，
 * **版式坏了也照样全绿**，是有人在真浏览器里看了一眼才暴露的，见 README 六条踩过的坑第 6 条）。
 *
 * 用法（需要已经跑起全栈）：
 *   node frontend/e2e/browser-check.mjs --base http://localhost:8080
 *
 * 环境变量：
 *   CHROME_BIN     指定 Chrome 可执行文件（默认按平台猜常见的几个位置）
 *   CHROME_FLAGS   追加启动参数，以空格分隔（例如以 root 运行的容器里需要 --no-sandbox）
 *
 * ⚠️ 只创建 **1 条** 短链：`POST /api/links` 是 10 次/分钟/IP 的硬配额，而 `cmd/smoke`
 *    的最后一项检查会故意把这配额打满（它要断言 429）。所以本套件要么排在 smoke 之前，
 *    要么单独跑 —— CI 里就是「先本套件、后 smoke」，见 `.github/workflows/ci.yml`。
 */
import { launchChrome } from './cdp.mjs'
import { register as registerDetailPage } from './detail-page.mjs'
import { register as registerPasswordGate } from './password-gate.mjs'
import { Api, Checks, assert, createLink, hit, randomSuffix, waitForClicks } from './harness.mjs'

/** 播种的点击条数：刻意大于一页（20），这样才覆盖得到「加载更多」。 */
const CLICKS_TO_SEED = 26

/** 每次跳转换一个来源：明细行渲染出来才有彼此可区分的字段（见 harness.mjs 的 hit）。 */
const SEED_REFERER_PREFIX = 'https://seed.example/click/'

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 2) {
    const key = argv[i]?.replace(/^--/, '')
    if (key) out[key] = argv[i + 1]
  }
  return out
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  const base = (args.base ?? 'http://localhost:8080').replace(/\/+$/, '')
  const api = new Api(base)
  const checks = new Checks()

  // ---- 1. 播种：一条短链 + 一批点击明细 -------------------------------------
  // 全部走 HTTP 接口而不是点 UI：这里要验收的是「页面渲染与版式」，
  // 数据准备混进 UI 只会让失败原因变得含糊（分不清是创建坏了还是版式坏了）。
  const requestedCode = `e2e${randomSuffix(6)}`
  const fixture = await createLink(api, {
    code: requestedCode,
    // 目标地址用本栈自己的 SPA 路由：不解锁那一段要断言「真的落到目标」，
    // 用外部站点既依赖外网，又没法确定性地断言
    target: `${base}/login`,
    tags: ['ops', 'e2e'],
  })
  console.log(`已创建：${base}/${fixture.code}`)

  for (let i = 0; i < CLICKS_TO_SEED; i++) {
    await hit(api, fixture.code, { referer: `${SEED_REFERER_PREFIX}${i}` })
  }
  const seeded = await waitForClicks(api, fixture.code, fixture.manageKey, CLICKS_TO_SEED)
  console.log(`已播种 ${seeded} 条点击明细`)

  const clicksRes = await api.get(`/api/links/${fixture.code}/clicks?limit=100&days=30`, {
    'X-Manage-Key': fixture.manageKey,
  })
  assert(clicksRes.status === 200, `读明细失败：HTTP ${clicksRes.status}`)
  const apiClicks = clicksRes.body.clicks ?? []
  assert(apiClicks.length === seeded, `明细接口回了 ${apiClicks.length} 条，播种的是 ${seeded} 条`)

  // ---- 2. 起浏览器，把管理密钥写进 localStorage -----------------------------
  const { session, stop } = await launchChrome()
  try {
    // 必须先落在目标源下才能写 localStorage（about:blank 的 origin 是 opaque）
    await session.navigate(`${base}/`)
    const stored = await session.evaluate(`(() => {
      const keys = JSON.parse(localStorage.getItem('ashen:manageKeys') || '{}')
      keys[${JSON.stringify(fixture.code)}] = ${JSON.stringify(fixture.manageKey)}
      localStorage.setItem('ashen:manageKeys', JSON.stringify(keys))
      return Object.keys(keys)
    })()`)
    assert(
      Array.isArray(stored) && stored.includes(fixture.code),
      `管理密钥没写进 localStorage（当前键：${JSON.stringify(stored)}）`,
    )

    // ---- 3. 各项检查 -------------------------------------------------------
    await registerDetailPage({
      checks,
      session,
      base,
      fixture,
      expectedClicks: CLICKS_TO_SEED,
      apiClicks,
    })
    await registerPasswordGate({
      checks,
      session,
      base,
      api,
      fixture,
      expectedClicks: CLICKS_TO_SEED,
    })

    // 放在最后：这样它覆盖的是上面所有页面（详情页 + 口令页 + 解锁后的跳转）的累计错误
    await checks.run('全程没有 console.error 与未捕获异常', async () => {
      assert(session.pageErrors.length === 0, `页面报错：\n      ${session.pageErrors.join('\n      ')}`)
    })
  } finally {
    stop()
  }

  const failed = checks.summary()
  console.log(`验收用的短链：${base}/${fixture.code}（${CLICKS_TO_SEED} 条明细）`)
  process.exitCode = failed === 0 ? 0 : 1
}

main().catch((error) => {
  console.error(`\n浏览器验收没能跑完：${error.message}`)
  process.exitCode = 1
})
