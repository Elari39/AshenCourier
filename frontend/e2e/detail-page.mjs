/**
 * 详情页的浏览器检查：二维码版式、点击明细、翻页。
 *
 * 这个文件存在的主要理由是**版式断言**。README「七条踩过的坑」第 6 条记的正是
 * 「二维码画布撑破容器」：当时的验收只解码了 `toDataURL()` 的像素，而那永远是 320×320 ——
 * 版式坏了也照样全绿。所以这里断言的是**几何关系**（画布在容器内 / 显示宽等于容器宽减去
 * padding / 右边缘不压文字列），而不是「图能不能解码」。
 */
import { assert, assertClose, assertEqual, assertMatch } from './harness.mjs'

/** 量一次二维码区域的几何信息。返回的对象直接喂给断言。 */
const MEASURE_QR = `(() => {
  const canvas = document.querySelector('canvas[aria-label="短链二维码"]')
  if (!canvas) return { found: false }

  const box = canvas.parentElement
  const boxStyle = getComputedStyle(box)
  const canvasRect = canvas.getBoundingClientRect()
  const boxRect = box.getBoundingClientRect()
  // 二维码右边那一列（说明文字 + 短链 URL）。容器是 flex 布局，它是下一个兄弟节点。
  const textColumn = box.nextElementSibling
  const textRect = textColumn ? textColumn.getBoundingClientRect() : null

  // 全量扫一遍位图：确认画布真的被画过，而不是「尺寸对了但是一片空白」。
  // 稀疏采样会漏掉小面积内容，而 40 万像素的循环只要十几毫秒。
  const pixels = canvas.getContext('2d').getImageData(0, 0, canvas.width, canvas.height).data
  let dark = 0
  let light = 0
  for (let i = 0; i < pixels.length; i += 4) {
    const r = pixels[i]
    const g = pixels[i + 1]
    const b = pixels[i + 2]
    // 深墨 #141413 与暖奶油 #faf9f5
    if (r === 20 && g === 20 && b === 19) dark++
    else if (r === 250 && g === 249 && b === 245) light++
  }

  return {
    found: true,
    inlineWidth: canvas.style.width,
    inlineHeight: canvas.style.height,
    bitmapWidth: canvas.width,
    bitmapHeight: canvas.height,
    totalPixels: pixels.length / 4,
    darkPixels: dark,
    lightPixels: light,
    boxClientWidth: box.clientWidth,
    boxClientHeight: box.clientHeight,
    paddingLeft: parseFloat(boxStyle.paddingLeft),
    paddingRight: parseFloat(boxStyle.paddingRight),
    paddingTop: parseFloat(boxStyle.paddingTop),
    paddingBottom: parseFloat(boxStyle.paddingBottom),
    canvasLeft: canvasRect.left,
    canvasRight: canvasRect.right,
    canvasTop: canvasRect.top,
    canvasBottom: canvasRect.bottom,
    canvasWidth: canvasRect.width,
    canvasHeight: canvasRect.height,
    boxLeft: boxRect.left,
    boxRight: boxRect.right,
    boxTop: boxRect.top,
    boxBottom: boxRect.bottom,
    textColumnLeft: textRect ? textRect.left : null,
  }
})()`

/**
 * 量一次趋势图的配色。
 *
 * 为什么值得测：B5 把折线与渐变的颜色从内联 hex 改成了 token 类
 * （`.chart-line { stroke: var(--color-primary) }`）。这是一次**纯视觉**的改动 ——
 * 类没生效时 SVG 不会报错，折线只是变成默认的黑色、面积变成黑色渐变，
 * 页面照样渲染、evaluate 照样返回值、console 照样干净。
 * 所以只能靠读回**计算后的颜色**来判断，而不是「有没有报错」。
 */
const MEASURE_CHART = `(() => {
  const svg = document.querySelector('svg[aria-label="按天点击趋势折线图"]')
  if (!svg) return { found: false }

  const stopColorOf = (el) => (el ? getComputedStyle(el).stopColor : null)
  const area = svg.querySelector('path[fill]')
  const gradient = svg.querySelector('linearGradient')

  // url(#trend-fill-v3) -> trend-fill-v3，不用正则（模板串里写转义太容易出错）
  const raw = area ? area.getAttribute('fill') || '' : ''
  const refId = raw.startsWith('url(#') && raw.endsWith(')') ? raw.slice(5, -1) : ''

  return {
    found: true,
    lineStroke: getComputedStyle(svg.querySelector('.chart-line')).stroke,
    guideStroke: getComputedStyle(svg.querySelector('.chart-guide')).stroke,
    tickFill: getComputedStyle(svg.querySelector('.chart-tick')).fill,
    tickFontSize: getComputedStyle(svg.querySelector('.chart-tick')).fontSize,
    stopColors: [...svg.querySelectorAll('.chart-stop')].map(stopColorOf),
    areaFillRef: raw,
    refId,
    gradId: gradient ? gradient.id : null,
    // 引用必须指向**本文档里真实存在**的那个渐变节点
    refResolves: refId ? Boolean(document.getElementById(refId)) : false,
    gradientIds: [...document.querySelectorAll('linearGradient')].map((g) => g.id),
  }
})()`

/** 读一次明细表：行数、首行每个单元格的文本、以及整页可见文本。 */
const READ_TABLE = `(() => {
  const rows = [...document.querySelectorAll('table.data-table tbody tr')]
  const cells = rows.map((row) => [...row.querySelectorAll('td')].map((td) => td.textContent.trim()))
  const moreButton = [...document.querySelectorAll('button')].find(
    (button) => button.textContent.includes('加载更多'),
  )
  return {
    rowCount: rows.length,
    cells,
    hasMoreButton: Boolean(moreButton),
    bodyText: document.body.innerText,
  }
})()`

/** 点一次「加载更多」。 */
const CLICK_LOAD_MORE = `(() => {
  const button = [...document.querySelectorAll('button')].find(
    (element) => element.textContent.includes('加载更多'),
  )
  if (!button) return false
  button.click()
  return true
})()`

export async function register({ checks, session, base, fixture, expectedClicks, apiClicks }) {
  console.log('\n详情页：二维码版式')

  await session.navigate(`${base}/links/${fixture.code}`)
  // 等明细渲染出来再量版式 —— 页面还没挂上数据时量到的矩形没有意义
  await session.waitFor(`document.querySelectorAll('table.data-table tbody tr').length > 0`, {
    label: '点击明细表格渲染完成',
  })

  await checks.run('二维码画布存在，且行内尺寸已被清空（不被 qrcode 写的 320px 盖住）', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    assert(qr.found, '页面上找不到 canvas[aria-label="短链二维码"]')
    // 这一条直接守住「七条踩过的坑」第 6 条：qrcode 的 canvas 渲染器会把
    // style.width/height 写成 320px 行内样式，行内优先级高于 Tailwind 的 h-full w-full
    assertEqual(qr.inlineWidth, '', 'canvas 的行内 width 没被清空')
    assertEqual(qr.inlineHeight, '', 'canvas 的行内 height 没被清空')
    return `位图 ${qr.bitmapWidth}×${qr.bitmapHeight}`
  })

  await checks.run('位图分辨率仍是 320×320（高 DPI 与截图放大的依据）', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    assertEqual(qr.bitmapWidth, 320, '位图宽度不是 320')
    assertEqual(qr.bitmapHeight, 320, '位图高度不是 320')
  })

  await checks.run('画布真实绘制过：像素里同时有深墨与暖奶油', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    assert(qr.darkPixels > 2000, `深墨像素太少（${qr.darkPixels}）—— 画布可能没画上`)
    assert(qr.lightPixels > 2000, `暖奶油像素太少（${qr.lightPixels}）—— 画布可能没画上`)
    const painted = qr.darkPixels + qr.lightPixels
    assert(
      painted >= qr.totalPixels * 0.98,
      `两种配色只覆盖了 ${((painted / qr.totalPixels) * 100).toFixed(2)}% 的像素，` +
        `说明前景不是深墨/暖奶油，或者画布被撑开后有留白`,
    )
    return `深墨 ${qr.darkPixels} px / 暖奶油 ${qr.lightPixels} px`
  })

  await checks.run('画布矩形落在容器矩形内（没撑破容器）', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    assert(qr.canvasLeft >= qr.boxLeft - 0.5, `画布左边缘 ${qr.canvasLeft} 越过了容器左边缘 ${qr.boxLeft}`)
    assert(
      qr.canvasRight <= qr.boxRight + 0.5,
      `画布右边缘 ${qr.canvasRight} 越过了容器右边缘 ${qr.boxRight}`,
    )
    assert(qr.canvasTop >= qr.boxTop - 0.5, `画布上边缘 ${qr.canvasTop} 越过了容器上边缘 ${qr.boxTop}`)
    assert(
      qr.canvasBottom <= qr.boxBottom + 0.5,
      `画布下边缘 ${qr.canvasBottom} 越过了容器下边缘 ${qr.boxBottom}`,
    )
    return `容器 ${qr.boxClientWidth}×${qr.boxClientHeight}，画布 ${qr.canvasWidth}×${qr.canvasHeight}`
  })

  await checks.run('显示尺寸 = 容器宽度减去左右 padding（这正是撑破容器时会失守的那条）', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    const expectedWidth = qr.boxClientWidth - qr.paddingLeft - qr.paddingRight
    const expectedHeight = qr.boxClientHeight - qr.paddingTop - qr.paddingBottom
    assertClose(qr.canvasWidth, expectedWidth, 1, '画布显示宽度与容器内容宽度不一致')
    assertClose(qr.canvasHeight, expectedHeight, 1, '画布显示高度与容器内容高度不一致')
    return `${qr.canvasWidth.toFixed(1)}px = ${qr.boxClientWidth} − ${qr.paddingLeft} − ${qr.paddingRight}`
  })

  await checks.run('画布右边缘不压住右侧文字列', async () => {
    const qr = await session.evaluate(MEASURE_QR)
    assert(qr.textColumnLeft !== null, '找不到二维码右侧的文字列（容器结构可能变了）')
    assert(
      qr.canvasRight < qr.textColumnLeft,
      `画布右边缘 ${qr.canvasRight} 压到了文字列左边缘 ${qr.textColumnLeft}`,
    )
    return `画布右边缘 ${qr.canvasRight.toFixed(1)} < 文字列左边缘 ${qr.textColumnLeft.toFixed(1)}`
  })

  console.log('\n详情页：趋势图配色')

  const chart = await session.evaluate(MEASURE_CHART)

  await checks.run('折线与面积渐变解析成主题主色 #cc785c（token 类真的生效，而不是回落到默认黑）', async () => {
    assert(chart.found, '页面上找不到 svg[aria-label="按天点击趋势折线图"]')
    // 主色 #cc785c = rgb(204, 120, 92)。回落成默认黑是 rgb(0, 0, 0)。
    assertEqual(chart.lineStroke, 'rgb(204, 120, 92)', '折线描边不是主色（.chart-line 没生效？）')
    assertEqual(chart.guideStroke, 'rgb(230, 223, 216)', '参考线不是 hairline 色（.chart-guide 没生效？）')
    assertEqual(chart.tickFill, 'rgb(142, 139, 130)', '刻度文字不是 muted-soft 色（.chart-tick 没生效？）')
    assertEqual(chart.stopColors.length, 2, '面积渐变应该有 2 个 stop')
    for (const color of chart.stopColors) {
      assertEqual(color, 'rgb(204, 120, 92)', `渐变 stop 不是主色：${color}`)
    }
    return `折线 ${chart.lineStroke} / 参考线 ${chart.guideStroke} / 刻度 ${chart.tickFill} / 刻度字号 ${chart.tickFontSize}`
  })

  await checks.run('面积渐变引用的是文档里真实存在的 id（写死 id 会在多实例时指向别人）', async () => {
    assert(chart.found, '页面上找不到趋势图')
    assert(
      chart.areaFillRef.startsWith('url(#'),
      `面积路径的 fill 不是渐变引用：${JSON.stringify(chart.areaFillRef)}`,
    )
    assert(chart.refResolves, `面积路径引用的 #${chart.refId} 在文档里不存在（渐变没渲染出来）`)
    assertEqual(
      chart.gradientIds.filter((id) => id === chart.refId).length,
      1,
      `文档里有多个节点的 id 都是 ${chart.refId} —— id 必须是组件级唯一的`,
    )
    // 这一条是**代理断言**：真正要防的是「同一页出现两张趋势图时互相顶掉」，
    // 而当前页面上只有一张图，验不出来。能验的是「id 不是写死的常量」——
    // id 一旦变回字面量，多实例碰撞就必然复现，所以拦住这一点就够用了。
    assert(
      chart.gradId !== 'trend-fill',
      '渐变的 id 又变成写死的常量了：同一页出现两张趋势图时，url(#trend-fill) 会一律指向文档里第一个渐变',
    )
    return `${chart.areaFillRef} → 命中唯一节点（id=${chart.gradId}）`
  })

  console.log('\n详情页：点击明细')

  const table = await session.evaluate(READ_TABLE)

  await checks.run('明细接口只回掩码网段，不回原始 IP', async () => {
    assert(
      apiClicks.length >= expectedClicks,
      `接口只回了 ${apiClicks.length} 条明细，期望至少 ${expectedClicks} 条`,
    )
    for (const click of apiClicks) {
      assertMatch(
        click.ip,
        /^(\d{1,3}\.){3}0\/24$|^[0-9a-f:]+::\/64$/i,
        `明细里的 ip 不是掩码网段：${JSON.stringify(click.ip)}`,
      )
    }
    return `${apiClicks.length} 条都是网段（例如 ${apiClicks[0].ip}）`
  })

  await checks.run('首屏明细正好一页（20 行）', async () => {
    assertEqual(table.rowCount, 20, '首屏行数不等于一页大小 20')
  })

  await checks.run('时间列是精确到秒的本地时间', async () => {
    assertMatch(table.cells[0][0], /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}$/, '时间列格式不符')
    return table.cells[0][0]
  })

  await checks.run('每一行的来源与 IP 都与接口逐条一致（UI 没有自作主张）', async () => {
    for (let i = 0; i < table.cells.length; i++) {
      assertEqual(table.cells[i][3], apiClicks[i].referer, `第 ${i + 1} 行的来源列与接口不一致`)
      assertEqual(table.cells[i][4], apiClicks[i].ip, `第 ${i + 1} 行的 IP 列与接口不一致`)
    }
    return `逐条核对 ${table.cells.length} 行`
  })

  await checks.run('页面可见文本里没有原始 IP（掩码不是只在接口上做）', async () => {
    // 容器网段里的原始地址形如 172.20.0.1；掩码后是 172.20.0.0/24。
    // 只要页面上出现了「以 .0/24 结尾」以外的完整地址（带 /32 或末端非 0），就算泄露。
    const leaked = table.bodyText.match(/\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?:\/\d{1,3})?\b/g) ?? []
    const offenders = leaked.filter((value) => !/\.0\/24$/.test(value) && value !== '0.0.0.0')
    assert(offenders.length === 0, `页面文本里出现了疑似原始地址：${offenders.join(', ')}`)
  })

  await checks.run('「加载更多」能翻到第二页，两页拼接后不重不漏', async () => {
    assert(table.hasMoreButton, '有更多明细时却没渲染「加载更多」按钮')
    const clicked = await session.evaluate(CLICK_LOAD_MORE)
    assert(clicked, '点不到「加载更多」按钮')
    await session.waitFor(
      `document.querySelectorAll('table.data-table tbody tr').length === ${expectedClicks}`,
      { label: `翻页后明细达到 ${expectedClicks} 行` },
    )
    const after = await session.evaluate(READ_TABLE)
    assertEqual(after.rowCount, expectedClicks, '翻页后行数不对')
    assert(!after.hasMoreButton, '已经翻到底了却还留着「加载更多」按钮（next_cursor 应该为空）')
    assert(after.bodyText.includes('已经到底了'), '翻到底后没有出现「已经到底了」提示')
    // 播种时每次跳转都换了来源，所以 26 行两两可区分 —— 这一条才真的能证明「两页没有重叠」。
    // 若行内容完全相同（同一秒、同一网段、同一来源），重复行在页面上根本看不出来，
    // 断言就成了摆设。与接口逐条对齐则顺带挡住「少一行」这种缺口。
    const keys = after.cells.map((cells) => `${cells[0]}|${cells[3]}|${cells[4]}`)
    assertEqual(
      new Set(keys).size,
      keys.length,
      `翻页后出现了重复行（两页有重叠）\n      行数 ${after.rowCount}\n      首行 ${JSON.stringify(after.cells[0])}`,
    )
    for (let i = 0; i < after.cells.length; i++) {
      assertEqual(after.cells[i][3], apiClicks[i].referer, `翻页后第 ${i + 1} 行的来源与接口不一致`)
    }
    return `${table.rowCount} + ${expectedClicks - table.rowCount} = ${after.rowCount} 行`
  })
}
