/**
 * `@/utils/tags` 的单测。
 *
 * 断言的是「前端提交出去的值」——和后端的归一化（trim + 小写 + 去重 + 限长）
 * 是两件事，这里**刻意不断言去重与大小写**，免得把两处规则焊在一起。
 */
import { describe, expect, it } from 'vitest'

import { splitTags } from '@/utils/tags'

describe('splitTags', () => {
  it('按半角逗号拆分', () => {
    expect(splitTags('ops,dev')).toEqual(['ops', 'dev'])
  })

  it('全角逗号也认（中文输入法很容易打出来）', () => {
    expect(splitTags('ops，dev')).toEqual(['ops', 'dev'])
  })

  it('逐项 trim，并丢掉空项', () => {
    expect(splitTags(' ops , , dev ,, ')).toEqual(['ops', 'dev'])
  })

  it('空串与纯空白得到空数组（调用方据此决定「不传 tags」还是「清空 tags」）', () => {
    expect(splitTags('')).toEqual([])
    expect(splitTags('   ')).toEqual([])
    expect(splitTags(',，')).toEqual([])
  })

  it('单字符标签不会被丢掉（用户真的会打「书」这种短标签）', () => {
    expect(splitTags('a,b,书')).toEqual(['a', 'b', '书'])
  })

  it('保持输入顺序，不做去重与大小写归一（那是后端的职责）', () => {
    expect(splitTags('Ops,dev,ops')).toEqual(['Ops', 'dev', 'ops'])
  })

  it('不把逗号以外的分隔符当成分隔符', () => {
    expect(splitTags('a b\tc;d')).toEqual(['a b\tc;d'])
  })
})
