/**
 * 确认框与 toast 的浏览器检查（B3 引入）。
 *
 * 为什么这件事非上真浏览器不可：确认框的行为**几乎全是浏览器行为** ——
 * 焦点落在哪、Tab 能不能跑出框、Esc 会不会被别处先吃掉、关掉之后焦点回到哪。
 * 这些在 node 环境的单测里一条都验不到（本项目刻意不引 jsdom），
 * 而它们恰恰是「从 window.confirm 换成自绘对话框」最容易丢的东西：
 * 原生确认框的焦点归位与 Esc 是浏览器白送的，换成自己的 div 之后全部得自己实现。
 *
 * ⚠️ 两条工程细节（都踩过）：
 *  ① 点击用 CDP 合成鼠标事件，不用 `element.click()` —— 后者**不移动焦点**，
 *     而「关闭后焦点还给触发元素」这条断言的前提，就是焦点真的到过那个按钮。
 *  ② 定位前先 `scrollIntoView({behavior:'instant'})`：全局 CSS 里 scroll-behavior
 *     是 smooth，平滑滚动途中量到的矩形是错的（会点到别的元素上）。
 *
 * ⚠️ 本模块**最后一步真的会删掉这条短链**（DELETE 本身就是要验的路径之一），
 *    所以它必须排在 browser-check.mjs 里「零 console 错误」那一条之前、
 *    且必须排在所有依赖这条短链的检查之后。
 */
import { sleep } from './cdp.mjs'
import { assert, assertEqual } from './harness.mjs'

/** 页面里那个「删除」按钮 —— 要排除对话框里的同名按钮。 */
const PAGE_DELETE = `[...document.querySelectorAll('button')].find(
  (button) => !button.closest('.dialog-panel') && button.textContent.trim() === '删除'
)`

/** 对话框内文字等于 text 的按钮。 */
const dialogButton = (text) =>
  `[...document.querySelectorAll('.dialog-panel button')].find(
    (button) => button.textContent.trim() === ${JSON.stringify(text)}
  )`

/** 读回当前焦点：它的文案、以及是否还在对话框里。 */
const ACTIVE_INFO = `(() => {
  const active = document.activeElement
  return {
    tag: active ? active.tagName : null,
    text: active ? (active.textContent || '').trim() : '',
    inPanel: Boolean(active && active.closest && active.closest('.dialog-panel')),
    isBody: active === document.body,
    isPageDelete: active === (${PAGE_DELETE}),
  }
})()`

/** 用 CDP 合成鼠标事件真的点一下（会移动焦点，与真人点击一致）。 */
async function realClick(session, locateExpr, label, { x, y } = {}) {
  let point = null
  if (x === undefined) {
    point = await session.evaluate(`(() => {
      const el = (${locateExpr})
      if (!el) return null
      // behavior:'instant' 覆盖全局的 scroll-behavior: smooth
      el.scrollIntoView({ block: 'center', behavior: 'instant' })
      const rect = el.getBoundingClientRect()
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, w: rect.width, h: rect.height }
    })()`)
    assert(point && point.w > 0 && point.h > 0, `点不到「${label}」：元素不存在或尺寸为 0`)
    x = Math.round(point.x)
    y = Math.round(point.y)
  }

  const common = { x, y, button: 'left', clickCount: 1 }
  await session.send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...common, button: 'none' })
  await session.send('Input.dispatchMouseEvent', { type: 'mousePressed', ...common })
  await session.send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...common })
}

/** 发一次真实的按键（走浏览器同一条输入管线，不是页面里造 KeyboardEvent）。 */
async function pressKey(session, { key, code, keyCode, modifiers = 0 }) {
  const base = { key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode, modifiers }
  await session.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...base })
  await session.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base })
}

const TAB = { key: 'Tab', code: 'Tab', keyCode: 9 }
const SHIFT_TAB = { ...TAB, modifiers: 8 }
const ESCAPE = { key: 'Escape', code: 'Escape', keyCode: 27 }

/** 等某个 toast 消失。 */
async function waitForToastGone(session, { timeoutMs = 8000 } = {}) {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const gone = await session.evaluate(`!document.querySelector('.toast')`)
    if (gone) return
    if (Date.now() >= deadline) throw new Error(`等提示消失超时（${timeoutMs}ms）`)
    await sleep(120)
  }
}

/** 等详情接口变成 404（软删除生效）。 */
async function waitForDeleted(api, code, manageKey, { timeoutMs = 20000 } = {}) {
  const deadline = Date.now() + timeoutMs
  let last = 0
  for (;;) {
    const res = await api.get(`/api/links/${encodeURIComponent(code)}`, { 'X-Manage-Key': manageKey })
    last = res.status
    if (res.status === 404) return
    if (Date.now() >= deadline) throw new Error(`等软删除生效超时，详情接口最后一次是 HTTP ${last}`)
    await sleep(300)
  }
}

/** 打开对话框：点页面上的「删除」，等面板出现。 */
async function openDialog(session) {
  await realClick(session, PAGE_DELETE, '页面上的「删除」')
  await session.waitFor(`Boolean(document.querySelector('.dialog-panel'))`, { label: '确认框出现' })
}

export async function register({ checks, session, base, api, fixture }) {
  console.log('\n确认框（替代 window.confirm）')

  await session.navigate(`${base}/links/${fixture.code}`)
  await session.waitFor(`(${PAGE_DELETE}) !== undefined`, { label: '详情页出现「删除」按钮' })

  // ---- 1. 语义与关联 -------------------------------------------------------
  await openDialog(session)

  await checks.run('弹出的是自绘的 alertdialog（role / aria-modal / 标题与正文都关联上了）', async () => {
    const info = await session.evaluate(`(() => {
      const panel = document.querySelector('.dialog-panel')
      const backdrop = document.querySelector('.dialog-backdrop')
      const titleId = panel.getAttribute('aria-labelledby')
      const descId = panel.getAttribute('aria-describedby')
      return {
        count: document.querySelectorAll('[role="alertdialog"]').length,
        role: panel.getAttribute('role'),
        modal: panel.getAttribute('aria-modal'),
        hasBackdrop: Boolean(backdrop),
        title: titleId ? (document.getElementById(titleId)?.textContent || '').trim() : '',
        desc: descId ? (document.getElementById(descId)?.textContent || '').trim() : '',
      }
    })()`)
    // 「能走到这里」本身就是一条断言：如果还在用 window.confirm，
    // 合成点击会把整个 JS 线程卡住，上面这次 evaluate 永远回不来（超时红）。
    assertEqual(info.count, 1, `页面上 role="alertdialog" 的元素有 ${info.count} 个`)
    assertEqual(info.role, 'alertdialog', '面板的 role 不对')
    assertEqual(info.modal, 'true', '面板没有标 aria-modal')
    assert(info.hasBackdrop, '没有找到遮罩层 .dialog-backdrop')
    assertEqual(info.title, '删除短链', '标题没有通过 aria-labelledby 关联到面板')
    assert(
      info.desc.includes(`/${fixture.code}`),
      `正文（aria-describedby）里没提到这条短码：${JSON.stringify(info.desc)}`,
    )
    return `标题「${info.title}」，正文提到 /${fixture.code}`
  })

  // ---- 2. 初始焦点 ---------------------------------------------------------
  await checks.run('初始焦点落在「取消」上，而不是「删除」上', async () => {
    const info = await session.evaluate(ACTIVE_INFO)
    assert(info.inPanel, `初始焦点不在对话框内（落在 ${info.tag} 上）`)
    assertEqual(info.text, '取消', '初始焦点不是「取消」—— 破坏性操作的默认落点必须是「不做什么」')
  })

  // ---- 3. Tab 循环 ---------------------------------------------------------
  await checks.run('Tab 与 Shift+Tab 都被关在框内（首尾循环，跑不出去）', async () => {
    // 「取消」是第一个可聚焦元素，Shift+Tab 应该绕到最后一个（确认「删除」）
    await pressKey(session, SHIFT_TAB)
    const back = await session.evaluate(ACTIVE_INFO)
    assert(back.inPanel, `Shift+Tab 之后焦点跑出了对话框（落在 ${back.tag}）`)
    assertEqual(back.text, '删除', 'Shift+Tab 没有从「取消」绕到最后一个按钮')

    // 再按 Tab，应该回到第一个
    await pressKey(session, TAB)
    const forward = await session.evaluate(ACTIVE_INFO)
    assert(forward.inPanel, `Tab 之后焦点跑出了对话框（落在 ${forward.tag}）`)
    assertEqual(forward.text, '取消', 'Tab 没有从最后一个绕回第一个')

    return '取消 ⇄ 删除，两个方向都绕回框内'
  })

  // ---- 4. Esc 取消，且什么都没发生 ------------------------------------------
  await checks.run('Esc 关闭对话框，且链接没有被删（接口仍 200）', async () => {
    await pressKey(session, ESCAPE)
    await session.waitFor(`!document.querySelector('.dialog-panel')`, { label: 'Esc 之后确认框关闭' })

    const res = await api.get(`/api/links/${encodeURIComponent(fixture.code)}`, {
      'X-Manage-Key': fixture.manageKey,
    })
    assertEqual(res.status, 200, 'Esc 之后链接还是被删了 —— 取消路径不该动数据')
    return '详情接口 HTTP 200'
  })

  // ---- 5. 取消按钮 + 焦点归位 ----------------------------------------------
  await checks.run('点「取消」关闭，且焦点回到触发它的「删除」按钮', async () => {
    await openDialog(session)
    await realClick(session, dialogButton('取消'), '对话框里的「取消」')
    await session.waitFor(`!document.querySelector('.dialog-panel')`, { label: '点取消之后确认框关闭' })

    const info = await session.evaluate(ACTIVE_INFO)
    assert(
      info.isPageDelete,
      `关闭后焦点没有回到触发它的按钮上（落在 ${info.tag}「${info.text}」上）`,
    )
    return '焦点回到页面上的「删除」'
  })

  // ---- 6. 点遮罩 = 取消 ----------------------------------------------------
  await checks.run('点遮罩关闭（点面板本身不算）', async () => {
    await openDialog(session)
    // 视口左上角一定在遮罩上：面板是居中的 440px，遮罩是 fixed inset:0
    await realClick(session, null, '遮罩左上角', { x: 12, y: 12 })
    await session.waitFor(`!document.querySelector('.dialog-panel')`, { label: '点遮罩之后确认框关闭' })

    const res = await api.get(`/api/links/${encodeURIComponent(fixture.code)}`, {
      'X-Manage-Key': fixture.manageKey,
    })
    assertEqual(res.status, 200, '点遮罩也把链接删了 —— 遮罩应该等于取消')
    return '遮罩点击 = 取消'
  })

  // ---- 7. 触发元素已经不在文档里时，焦点该去哪 -----------------------------
  await checks.run('触发它的元素已经不在文档里时，焦点退到主区域而不是 <body>', async () => {
    await openDialog(session)
    // 手工把触发元素移出文档，精确复现「删完之后那一行没了」这个场景 ——
    // 用真删除来复现是不行的：删除生效要等接口回来，而确认框在那一刻早已关闭，
    // 焦点那时已经正常还给了按钮。
    await session.evaluate(`(() => { (${PAGE_DELETE}).remove(); return true })()`)
    await pressKey(session, ESCAPE)
    await session.waitFor(`!document.querySelector('.dialog-panel')`, { label: 'Esc 之后确认框关闭' })

    const info = await session.evaluate(ACTIVE_INFO)
    assert(!info.isBody, '焦点掉回了 <body> —— 键盘用户会被迫从页首重新 Tab 一遍')
    assertEqual(info.tag, 'MAIN', `焦点落在 ${info.tag} 上，期望是 <main>`)
    return '焦点落在 <main> 上'
  })

  // 上一条把「删除」按钮移出了文档，重新导航把页面恢复成干净状态。
  //
  // ⚠️ 这里刻意**不**断言「确认删除之后焦点在哪」：删除成功会立刻路由跳转，
  //    触发元素是被随后的路由切换带走的，那属于「路由变化后焦点放哪」的问题，
  //    由 B4 统一处理（跳到新页面后把焦点放到标题上）。确认框只负责
  //    「关闭时还给触发元素，它不在了就退到主区域」，这两件事不该混在一起断言。
  await session.navigate(`${base}/links/${fixture.code}`)
  await session.waitFor(`(${PAGE_DELETE}) !== undefined`, { label: '重新导航后「删除」按钮回来' })

  // ---- 8. 确认删除：真的删掉 ----------------------------------------------
  await checks.run('点「删除」确认后真的软删除（详情接口变 404）', async () => {
    await openDialog(session)
    await realClick(session, dialogButton('删除'), '对话框里的「删除」')
    await session.waitFor(`!document.querySelector('.dialog-panel')`, { label: '确认之后确认框关闭' })
    await waitForDeleted(api, fixture.code, fixture.manageKey)
    return '详情接口 404'
  })

  console.log('\n提示（toast）')

  // ---- 9. 两个 live region -------------------------------------------------
  await checks.run('错误区与提示区是两个独立的 live region（assertive / polite）', async () => {
    const info = await session.evaluate(`(() => {
      const assertive = document.querySelector('[role="alert"][aria-live="assertive"]')
      const polite = document.querySelector('[role="status"][aria-live="polite"]')
      return { hasAssertive: Boolean(assertive), hasPolite: Boolean(polite), same: assertive === polite }
    })()`)
    assert(info.hasAssertive, '找不到 role="alert" + aria-live="assertive" 的区域')
    assert(info.hasPolite, '找不到 role="status" + aria-live="polite" 的区域')
    assert(!info.same, '两个语气共用同一个 live region —— 这样要么全打断、要么全排队')
  })

  // ---- 10. 删除成功的提示与关闭按钮 ---------------------------------------
  await checks.run('删除成功的提示落在 polite 区，文案是「已删除」', async () => {
    const info = await session.evaluate(`(() => {
      const polite = document.querySelector('[role="status"][aria-live="polite"]')
      const assertive = document.querySelector('[role="alert"][aria-live="assertive"]')
      return {
        politeText: polite ? polite.innerText.trim() : '',
        assertiveText: assertive ? assertive.innerText.trim() : '',
        hasClose: Boolean(document.querySelector('.toast-close')),
        closeLabel: document.querySelector('.toast-close')?.getAttribute('aria-label') ?? '',
      }
    })()`)
    assert(
      info.politeText.includes('已删除'),
      `polite 区里没有「已删除」：polite=${JSON.stringify(info.politeText)} assertive=${JSON.stringify(info.assertiveText)}`,
    )
    assertEqual(info.assertiveText, '', 'success 提示不该进 assertive 区（会打断读屏）')
    assert(info.hasClose, '提示没有关闭按钮')
    assertEqual(info.closeLabel, '关闭提示', '关闭按钮缺少无障碍名称')
    return `polite=「${info.politeText.split('\n')[0]}」`
  })

  await checks.run('提示的关闭按钮真的能关掉它', async () => {
    await realClick(session, `document.querySelector('.toast-close')`, '提示上的关闭按钮')
    await waitForToastGone(session)
    return '点一下就消失，不必等 3 秒自动过期'
  })
}
