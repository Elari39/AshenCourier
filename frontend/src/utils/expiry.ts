export type ExpiryChoice = '1h' | '24h' | '7d' | '30d' | 'permanent' | 'custom'

export function creationExpiry(choice: ExpiryChoice, custom: string, now = Date.now()): string | undefined {
  if (choice === 'permanent') return undefined
  if (choice === 'custom') {
    const time = new Date(custom).getTime()
    if (!custom || !Number.isFinite(time) || time <= now) throw new Error('请选择晚于当前时间的到期时间')
    return new Date(time).toISOString()
  }
  const hours = { '1h': 1, '24h': 24, '7d': 168, '30d': 720 }[choice]
  return new Date(now + hours * 60 * 60 * 1000).toISOString()
}
