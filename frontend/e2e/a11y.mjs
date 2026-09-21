/**
 * 版式与可访问性的浏览器检查（B4 引入）。
 *
 * 这里验的四件事都**不可能**在 node 环境的单测里验到，因为它们的答案在浏览器里：
 *  - 「第一个可聚焦元素是哪一个」：要按文档顺序 + 计算样式判定；
 *  - 「跳转链接平时在不在视口外、聚焦后进不进来」：要真实矩形；
 *  - 「移动端菜单有没有真的盖住页面」：要窄视口下的矩形，而这正是那个 bug 逃过审查的原因 ——
 *    `inset-16 top-16` 在代码里看着像「贴着顶栏铺满」，只有量了矩形才知道它四周各留了 64px；
 *  - 「换页之后焦点在哪」：要真的走一次客户端跳转。
 *
 * ⚠️ 移动端那两条要先把视口调窄（`Emulation.setDeviceMetricsOverride`），跑完必须清掉，
 *    否则后面所有检查都会在 390px 宽的视口上跑 —— 那是会污染后续用例的。
 */
import { assert, assertClose, assertEqual } from './harness.mjs'
import { ESCAPE, pressKey, realClick } from './input.mjs'

/** 量一次当前页面的 h1 与主区域。 */
const MEASURE_HEADING = `(() => {
  const headings = [...document.querySelectorAll('h1')]
  const main = document.querySelector('#main-content')
  const h1 = main ? main.querySelector('h1') : null
  return {
    count: headings.length,
    inMain: Boolean(h1),
    tabindex: h1 ? h1.getAttribute('tabindex') : null,
    text: h1 ? h1.textContent.trim() : '',
    mainTabindex: main ? main.getAttribute('tabindex') : null,
  }
})()`

/** 量一次跳转链接。 */
const MEASURE_SKIP = `(() => {
  const el = document.querySelector('.skip-link')
  if (!el) return { found: false }
  const rect = el.getBoundingClientRect()
  // 文档顺序上第一个「本来就能聚焦」的元素：普通 a[href] 与 button
  const first = [...document.querySelectorAll('a[href], button, [tabindex]')].find(
    (node) => node.getAttribute('tabindex') !== '-1',
  )
  return {
    found: true,
    text: el.textContent.trim(),
    href: el.getAttribute('href'),
    isFirstFocusable: first === el,
    top: rect.top,
    height: rect.height,
    viewportHeight: window.innerHeight,
  }
})()`

/** 读一次导航链接的 aria-current。 */
const READ_CURRENT = `(() => {
  const links = [...document.querySelectorAll('nav a[href]')]
  return links
    .filter((link) => link.getAttribute('aria-current') === 'page')
    .map((link) => link.textContent.trim())
})()`

export async function register({ checks, session, base }) {
  console.log('\n版式与可访问性')

  // ---- 1. 每个页面一个可聚焦的 h1 -----------------------------------------
  await session.navigate(`${base}/`)
  await session.waitFor(`document.querySelectorAll('h1').length > 0`, { label: '首页渲染出 h1' })

  await checks.run('页面上只有一个 h1，且它是可聚焦的（tabindex="-1"）', async () => {
    const info = await session.evaluate(MEASURE_HEADING)
    assertEqual(info.count, 1, `页面上有 ${info.count} 个 h1`)
    assert(info.inMain, 'h1 不在主区域 <main id="main-content"> 里 —— 「换页后焦点移到标题」就找不到落点')
    // 这条是给「以后新增页面」用的守卫：h1 默认不可聚焦，漏写 tabindex 的表现
    // 完全静默 —— 焦点没动，页面上什么都看不出来。
    assertEqual(info.tabindex, '-1', 'h1 上没有 tabindex="-1"，它拿不到焦点')
    return `「${info.text}」`
  })

  // ---- 2. 跳转链接 ---------------------------------------------------------
  await checks.run('跳转链接是全站第一个可聚焦元素，且平时藏在视口外、聚焦后才进来', async () => {
    const before = await session.evaluate(MEASURE_SKIP)
    assert(before.found, '页面上找不到 .skip-link')
    assert(before.isFirstFocusable, '跳转链接不是文档顺序上的第一个可聚焦元素 —— 键盘用户第一个 Tab 到不了它')
    assertEqual(before.href, '#main-content', '跳转链接的目标不是主区域')
    assert(
      before.top + before.height <= 0,
      `跳转链接没藏好：它占了视口内的 ${before.top.toFixed(0)}…${(before.top + before.height).toFixed(0)}`,
    )

    const after = await session.evaluate(`(() => {
      const el = document.querySelector('.skip-link')
      el.focus()
      const rect = el.getBoundingClientRect()
      return { top: rect.top, bottom: rect.bottom, viewportHeight: window.innerHeight }
    })()`)
    assert(after.top >= 0, `聚焦后跳转链接仍在视口上方（top=${after.top}）—— 等于看不到`)
    assert(after.bottom <= after.viewportHeight, '聚焦后跳转链接跑到了视口下方')
    return `平时 top=${before.top.toFixed(0)}，聚焦后 top=${after.top.toFixed(0)}`
  })

  await checks.run('激活跳转链接后焦点落到主区域 <main>', async () => {
    // 先聚焦再点：这条链接平时藏在视口外，只有聚焦时才出现 ——
    // 真人也是先 Tab 到它、再按回车/点击，所以这里先聚焦不是「为了测试方便」，
    // 而是这条链接唯一可能被触发的路径。
    await session.evaluate(`document.querySelector('.skip-link').focus()`)
    await realClick(session, `document.querySelector('.skip-link')`, '跳转链接')
    const info = await session.evaluate(`(() => {
      const active = document.activeElement
      return {
        tag: active ? active.tagName : null,
        id: active ? active.id : null,
        hash: location.hash,
      }
    })()`)
    assertEqual(info.id, 'main-content', `焦点落在 ${info.tag}#${info.id} 上，期望是主区域`)
    return `焦点 = ${info.tag}#${info.id}（hash ${info.hash}）`
  })

  // ---- 3. aria-current ----------------------------------------------------
  await checks.run('导航给当前页打 aria-current="page"，且只打给一个链接', async () => {
    const current = await session.evaluate(READ_CURRENT)
    assertEqual(current.length, 1, `有 ${current.length} 个链接带 aria-current="page"：${JSON.stringify(current)}`)
    assertEqual(current[0], '首页', '在首页上，带 aria-current 的却是别的链接')
    return `首页 → 「${current[0]}」`
  })

  // ---- 4. 客户端跳转后的焦点 ----------------------------------------------
  await checks.run('客户端跳转后焦点落到新页面的 h1（读屏才会念出「这是哪一页」）', async () => {
    await session.navigate(`${base}/`)
    await session.waitFor(`document.querySelectorAll('h1').length > 0`, { label: '首页渲染出 h1' })

    // 点顶栏的「免费开始」→ /register。这是一次**客户端跳转**，页面不刷新。
    await realClick(
      session,
      `[...document.querySelectorAll('header a[href]')].find((a) => a.textContent.trim() === '免费开始')`,
      '顶栏的「免费开始」',
    )
    await session.waitFor(`document.activeElement && document.activeElement.tagName === 'H1'`, {
      label: '焦点落到新页面的 h1',
    })
    const info = await session.evaluate(MEASURE_HEADING)
    assertEqual(info.text, '创建账号', `焦点落在标题「${info.text}」上，期望是注册页的标题`)
    return `焦点 = h1「${info.text}」`
  })

  // ---- 5. 移动端菜单（窄视口） --------------------------------------------
  console.log('\n移动端菜单（390px 视口）')
  await session.send('Emulation.setDeviceMetricsOverride', {
    width: 390,
    height: 844,
    deviceScaleFactor: 1,
    mobile: true,
  })

  const OPEN_MENU = `(() => {
    const button = document.querySelector('button[aria-expanded]')
    if (!button) return { found: false }
    const id = button.getAttribute('aria-controls')
    return { found: true, expanded: button.getAttribute('aria-expanded'), id }
  })()`

  const READ_MENU = `(() => {
    const button = document.querySelector('button[aria-expanded]')
    const id = button ? button.getAttribute('aria-controls') : null
    const panel = id ? document.getElementById(id) : null
    const header = document.querySelector('header')
    if (!panel || !header) return { found: false }
    const rect = panel.getBoundingClientRect()
    const headerRect = header.getBoundingClientRect()
    return {
      found: true,
      left: rect.left,
      right: rect.right,
      top: rect.top,
      bottom: rect.bottom,
      headerBottom: headerRect.bottom,
      viewportWidth: document.documentElement.clientWidth,
      viewportHeight: window.innerHeight,
      bodyOverflow: document.body.style.overflow,
    }
  })()`

  try {
    await session.navigate(`${base}/`)
    await session.waitFor(`Boolean(document.querySelector('button[aria-expanded]'))`, {
      label: '窄视口下出现汉堡按钮',
    })

    await checks.run('窄视口下菜单是真的铺满，而不是四周各留 64px', async () => {
      const before = await session.evaluate(OPEN_MENU)
      assert(before.found, '找不到带 aria-expanded 的汉堡按钮')
      assertEqual(before.expanded, 'false', '菜单初始就是展开状态')
      assert(before.id, '汉堡按钮没有 aria-controls —— 读屏无法把它和菜单关联起来')

      await realClick(session, `document.querySelector('button[aria-expanded]')`, '汉堡按钮')
      await session.waitFor(`document.getElementById(${JSON.stringify(before.id)}) !== null`, {
        label: '菜单面板出现',
      })

      const menu = await session.evaluate(READ_MENU)
      assert(menu.found, '菜单面板或顶栏不见了')
      // 就是这一条抓住 `inset-16 top-16`：它会让面板四周各留 64px
      assertClose(menu.left, 0, 1, '菜单面板的左边缘没贴到视口左边')
      assertClose(menu.right, menu.viewportWidth, 1, '菜单面板的右边缘没贴到视口右边')
      assertClose(menu.top, menu.headerBottom, 1, '菜单面板的上边缘没接住顶栏下沿')
      assertClose(menu.bottom, menu.viewportHeight, 1, '菜单面板的下边缘没到视口底部')
      assertEqual(menu.bodyOverflow, 'hidden', '菜单开着，但页面还能滚')
      return (
        `面板 ${menu.left.toFixed(0)}…${menu.right.toFixed(0)} × ` +
        `${menu.top.toFixed(0)}…${menu.bottom.toFixed(0)}（视口 ${menu.viewportWidth}×${menu.viewportHeight}）`
      )
    })

    await checks.run('Esc 关掉菜单，并把焦点还给汉堡按钮', async () => {
      await pressKey(session, ESCAPE)
      await session.waitFor(`document.querySelector('button[aria-expanded]').getAttribute('aria-expanded') === 'false'`, {
        label: 'Esc 之后菜单收起',
      })
      const info = await session.evaluate(`(() => {
        const active = document.activeElement
        const button = document.querySelector('button[aria-expanded]')
        return {
          onButton: active === button,
          tag: active ? active.tagName : null,
          label: active ? active.getAttribute('aria-label') : null,
          bodyOverflow: document.body.style.overflow,
        }
      })()`)
      assert(info.onButton, `焦点落在 ${info.tag}（${info.label}）上，期望是汉堡按钮`)
      assert(info.bodyOverflow !== 'hidden', '菜单关了，但滚动锁没解开')
      return `焦点 = ${info.label}，滚动锁已解`
    })
  } finally {
    // 必须清掉：否则后面所有检查都会跑在 390px 的视口上
    await session.send('Emulation.clearDeviceMetricsOverride')
  }
}
