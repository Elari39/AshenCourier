#!/usr/bin/env node
/**
 * 浏览器级验收（真 Chrome + CDP，零依赖）。
 *
 * 为什么需要它：`cmd/smoke` 覆盖的是**接口**的正确性，而有一类问题只在真浏览器里存在 ——
 * 二维码画布撑破容器就是最典型的例子（当时只解码 `toDataURL()` 的像素，那永远是 320×320，
 * **版式坏了也照样全绿**，是有人在真浏览器里看了一眼才暴露的，见 README 七条踩过的坑第 6 条）。
 *
 * 用法（需要已经跑起全栈）：
 *   node frontend/e2e/browser-check.mjs --base http://localhost:8080
 *
 * 环境变量：
 *   CHROME_BIN     指定 Chrome 可执行文件（默认按平台猜常见的几个位置）
 *   CHROME_FLAGS   追加启动参数，以空格分隔（例如以 root 运行的容器里需要 --no-sandbox）
 *
 * ⚠️ **本套件只创建 1 条短链，这个数字是算过的，别随手加。**
 *    `POST /api/links` 是 10 次/分钟/IP 的硬配额，`cmd/smoke` 自己要用掉约 **7** 次
 *    （匿名创建 / javascript 被拒 / 自定义短码 / 重复短码 / 保留字 / 登录用户创建 /
 *    限流那一项的循环），最后还会故意把这配额打满来断言 429。
 *    也就是说留给本套件的余量只有 2 次 —— 多创建一条，CI 就变成「时序稍有偏差就偶发 429」。
 *    所以本套件里**不新增创建**：要验的行为优先用接口播种（那条短链在套件一开始就建好，
 *    smoke 跑起来时它早已滚出限流窗口），验不到的一律下沉到 vitest。
 *    所以本套件必须排在 smoke 之前，见 `.github/workflows/ci.yml`。
 */
import { launchChrome } from './cdp.mjs'
import { register as registerA11y } from './a11y.mjs'
import { register as registerConfirmDialog } from './confirm-dialog.mjs'
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

  /**
   * **浏览器侧**统一使用的 origin（Node 侧打接口仍用 `base`，不变）。
   *
   * 为什么不直接用 `base`：后端默认**拒绝**指向内网/回环的目标
   * （`localhost`、`127.0.0.1`、`10.x`、`169.254.169.254` …，见 README「已知限制」）——
   * 那是对的：公网短链指向访客自己的 localhost 毫无意义，只可能是拿它当内网跳板。
   * 而本套件的短链目标就是本栈自己的 SPA 路由（这样「到底落到哪」能确定性断言，
   * 也不依赖外网），于是换一个「对服务端而言是普通域名、对 Chrome 而言被显式映射到
   * 回环」的名字，靠 `--host-resolver-rules` 让它真的可达。
   *
   * ⚠️ 换的必须是**浏览器侧的整个 origin**，不能只换目标地址 ——
   *    目标与页面不同源时，CSP 的 `form-action 'self'` 会拦下「表单提交后重定向到
   *    别的源」。实测症状极具误导性：提交口令后 `net::ERR_ABORTED`，既不导航也不
   *    报错（CSP 违规走 Log 域，不进 `console.error`，「零 console 错误」照样绿），
   *    看着像 303 没生效，其实是策略在正确工作。所以口令页与目标必须同源。
   *
   * 这样 e2e 跑在**默认（最严）配置**下 —— CI 验收的就是生产形态，
   * 不必为了跑测试打开 ALLOW_PRIVATE_TARGETS（那会让这条策略在 CI 里无人守）。
   *
   * `.test` 是 RFC 2606 保留的顶级域，永远不会与真实域名撞车。
   * base 本身是公网域名时（比如对着线上跑）不需要别名，两者相同。
   */
  const baseURL = new URL(base)
  const baseIsLoopback = ['localhost', '127.0.0.1', '[::1]', '::1'].includes(baseURL.hostname)
  const aliasHost = 'e2e.ashen.test'
  const browserOrigin = baseIsLoopback
    ? `${baseURL.protocol}//${aliasHost}${baseURL.port ? `:${baseURL.port}` : ''}`
    : baseURL.origin

  // ---- 1. 播种：一条短链 + 一批点击明细 -------------------------------------
  // 全部走 HTTP 接口而不是点 UI：这里要验收的是「页面渲染与版式」，
  // 数据准备混进 UI 只会让失败原因变得含糊（分不清是创建坏了还是版式坏了）。
  const requestedCode = `e2e${randomSuffix(6)}`
  const fixture = await createLink(api, {
    code: requestedCode,
    // 目标地址见上面 browserOrigin 的说明：不解锁那一段要断言「真的落到目标」，
    // 用外部站点既依赖外网，又没法确定性地断言
    target: `${browserOrigin}/login`,
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
  const { session, stop } = await launchChrome({
    // 让浏览器侧那个别名 origin 真的解析到本栈（见上面 browserOrigin 的说明）
    // ⚠️ `--no-proxy-server` 不是可选项：本机若配了系统代理，Chrome 会把
    //    `e2e.ashen.test` 交给**代理**去解析，而 `--host-resolver-rules` 只管
    //    Chrome 自己的解析 —— 实测到的症状是目标页变成「HTTP ERROR 502」
    //    （代理无法解析这个名字），看起来像 DNS 没配好。本套件每个请求都打本栈，
    //    直连就行。（CI 的 runner 没有代理，但本机会有，两边行为必须一致。）
    extraFlags: baseIsLoopback
      ? [`--host-resolver-rules=MAP ${aliasHost} 127.0.0.1`, '--no-proxy-server']
      : [],
  })
  try {
    // 必须先落在目标源下才能写 localStorage（about:blank 的 origin 是 opaque）
    // 从这里开始，**浏览器侧的一切都用 browserOrigin**（见上面的说明）
    await session.navigate(`${browserOrigin}/`)
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
    // 不需要短链，所以放在依赖短链的那几组之前 —— 它只用首页、注册页与顶栏
    await registerA11y({ checks, session, base: browserOrigin })

    await registerDetailPage({
      checks,
      session,
      base: browserOrigin,
      fixture,
      expectedClicks: CLICKS_TO_SEED,
      apiClicks,
    })
    await registerPasswordGate({
      checks,
      session,
      base: browserOrigin,
      api,
      fixture,
      expectedClicks: CLICKS_TO_SEED,
    })

    // ⚠️ 必须排在最后（在「零 console 错误」之前）：它的最后一步会**真的删掉**这条短链。
    //    上面两组检查都要用这条短链，顺序反了它们就会拿到 404。
    await registerConfirmDialog({ checks, session, base: browserOrigin, api, fixture })

    // 放在最后：这样它覆盖的是上面所有页面（详情页 + 口令页 + 解锁后的跳转 + 确认框）的累计错误
    await checks.run('全程没有 console.error 与未捕获异常', async () => {
      assert(session.pageErrors.length === 0, `页面报错：\n      ${session.pageErrors.join('\n      ')}`)
    })
  } finally {
    stop()
  }

  const failed = checks.summary()
  console.log(`验收用的短链：${base}/${fixture.code}（${CLICKS_TO_SEED} 条明细，最后由确认框那一组删掉）`)
  process.exitCode = failed === 0 ? 0 : 1
}

main().catch((error) => {
  console.error(`\n浏览器验收没能跑完：${error.message}`)
  process.exitCode = 1
})
