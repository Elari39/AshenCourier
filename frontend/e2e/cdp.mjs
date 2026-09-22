/**
 * 无头 Chrome + CDP 的最小封装（零依赖）。
 *
 * 为什么不引 Playwright / Puppeteer：这套验收只需要「导航 → 跑一段 JS → 读回结果与版式矩形」，
 * 而 Node 22+ 自带 `fetch` 与全局 `WebSocket`，于是整个 e2e 目录不装任何 npm 包就能跑 ——
 * CI 里连 `pnpm install` 都不需要（见 `.github/workflows/ci.yml` 的 smoke job）。
 *
 * 代价是断言得自己写（没有 Playwright 的 auto-wait 与选择器重试）。这里的折中是
 * **轮询谓词**而不是固定 `sleep`：`waitFor('…表达式…')` 既比 sleep 快，也不会在慢机器上偶发红。
 */
import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

/** 常见的 Chrome 安装位置；`CHROME_BIN` 永远优先。 */
const CANDIDATES = [
  process.env.CHROME_BIN,
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome', // GitHub 的 ubuntu runner 预装的就在这里
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
].filter((candidate) => Boolean(candidate))

/** 找出可执行的 Chrome；找不到就抛错并列出试过的位置。 */
export function findChrome() {
  for (const candidate of CANDIDATES) {
    if (existsSync(candidate)) return candidate
  }
  throw new Error(`找不到 Chrome；请用 CHROME_BIN 指定可执行文件。已试过：\n  ${CANDIDATES.join('\n  ')}`)
}

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/** 极简 CDP 会话：发命令、按 id 匹配结果、等事件，顺带收集页面错误。 */
export class Session {
  constructor(ws) {
    this.ws = ws
    this.nextId = 1
    this.pending = new Map()
    this.listeners = new Set()
    /**
     * 页面里 `console.error` 与未捕获异常都会记在这里。
     * 「零控制台错误」是一条性价比很高的断言：Vue 的渲染异常、接口 500、
     * 未处理的 Promise 拒绝都会在这里露头，而纯文本 / 像素断言一个都抓不到。
     */
    this.pageErrors = []

    ws.addEventListener('message', (event) => {
      const msg = JSON.parse(event.data)
      if (msg.id !== undefined) {
        const slot = this.pending.get(msg.id)
        if (!slot) return
        this.pending.delete(msg.id)
        if (msg.error) slot.reject(new Error(`CDP 调用失败：${msg.error.message}`))
        else slot.resolve(msg.result)
        return
      }
      this.trackPageError(msg)
      for (const fn of this.listeners) fn(msg)
    })
  }

  /** 把页面侧的错误记进 `pageErrors`（只看 error 级别的 console 与未捕获异常）。 */
  trackPageError(msg) {
    if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
      const text = (msg.params.args ?? []).map((arg) => arg.value ?? arg.description ?? arg.type).join(' ')
      this.pageErrors.push(`console.error: ${text}`)
      return
    }
    if (msg.method === 'Runtime.exceptionThrown') {
      const details = msg.params.exceptionDetails
      this.pageErrors.push(`未捕获异常: ${details.exception?.description ?? details.text}`)
    }
  }

  send(method, params = {}) {
    const id = this.nextId++
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject })
      this.ws.send(JSON.stringify({ id, method, params }))
    })
  }

  /** 等一个 CDP 事件（一次性）。 */
  once(method, timeoutMs = 15000) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.listeners.delete(fn)
        reject(new Error(`等 CDP 事件 ${method} 超时（${timeoutMs}ms）`))
      }, timeoutMs)
      const fn = (msg) => {
        if (msg.method !== method) return
        clearTimeout(timer)
        this.listeners.delete(fn)
        resolve(msg.params)
      }
      this.listeners.add(fn)
    })
  }

  /** 在页面里求值并取回结果（支持 await）。页面内抛错会转成 Node 侧异常。 */
  async evaluate(expression) {
    const result = await this.send('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise: true,
    })
    if (result.exceptionDetails) {
      const details = result.exceptionDetails
      throw new Error(`页面内执行 JS 抛错：${details.exception?.description ?? details.text}`)
    }
    return result.result.value
  }

  /** 轮询一个返回布尔值的表达式，直到为真或超时。 */
  async waitFor(expression, { timeoutMs = 20000, label = expression, intervalMs = 150 } = {}) {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      if (await this.evaluate(expression)) return
      if (Date.now() >= deadline) throw new Error(`等「${label}」超时（${timeoutMs}ms）`)
      await sleep(intervalMs)
    }
  }

  /** 导航并等 load 事件。 */
  async navigate(url) {
    const loaded = this.once('Page.loadEventFired')
    await this.send('Page.navigate', { url })
    await loaded
  }

  close() {
    try {
      this.ws.close()
    } catch {
      /* 已经关了就算了 */
    }
  }
}

async function connect(wsUrl) {
  const ws = new WebSocket(wsUrl)
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true })
    ws.addEventListener('error', () => reject(new Error(`连不上 CDP WebSocket：${wsUrl}`)), { once: true })
  })
  return new Session(ws)
}

/**
 * 起一个无头 Chrome 并连上第一个目标页。
 *
 * `CHROME_FLAGS` 可追加启动参数（以空格分隔），例如在某些以 root 运行的容器里需要 `--no-sandbox`。
 *
 * `extraFlags` 是给单个调用点的追加参数（在 `CHROME_FLAGS` **之后**），
 * 目前只有一个用途：把 e2e 自己造的域名映射到回环（`--host-resolver-rules`），
 * 见 browser-check.mjs 里 `TARGET_HOST` 的说明。
 */
export async function launchChrome({ port = 9333, windowSize = '1440,1100', extraFlags = [] } = {}) {
  const profile = mkdtempSync(join(tmpdir(), 'ashen-e2e-'))
  const extra = (process.env.CHROME_FLAGS ?? '').split(' ').filter(Boolean)
  const child = spawn(
    findChrome(),
    [
      '--headless=new',
      '--disable-gpu',
      '--no-first-run',
      '--no-default-browser-check',
      // CI 容器里 /dev/shm 常常只有 64MB，不关掉这个 Chrome 会随机崩
      '--disable-dev-shm-usage',
      `--remote-debugging-port=${port}`,
      `--user-data-dir=${profile}`,
      `--window-size=${windowSize}`,
      ...extra,
      ...extraFlags,
      'about:blank',
    ],
    { stdio: 'ignore' },
  )

  const cdp = `http://127.0.0.1:${port}`
  const deadline = Date.now() + 20000
  let ready = false
  while (!ready && Date.now() < deadline) {
    try {
      const res = await fetch(`${cdp}/json/version`)
      ready = res.ok
    } catch {
      /* DevTools 端点还没起来 */
    }
    if (!ready) await sleep(200)
  }

  if (!ready) {
    child.kill()
    throw new Error('Chrome 的 DevTools 端点没起来（检查 CHROME_BIN 与 CHROME_FLAGS）')
  }

  const target = await (await fetch(`${cdp}/json/new?about:blank`, { method: 'PUT' })).json()
  const session = await connect(target.webSocketDebuggerUrl)
  await session.send('Page.enable')
  await session.send('Runtime.enable')
  /**
   * 让无头页面表现得像「窗口真的拿到了焦点」。
   *
   * 不加这一句，无头 Chrome 里 `document.hasFocus()` 是 false，`:focus` 这个伪类
   * 根本不会匹配 —— 而「跳到主要内容」那条链接平时藏在视口外、**聚焦时靠 `:focus`
   * 移进来**，于是断言会得到「已经 focus() 了，top 却还是 -100」这种看着像
   * 代码写错、其实是环境没配好的结果（2026-09-21 实际踩到）。
   * CDP 专门有这个开关：Emulation.setFocusEmulationEnabled。
   */
  await session.send('Emulation.setFocusEmulationEnabled', { enabled: true })

  return {
    session,
    profilePath: profile,
    /**
     * 收尾：关掉 CDP 会话、结束 Chrome。
     *
     * ⚠️ 刻意**不删**临时 profile。一个 Chrome profile 有几千个文件，在本机沙箱里
     * 递归删除会触发「批量删除守卫」，把整个命令连同已打印的输出一起带走 ——
     * 实测到的症状是：所有检查都 ✓，但汇总那一行永远打不出来，退出码却是 1。
     * 目录落在系统临时目录里，交给系统的临时文件清理即可；
     * 对验收脚本来说，「结论能打出来」比「删干净」重要得多。
     */
    stop() {
      session.close()
      child.kill()
      console.log(`已结束无头 Chrome（临时 profile 保留在 ${profile}，交系统临时目录回收）`)
    },
  }
}
