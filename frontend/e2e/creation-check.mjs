import { writeFileSync, mkdirSync } from 'node:fs'
import { launchChrome } from './cdp.mjs'
import { realClick } from './input.mjs'
import { assert } from './harness.mjs'
const base = process.argv[2] || 'http://localhost:18083'
const { session, stop } = await launchChrome({port:9336, extraFlags:['--no-proxy-server']})
const results=[]
const click = async (text) => realClick(session, `Array.from(document.querySelectorAll('button')).find(e=>e.textContent.trim().startsWith(${JSON.stringify(text)}))`, text)
async function fill(selector,text) {
 await realClick(session, `document.querySelector(${JSON.stringify(selector)})`,selector)
 await session.send('Input.insertText',{text})
}
async function select(value) {
 await session.evaluate(`(()=>{const el=document.querySelector('select');el.value=${JSON.stringify(value)};el.dispatchEvent(new Event('change',{bubbles:true}));})()`)
}
async function submitAndRead() {
 const before=await session.evaluate('Object.keys(JSON.parse(localStorage.getItem("ashen:manageKeys")||"{}"))')
 const start=Date.now()
 await click('生成短链')
 await session.waitFor(`Object.keys(JSON.parse(localStorage.getItem('ashen:manageKeys')||'{}')).some(k=>!${JSON.stringify(before)}.includes(k))`)
 const pair=await session.evaluate(`Object.entries(JSON.parse(localStorage.getItem('ashen:manageKeys'))).find(([k])=>!${JSON.stringify(before)}.includes(k))`)
 const response=await fetch(base+'/api/links/'+pair[0],{headers:{'X-Manage-Key':pair[1]}})
 assert(response.ok,'cannot read created link')
 return { link:await response.json(),start }
}
try {
 await session.navigate(base+'/')
 await session.waitFor('document.querySelector("input[type=url]")')
 await fill('input[type=url]','https://example.com/default-expiry')
 const first=await submitAndRead()
 const delta=Date.parse(first.link.expires_at)-first.start
 assert(delta>=86400000 && delta<86410000,'default is not 24 hours from submission')
 assert(await session.evaluate('document.body.textContent.includes("到期时间：")'),'result missing expiry')
 results.push('Default creation expires 24 hours from submit; result displays expiry.')
 await fill('input[type=url]','https://example.com/permanent')
 await click('高级选项')
 await select('permanent')
 const second=await submitAndRead()
 assert(!second.link.expires_at,'explicit permanent should omit expiry')
 assert(await session.evaluate('document.querySelector("select").value === "24h"'),'next creation did not reset default')
 results.push('Permanent works; next creation returns to 24-hour default.')
 await fill('input[type=url]','https://example.com/protected')
 await click('高级选项')
 await select('custom')
 await click('生成短链')
 await session.waitFor('document.querySelector("input[type=datetime-local]").getAttribute("aria-invalid") === "true"')
 assert(await session.evaluate('document.activeElement.type === "datetime-local"'),'custom expiry error not focused')
 await select('7d')
 await realClick(session,'document.querySelector("input[type=checkbox]")','enable password')
 await click('生成短链')
 await session.waitFor('document.querySelector("input[autocomplete=new-password]").getAttribute("aria-invalid") === "true"')
 await fill('input[autocomplete=new-password]','Browser-test-password')
 await fill('input[placeholder="例如 工作, 资料"]','工作,资料')
 mkdirSync('docs/screenshots/creation',{recursive:true})
 for(const width of [390,768,1440]) {
  await session.send('Emulation.setDeviceMetricsOverride',{width,height:1000,deviceScaleFactor:1,mobile:false})
  await session.evaluate('window.scrollTo(0,0)')
  assert(await session.evaluate('document.documentElement.scrollWidth <= innerWidth+1'),'advanced options overflow')
  const image=await session.send('Page.captureScreenshot',{format:'png',captureBeyondViewport:true})
  writeFileSync(`docs/screenshots/creation/advanced-${width}.png`,Buffer.from(image.data,'base64'))
 }
 const third=await submitAndRead()
 assert(third.link.password_protected,'password not enabled')
 assert(Date.parse(third.link.expires_at)-third.start>=7*86400000,'7-day expiry missing')
 assert(third.link.tags.length===2,'tags missing')
 results.push('Custom expiry and password validation focus their fields; 7-day protected creation and tags work.')
 assert(session.pageErrors.length===0,JSON.stringify(session.pageErrors))
 results.push('Advanced layout verified at 390/768/1440px; no console errors.')
 writeFileSync('docs/screenshots/creation/verification.txt',results.join('\n'))
 console.log(results.join('\n'))
} finally { stop() }
