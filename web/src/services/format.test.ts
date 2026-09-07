import { describe, expect, it } from 'vitest'
import { formatBytes, formatRate, normalizeSubscriptionInfo, providerUpdatedText } from './format'

describe('display formatters', () => {
  it('formats traffic and subscription metadata', () => {
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatRate(1024)).toBe('1.0 KB/s')
    expect(normalizeSubscriptionInfo({ Upload: '10', Download: 20, Total: 100 })).toEqual({ upload: 10, download: 20, total: 100, expire: 0 })
  })

  it('hides Mihomo zero timestamps', () => {
    expect(providerUpdatedText('0001-01-01T00:00:00Z')).toBe('--')
  })
})
