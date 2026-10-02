import { expect, it } from 'vitest'
import { creationExpiry } from './expiry'

it('starts the default 24 hours at submission time', () => {
  const now = Date.parse('2026-10-02T06:00:00Z')
  expect(creationExpiry('24h', '', now)).toBe('2026-10-03T06:00:00.000Z')
  expect(creationExpiry('24h', '', now + 3600000)).toBe('2026-10-03T07:00:00.000Z')
})
it('supports short and long presets and explicit permanent links', () => {
  expect(creationExpiry('1h', '', 0)).toBe('1970-01-01T01:00:00.000Z')
  expect(creationExpiry('7d', '', 0)).toBe('1970-01-08T00:00:00.000Z')
  expect(creationExpiry('30d', '', 0)).toBe('1970-01-31T00:00:00.000Z')
  expect(creationExpiry('permanent', 'invalid')).toBeUndefined()
})
it('rejects missing, invalid and expired custom dates', () => {
  for (const date of ['', 'invalid', '1970-01-01T00:00:00Z']) {
    expect(() => creationExpiry('custom', date, 1000)).toThrow('请选择晚于当前时间')
  }
  expect(creationExpiry('custom', '2026-10-03T00:00:00Z', 0)).toBe('2026-10-03T00:00:00.000Z')
})
