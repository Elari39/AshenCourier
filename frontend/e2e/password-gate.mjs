/**
 * 访问口令的浏览器检查（M5-1 / N2）。
 *
 * `cmd/smoke` 已经用 HTTP 覆盖了状态码与计数（未解锁 200 口令页 / 错误 401 / 正确 303
 * / `stats.total_clicks` 恰好 +1）。这里补的是**只有真浏览器才成立**的部分：
 *
 *   - `POST /{code}` 走原生表单提交，浏览器会不会真的按 303 换成 GET 去取？
 *   - 303 之后那个 GET 是否带上了解锁 cookie？cookie 的 HttpOnly 有没有真的生效？
 *   - 最后有没有真的落到目标地址？
 *   - 「解锁恰好只计一次点击」换一个口径复核：落库的**明细行数**恰好 +1
 *     （明细靠 `event_uid` 幂等去重，行数不会出现瞬时翻倍这种抖动，比读计数更稳）
 *
 * 目标地址刻意用本栈自己的 SPA 路由而不是外部站点：验收因此不依赖外网，
 * 而且「到底落到哪」能用一个确定性的 URL 断言。
 *
 * ⚠️ 这里的 `base` 是**浏览器侧**的 origin，由 browser-check.mjs 传入 ——
 * 回环部署下它是一个被 `--host-resolver-rules` 映射到本栈的别名域名，而不是
 * `localhost`（后端默认拒绝指向内网/回环的目标）。**必须与口令页同源**，
 * 否则 CSP 的 `form-action 'self'` 会拦掉「提交后重定向到别的源」。
 * 理由见 browser-check.mjs 里 `browserOrigin` 的注释。
 */
import { assert, assertEqual, waitForClicks } from './harness.mjs'

const PASSWORD = 'e2e-pass-9f3a'

/** 填口令并点提交（原生表单，不用 JS 拦截，尽量贴近真实用户）。 */
function submitScript(password) {
  return `(() => {
    const input = document.querySelector('input#password')
    if (!input) return false
    input.value = ${JSON.stringify(password)}
    const button = document.querySelector('form button[type="submit"]')
    if (!button) return false
    button.click()
    return true
  })()`
}

/** 点提交后等一次 load 事件（成功与失败都会导航，两条路径共用它）。 */
async function submitPassword(session, password) {
  const loaded = session.once('Page.loadEventFired', 20000)
  const clicked = await session.evaluate(submitScript(password)).catch((error) => {
    // 点击会立刻触发导航，求值本身可能因为「执行上下文被销毁」而抛错 —— 那属于预期。
    // 其他错误（例如页面上根本没有口令输入框）必须冒出来，不能被这里吞掉。
    if (/context|destroyed/i.test(error.message)) return null
    throw error
  })
  if (clicked === false) throw new Error('口令页上找不到 input#password 或提交按钮')
  await loaded
}

/** 读当前的解锁 cookie（HttpOnly 的 cookie 在页面里读不到，只能走 CDP）。 */
async function readUnlockCookie(session, base) {
  const result = await session.send('Network.getCookies', { urls: [base] })
  return (result.cookies ?? []).find((cookie) => cookie.name === 'ac_unlock') ?? null
}

export async function register({ checks, session, base, api, fixture, expectedClicks }) {
  console.log('\n访问口令')

  // 口令设在这一条已经建好的链接上：创建接口是 10 次/分钟的硬配额，
  // 不能再花一个配额（见 harness.mjs 里 createLink 的注释）。
  const patched = await api.patch(
    `/api/links/${fixture.code}`,
    { password: PASSWORD },
    { 'X-Manage-Key': fixture.manageKey },
  )
  assertEqual(patched.status, 200, `设置口令失败：HTTP ${patched.status} ${JSON.stringify(patched.body)}`)

  await checks.run('设置口令后：详情接口只回布尔，不回摘要也不回明文', async () => {
    const detail = await api.get(`/api/links/${fixture.code}`, { 'X-Manage-Key': fixture.manageKey })
    assertEqual(detail.status, 200, `读详情失败：HTTP ${detail.status}`)
    // 详情与 PATCH 的响应体都是 link DTO 本身（不像创建那样包一层 {link: ...}）
    assertEqual(detail.body.password_protected, true, 'password_protected 不是 true')
    const raw = JSON.stringify(detail.body)
    assert(!raw.includes('password_hash'), '响应体里出现了 password_hash')
    assert(!raw.includes(PASSWORD), '响应体里出现了口令明文')
  })

  await session.send('Network.enable')
  // 从「干净的浏览器」开始：否则上一阶段的 cookie 会让未解锁的断言假通过
  await session.send('Network.clearBrowserCookies')

  await checks.run('未解锁访问：渲染口令页而不是跳转，且不给 cookie', async () => {
    await session.navigate(`${base}/${fixture.code}`)
    const state = await session.evaluate(`({
      path: location.pathname,
      hasInput: Boolean(document.querySelector('input#password')),
      text: document.body.innerText,
    })`)
    assertEqual(state.path, `/${fixture.code}`, '不该离开口令页')
    assert(state.hasInput, '口令页上没有 input#password')
    assert(state.text.includes('这条短链需要口令'), '口令页没有标题文案')
    assert(!state.text.includes('口令不对'), '首次访问不该出现「口令不对」')
    assertEqual(await readUnlockCookie(session, base), null, '未解锁就不该下发 ac_unlock')
  })

  await checks.run('错误口令：仍是口令页，并给出错误提示', async () => {
    await submitPassword(session, 'wrong-guess-1')
    const state = await session.evaluate(`({ path: location.pathname, text: document.body.innerText })`)
    assertEqual(state.path, `/${fixture.code}`, '错误口令不该跳走')
    assert(state.text.includes('口令不对，请再试一次。'), '口令页没有显示「口令不对」提示')
  })

  await checks.run('正确口令：303 换成 GET，最终落到目标地址', async () => {
    await submitPassword(session, PASSWORD)
    const state = await session.evaluate(`({ href: location.href, text: document.body.innerText })`)
    assertEqual(state.href, `${base}/login`, '没有落到目标地址')
    assert(!state.text.includes('这条短链需要口令'), '仍然停在口令页（cookie 没生效？）')
  })

  await checks.run('解锁 cookie 已下发，且 HttpOnly 真的生效', async () => {
    const unlock = await readUnlockCookie(session, base)
    assert(unlock, '没有下发 ac_unlock cookie')
    assertEqual(unlock.httpOnly, true, 'ac_unlock 不是 HttpOnly')
    assertEqual(unlock.path, '/', `ac_unlock 的 Path 是 ${unlock.path}，应为 /`)
    return `Path=${unlock.path} SameSite=${unlock.sameSite} Secure=${unlock.secure}`
  })

  await checks.run('一次解锁恰好只多一条点击明细（不是 0 条，也不是 2 条）', async () => {
    const rows = await waitForClicks(api, fixture.code, fixture.manageKey, expectedClicks + 1)
    assertEqual(
      rows,
      expectedClicks + 1,
      `明细行数应为 ${expectedClicks + 1}（解锁后那个 GET 计一次），实际 ${rows}`,
    )
    return `${expectedClicks} → ${rows} 条`
  })
}
