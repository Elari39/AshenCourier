#!/usr/bin/env node
/**
 * 零引用守卫 —— 拦住「定义了但没人用」的组件与样式类。
 *
 * 为什么要有它：B0–B6 这轮前端收敛清掉了一批零引用（视图里手写的 card- / btn- 系列收进组件层、
 * 删掉 .title-sm 与 .caption），但**清一次不等于不再长出来**。新加一个 UI 组件却忘了接进视图、
 * 或者照着 DESIGN.md 的字阶表把类补进 main.css 而没有任何使用点，都属于「人眼很难发现、
 * 但产物里真实多出字节」的退化。这条守卫把它变成 CI 会红的事。
 *
 * 只检查三类**有实际成本**的东西：
 *   ① src/{components,layouts,views}/** 下的 .vue 组件
 *   ② main.css 里定义的具名类（@layer components / utilities，以及层外的减弱动效规则）
 *   ③ 未被引用的 token 在非 CSS 源码里被「提及」（详见下面第 ③ 节）
 *
 * 刻意**不**检查 @theme 里的 token，也**不**把「导出的类型」当死码：
 *   - @theme 里未被引用的 token 不进产物（Tailwind v4 会剪枝），它们是 DESIGN.md 色板的
 *     声明，留着零成本 —— 删掉反而是纯改动。
 *     ⚠️ 这条有个反直觉的前提：**别在 frontend/ 内的源码里写出 token 的全名。**
 *     Tailwind 判断「一个 token 有没有被用到」靠的是**扫源码文本**，写在 JS/TS 的注释里
 *     同样算数 —— 真写了，那个变量就会被输出进 :root，白白多出几十字节。
 *     本脚本早先的版本正是踩了这个坑：文件头为了举例写了几个 token 名，
 *     结果把它们钉进了产物（实测移除后产物少 161 字节）。所以这里一个名字都不写，
 *     具体是哪几个、怎么验的，记在 README 的验收表里（README 在 Vite 根之外，不会被扫）。
 *   - 类型导出（如 RequestTicket / ConfirmOptions / LinkStatus）大多只在本文件内部被接口
 *     签名引用，删掉 export 会让调用方再也引用不到这些字段的类型，属于倒退。
 *
 * 判定「被使用」：
 *   - 组件：出现在别的文件里，形态为导入路径 `.../Name.vue`、或模板标签 `<Name`。
 *     同时接受 kebab-case 标签（`<page-header`），因为 Vue 允许这样写。
 *   - 类：出现在**非 CSS** 文件里（.ts / .vue / .mjs / .html），但**不算 scripts/**。
 *     刻意不把「出现在 main.css 的另一条规则里」算作使用 —— 那是选择器，不是使用点；
 *     把 CSS 里的提及算进去的话，光是注释里写一句 `.foo` 就会让守卫失效。
 *     scripts/ 同理：审计脚本的注释里会写类名，算进来守卫就会被自己糊住。
 *   - token 是否「被用到」：`var(--名字)` 或对应的 Tailwind 工具类（按组匹配前缀），
 *     在**含 scripts/ 在内**的全部源码里搜 —— 因为 Tailwind 扫的就是这些。
 *
 * 用 `pnpm audit:refs` 跑；CI 的 frontend job 会跑。要新增「暂时没有使用点」的东西，
 * 请加进下面的 ALLOW 并写清理由，不要注释掉检查。
 */

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'src')
const CSS_PATH = join(SRC, 'assets', 'main.css')

const SKIP_DIRS = new Set(['node_modules', 'dist', '.vite', 'coverage'])

/** 允许零引用并说明原因。键是 `相对路径` 或 `类名`。 */
const ALLOW_COMPONENTS = new Map()
const ALLOW_CLASSES = new Map()

// ---------------------------------------------------------------- 收集文本

/** @param {string} dir @returns {string[]} */
function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) out.push(...walk(full))
    else out.push(full)
  }
  return out
}

/** 相对 ROOT 的 posix 风格路径，报错信息在任何平台上都可读。 */
function rel(p) {
  return relative(ROOT, p).split(sep).join('/')
}

/** 收集要被搜索的文件：src 下全部源码 + index.html + e2e（e2e 也是真实使用方）。 */
const files = []
for (const f of walk(SRC)) {
  if (/\.(ts|vue|mjs|js|html)$/.test(f)) files.push(f)
}
files.push(join(ROOT, 'index.html'))
try {
  for (const f of walk(join(ROOT, 'e2e'))) {
    if (/\.(mjs|js)$/.test(f)) files.push(f)
  }
} catch {
  // 没有 e2e 目录也不影响
}

/**
 * scripts/ 下的构建/审计脚本单独放一组。
 *
 * 为什么要单独一组：这两类检查对它的态度相反 ——
 *   - **token 泄漏**：必须算进来。它就在 Vite 根里，Tailwind 扫它；
 *     本轮那次泄漏事故正是发生在 scripts/ 下的注释里（见文件头）。
 *     一开始漏了这一个目录，是变异验证 M5 把它抓出来的。
 *   - **类的「使用」**：不能算。审计脚本的注释里会写类名（比如本文件就提到过
 *     两个被删掉的类），若算作使用，守卫会被自己的注释糊住、再也发现不了零引用类。
 */
const SCRIPT_TEXTS = []
try {
  for (const f of walk(join(ROOT, 'scripts'))) {
    if (/\.(mjs|cjs|js|ts)$/.test(f)) {
      SCRIPT_TEXTS.push({ path: f, text: readFileSync(f, 'utf8') })
    }
  }
} catch {
  // 没有 scripts 目录也不影响
}

/** 非 CSS 文件的文本（类的「使用」只认这些，不含 scripts/） */
const CODE_FILES = files.filter((f) => !f.endsWith('.css'))
const CODE_TEXTS = CODE_FILES.map((f) => ({ path: f, text: readFileSync(f, 'utf8') }))
/** token 泄漏检查要连 scripts/ 一起看（Tailwind 也会扫它） */
const LEAK_TEXTS = [...CODE_TEXTS, ...SCRIPT_TEXTS]
/** 含 CSS 与 scripts 的全部文本（组件的「使用」与 token 的「是否被用到」都认全部） */
const ALL_TEXTS = [
  ...LEAK_TEXTS,
  { path: CSS_PATH, text: readFileSync(CSS_PATH, 'utf8') },
]

const problems = []

// ------------------------------------------------------------ ① 组件零引用

const COMPONENT_DIRS = ['components', 'layouts', 'views'].map((d) => join(SRC, d))

const componentFiles = []
for (const d of COMPONENT_DIRS) {
  let entries = []
  try {
    entries = walk(d)
  } catch {
    continue
  }
  for (const f of entries) if (f.endsWith('.vue')) componentFiles.push(f)
}

/** PascalCase → kebab-case（`PageHeader` → `page-header`） */
function toKebab(name) {
  return name.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase()
}

let componentChecked = 0
for (const file of componentFiles) {
  const name = file.slice(file.lastIndexOf(sep) + 1).replace(/\.vue$/, '')
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  // 导入路径 .../Name.vue（兼容 `./Name.vue` 这类相对导入）或模板标签 <Name / <Name> / <Name/>
  const used = new RegExp(
    `[/\\\\]${escaped}\\.vue|<${escaped}[\\s/>]|<${toKebab(name)}[\\s/>]`,
  )
  const hits = ALL_TEXTS.filter((f) => f.path !== file && used.test(f.text))
  componentChecked++
  if (hits.length === 0 && !ALLOW_COMPONENTS.has(rel(file))) {
    problems.push({
      kind: '组件',
      what: rel(file),
      hint: `没有任何文件用 <${name}> 或导入它`,
    })
  }
}

// ---------------------------------------------------------- ② 具名类零引用

const css = readFileSync(CSS_PATH, 'utf8')

/** 抽出 main.css 里以 `.` 开头的选择器行中的类名（含多选择器续行、伪类）。 */
const definedClasses = new Map() // 类名 → 定义次数
for (const line of css.split('\n')) {
  const stripped = line.trim()
  if (!stripped.startsWith('.')) continue
  const selector = stripped.split('{')[0]
  for (const m of selector.matchAll(/\.([a-zA-Z][a-zA-Z0-9_-]*)/g)) {
    definedClasses.set(m[1], (definedClasses.get(m[1]) ?? 0) + 1)
  }
}

let classChecked = 0
for (const name of [...definedClasses.keys()].sort()) {
  const word = new RegExp(`(?<![a-zA-Z0-9_-])${name}(?![a-zA-Z0-9_-])`)
  const hits = CODE_TEXTS.filter((f) => word.test(f.text))
  classChecked++
  if (hits.length === 0 && !ALLOW_CLASSES.has(name)) {
    problems.push({
      kind: '样式类',
      what: `.${name}`,
      hint: `main.css 里定义了，但没有任何模板/脚本用到它`,
    })
  }
}

// --------------------------------------------------------------- @theme 简报

const themeMatch = css.match(/@theme\s*\{([\s\S]*?)\n\}/)
const themeTokens = themeMatch
  ? [...themeMatch[1].matchAll(/^\s*(--(?:color|radius|spacing|font)-[a-z0-9-]+)\s*:/gm)].map(
      (m) => m[1],
    )
  : []

/**
 * 每个 token 组对应的 Tailwind 工具类前缀。
 * 必须按组分，不能统一用 `*-tail`：`xl` / `sm` / `md` / `lg` 这些尾名在不同组之间重复，
 * 统一匹配会把 `display-xl` / `max-w-xl` 当成圆角那一档的一次使用 —— 于是打印出
 * 「它有人在用」这种错的事实（这正是本脚本第一版干过的事）。
 */
const UTILITY_PREFIXES = {
  color: [
    'text', 'bg',
    'border', 'border-t', 'border-r', 'border-b', 'border-l', 'border-x', 'border-y',
    'border-s', 'border-e',
    'divide', 'divide-x', 'divide-y',
    'from', 'via', 'to',
    'ring', 'ring-offset', 'inset-ring',
    'fill', 'stroke', 'outline', 'shadow', 'inset-shadow',
    'caret', 'accent', 'placeholder', 'decoration',
  ],
  radius: ['rounded', 'rounded-t', 'rounded-r', 'rounded-b', 'rounded-l', 'rounded-tl', 'rounded-tr', 'rounded-br', 'rounded-bl'],
  spacing: [
    'p', 'px', 'py', 'pt', 'pb', 'pl', 'pr', 'ps', 'pe',
    'm', 'mx', 'my', 'mt', 'mb', 'ml', 'mr', 'ms', 'me',
    'gap', 'gap-x', 'gap-y', 'space-x', 'space-y',
    'w', 'h', 'size', 'min-w', 'max-w', 'min-h', 'max-h', 'basis',
    'inset', 'inset-x', 'inset-y', 'top', 'bottom', 'left', 'right', 'start', 'end',
    'translate-x', 'translate-y', 'scroll-m', 'scroll-p',
  ],
  font: ['font'],
}

const unusedTokens = themeTokens.filter((token) => {
  const group = token.match(/^--(color|radius|spacing|font)-/)?.[1]
  const tail = token.replace(/^--(?:color|radius|spacing|font)-/, '')
  const prefixes = UTILITY_PREFIXES[group] ?? []
  const direct = new RegExp(`var\\(\\s*${token}\\s*[,)]`)
  const utilities = prefixes.map(
    (p) => new RegExp(`(?<![a-zA-Z0-9-])${p}-${tail}(?![a-zA-Z0-9-])`),
  )
  return !ALL_TEXTS.some(
    (f) => direct.test(f.text) || utilities.some((re) => re.test(f.text)),
  )
})

// ------------------------------------------------- ③ 未引用的 token 被「提及」

/**
 * 未被引用的 token，不能在**非 CSS 源码**里被提到 —— 注释里写一句也算。
 *
 * 为什么单列一条：Tailwind v4 判断一个 @theme 变量有没有被用到，靠的是**扫源码文本**。
 * 于是「在 .mjs 的注释里写一句 token 全名」就等于声明它被使用，它会被输出进 :root，
 * 白白多出几十字节。这种退化没有任何报错、也不影响功能，最容易漏掉 ——
 * 本轮就真的踩过一次（见文件头的说明）。
 *
 * 只查**未被引用**的 token：已经被 var() 或工具类用到的那些，注释里提到它是无害的。
 * 也不查 CSS 文件（main.css 里的定义与 var() 都属正常）。
 */
const tokenLeaks = []
for (const token of unusedTokens) {
  const literal = new RegExp(`(?<![a-zA-Z0-9-])${token}(?![a-zA-Z0-9-])`)
  for (const f of LEAK_TEXTS) {
    if (literal.test(f.text)) tokenLeaks.push({ token, file: rel(f.path) })
  }
}
for (const leak of tokenLeaks) {
  problems.push({
    kind: 'token 提及',
    what: leak.token,
    hint: `${leak.file} 里提到了这个未被引用的 token —— 会被 Tailwind 当成「在用」并输出进产物，请删掉这处提及`,
  })
}

// ------------------------------------------------------------------- 输出

console.log('零引用守卫')
console.log('='.repeat(70))
console.log(`  组件  ${componentChecked} 个（components / layouts / views）`)
console.log(`  类名  ${classChecked} 个（main.css 中的具名类）`)
console.log(`  @theme token ${themeTokens.length} 个，其中未被引用 ${unusedTokens.length} 个`)
console.log('    （未引用的 token 本身不判失败：Tailwind 会剪掉它们、不进产物 ——')
console.log('      但前提是没有任何非 CSS 源码提到它，这一点由检查 ③ 负责）')
if (unusedTokens.length) {
  console.log(`    ${unusedTokens.join('、')}`)
}

if (problems.length === 0) {
  console.log()
  console.log('✓ 没有零引用的组件 / 样式类，也没有未引用的 token 被源码提到')
  process.exit(0)
}

console.log()
console.log(`✗ 发现 ${problems.length} 处问题：`)
for (const p of problems) {
  console.log(`    [${p.kind}] ${p.what}`)
  console.log(`         ${p.hint}`)
}
console.log()
console.log('处理方式：删掉它，或（确认后续会用到时）加进 scripts/check-zero-ref.mjs')
console.log('的 ALLOW 列表并写清理由 —— 不要注释掉检查。')
process.exit(1)
