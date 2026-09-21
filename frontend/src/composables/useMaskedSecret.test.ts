import { nextTick, ref } from 'vue'
import { describe, expect, it } from 'vitest'

import { useMaskedSecret } from './useMaskedSecret'

describe('useMaskedSecret', () => {
  it('默认是收起的（默认打码才是安全的那一侧）', () => {
    const code = ref('abc123')
    const { revealed } = useMaskedSecret(() => code.value)
    expect(revealed.value).toBe(false)
  })

  it('toggle 能来回切', () => {
    const code = ref('abc123')
    const { revealed, toggle } = useMaskedSecret(() => code.value)

    toggle()
    expect(revealed.value).toBe(true)
    toggle()
    expect(revealed.value).toBe(false)
  })

  it('换了来源（另一串密钥）就自动收起', async () => {
    // 这正是修掉的那个缺陷：结果卡被复用时，revealed 会跨链接留着
    const code = ref('abc123')
    const { revealed, toggle } = useMaskedSecret(() => code.value)

    toggle()
    expect(revealed.value).toBe(true)

    code.value = 'def456'
    await nextTick()
    expect(revealed.value).toBe(false)
  })

  it('来源没变时不动它（同一条链接重渲染不该把用户展开的密钥又收起来）', async () => {
    const code = ref('abc123')
    const { revealed, toggle } = useMaskedSecret(() => code.value)

    toggle()
    // 同一串来源连着赋两次：watch 只在值真的变了时才触发
    code.value = 'abc123'
    await nextTick()
    expect(revealed.value).toBe(true)
  })

  it('来源从空变成有值时也收起（先渲染、数据后到的那一帧）', async () => {
    const code = ref<string | undefined>(undefined)
    const { revealed, toggle } = useMaskedSecret(() => code.value)

    toggle()
    code.value = 'abc123'
    await nextTick()
    expect(revealed.value).toBe(false)
  })
})
