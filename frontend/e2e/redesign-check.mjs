import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { launchChrome } from './cdp.mjs'
import { realClick } from './input.mjs'
import { Api, assert, randomSuffix } from './harness.mjs'

const base = process.argv[2] || 'http://localhost:18083'
const out = resolve('docs/screenshots/neo-brutalism')
mkdirSync(out, { recursive: true })
const api = new Api(base)
const { session, stop } = await launchChrome({ port: 9334, extraFlags: ['--no-proxy-server'] })
const report = []
const button = (text) => `Array.from(document.querySelectorAll('button')).find(e => e.textContent.trim() === ${JSON.stringify(text)})`
async function fill(selector, text) {
  await realClick(session, `document.querySelector(${JSON.stringify(selector)})`, selector)
  await session.send('Input.insertText', { text })
}
async function page(path, ready = 'document.querySelector("h1")') {
  await session.navigate(base + path)
  await session.waitFor(ready)
  await session.evaluate('document.fonts.ready.then(() => true)')
}
async function capture(name, path, ready) {
  for (const width of [390, 768, 1440]) {
    await session.send('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: false })
    await page(path, ready)
    const overflow = await session.evaluate('document.documentElement.scrollWidth > innerWidth + 1')
    assert(!overflow, `${name} overflows at ${width}px`)
    const clippedControls = await session.evaluate(`Array.from(document.querySelectorAll('header button,header a')).filter(e=>e.getClientRects().length).filter(e=>{const r=e.getBoundingClientRect();return r.left<0 || r.right>document.documentElement.clientWidth+1}).length`)
    assert(clippedControls === 0, `${name} header clips controls at ${width}px`)
    const shot = await session.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true })
    writeFileSync(resolve(out, `${name}-${width}.png`), Buffer.from(shot.data, 'base64'))
    report.push(`${name} ${width}px: no overflow`)
  }
}
try {
  await capture('landing', '/')
  await capture('login', '/login')
  await capture('register', '/register')
  await capture('not-found', '/page/does-not-exist')
  await page('/')
  await fill('input[type=url]', 'https://example.com/anonymous-audit')
  await realClick(session, button('生成短链'), 'create anonymous')
  await session.waitFor('Object.keys(JSON.parse(localStorage.getItem("ashen:manageKeys") || "{}")).length > 0')
  const keys = await session.evaluate('JSON.parse(localStorage.getItem("ashen:manageKeys"))')
  const code = Object.keys(keys)[0]
  report.push('Anonymous creation via UI and management key persistence: passed')
  await page('/register')
  const email = `audit-${randomSuffix()}@example.com`
  const password = 'Audit-browser-strong-password'
  await fill('input[type=email]', email)
  await fill('input[autocomplete=new-password]', password)
  await fill('input[placeholder="再输入一次"]', password)
  await realClick(session, button('注册并开始'), 'register')
  await session.waitFor('location.pathname === "/dashboard" && document.querySelector("h1")?.textContent.includes("我的链接")')
  report.push('Registration via UI: passed')
  await page(`/links/${code}`, 'document.querySelector("canvas")')
  await realClick(session, button('认领到我的账号'), 'claim')
  await session.waitFor('!JSON.parse(localStorage.getItem("ashen:manageKeys"))["'+code+'"]')
  report.push('Anonymous link claim via UI: passed')
  await capture('dashboard', '/dashboard', 'document.querySelector("table tbody tr")')
  await capture('detail', `/links/${code}`, 'document.querySelector("canvas") && document.querySelector("svg .chart-line")')
  await session.evaluate(`window.auditFetch = window.fetch; window.fetch = (url, options) => String(url).includes('/stats?') ? Promise.resolve(new Response(JSON.stringify({error:{code:'unavailable',message:'统计暂不可用'}}), {status:503,headers:{'content-type':'application/json'}})) : window.auditFetch(url, options)`)
  await realClick(session, button('7 天'), 'change stats window')
  await session.waitFor('document.body.textContent.includes("统计暂不可用")')
  assert(await session.evaluate('document.querySelector("main").textContent.includes("—")'), 'failed stats should show unknown, not zero')
  await session.evaluate('window.fetch = window.auditFetch; delete window.auditFetch')
  await realClick(session, button('重试统计'), 'retry statistics')
  await session.waitFor('!document.body.textContent.includes("统计暂不可用") && document.querySelector(".chart-line")')
  report.push('Statistics failure shows unknown values and retry recovers: passed')
  await realClick(session, button('编辑'), 'edit')
  await fill('input[placeholder="给这条链接起个名字"]', '我的分享笔记')
  await realClick(session, button('保存'), 'save')
  await session.waitFor('document.querySelector("main").textContent.includes("我的分享笔记") && !Array.from(document.querySelectorAll("button")).some(e => e.textContent.trim() === "保存")')
  report.push('Edit via UI: passed')
  await session.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: out })
  await realClick(session, button('下载二维码'), 'download QR')
  await session.waitFor('document.body.textContent.includes("二维码已下载")')
  report.push('QR download via UI: passed')
  await page('/dashboard', 'document.querySelector("table tbody tr")')
  await fill('input[aria-label="按标签筛选"]', 'never-matches-audit')
  await session.waitFor('document.body.textContent.includes("没有匹配的链接")')
  await fill('input[type=url]', 'https://example.com/filtered-create')
  await realClick(session, button('生成短链'), 'create with filter active')
  await session.waitFor('document.body.textContent.includes("已生成") && document.body.textContent.includes("没有匹配的链接")')
  assert(await session.evaluate('document.querySelectorAll("table tbody tr").length === 0'), 'unmatched created link inserted into filtered list')
  report.push('Creation under active tag filter preserves matching results: passed')
  const token = await session.evaluate('localStorage.getItem("ashen:token")')
  await api.patch(`/api/links/${code}`, { password: 'first-password' }, { Authorization: `Bearer ${token}` })
  await capture('password', `/${code}`, 'document.querySelector("input[type=password]")')
  const unlocked = await fetch(base + '/' + code, { method: 'POST', body: new URLSearchParams({ password: 'first-password' }), redirect: 'manual' })
  const cookie = unlocked.headers.get('set-cookie').split(';')[0]
  assert(unlocked.status === 303, 'unlock failed')
  await api.patch(`/api/links/${code}`, { password: 'second-password' }, { Authorization: `Bearer ${token}` })
  const revoked = await fetch(base + '/' + code, { headers: { Cookie: cookie }, redirect: 'manual' })
  assert(revoked.status === 200 && (await revoked.text()).includes('password'), 'old password cookie survived change')
  report.push('Password change revokes existing cookie: passed')
  await api.patch(`/api/links/${code}`, { status: 'disabled' }, { Authorization: `Bearer ${token}` })
  await capture('expired', `/${code}`)
  await page('/')
  await session.evaluate('localStorage.removeItem("ashen:token"); localStorage.removeItem("ashen:user")')
  await page('/login')
  await fill('input[type=email]', email)
  await fill('input[type=password]', password)
  await realClick(session, button('登录'), 'login')
  await session.waitFor('location.pathname === "/dashboard"')
  report.push('Login via UI: passed')
  assert(session.pageErrors.length === 0, `Console errors: ${JSON.stringify(session.pageErrors)}`)
  writeFileSync(resolve(out, 'verification.txt'), report.join('\n') + '\n')
  console.log(report.join('\n'))
} finally { stop() }
