import { afterEach, describe, expect, it, vi } from 'vitest'

import { shortBase } from './shortlink'

describe('shortBase', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
  })

  it('优先用构建期配置的域名', () => {
    vi.stubEnv('VITE_SHORT_BASE_URL', 'https://s.ashen.dev')
    expect(shortBase()).toBe('https://s.ashen.dev')
  })

  it('结尾斜杠被剥掉 —— 拼接时不会再出现双斜杠', () => {
    vi.stubEnv('VITE_SHORT_BASE_URL', 'https://s.ashen.dev///')
    expect(shortBase()).toBe('https://s.ashen.dev')
  })

  it('只写空白视为未配置 —— 否则会拼出「   /7Kd2pQ」', () => {
    vi.stubEnv('VITE_SHORT_BASE_URL', '   ')
    vi.stubGlobal('window', { location: { origin: 'https://ashen.dev' } })
    expect(shortBase()).toBe('https://ashen.dev')
  })

  it('未配置时回退到同源 —— 默认部署形态下前端就跑在短链域名上', () => {
    vi.stubEnv('VITE_SHORT_BASE_URL', '')
    vi.stubGlobal('window', { location: { origin: 'https://ashen.dev' } })
    expect(shortBase()).toBe('https://ashen.dev')
  })

  it('没有 window（Node 环境）时返回空串，调用方拼出的仍是合法相对路径', () => {
    vi.stubEnv('VITE_SHORT_BASE_URL', '')
    expect(shortBase()).toBe('')
  })
})
