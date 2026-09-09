import { describe, expect, it } from 'vitest'
import { containsRule, normalizeRules } from './rules'

describe('rule presentation', () => {
  it('normalizes API rules with stable display line numbers', () => {
    expect(normalizeRules([
      { index: 99, type: 'DomainSuffix', payload: 'example.com', proxy: 'DIRECT' },
      { type: 'IPCIDR', payload: '10.0.0.0/8', proxy: 'LAN' },
    ])).toEqual([
      { lineNo: 1, type: 'DomainSuffix', payload: 'example.com', proxy: 'DIRECT' },
      { lineNo: 2, type: 'IPCIDR', payload: '10.0.0.0/8', proxy: 'LAN' },
    ])
  })

  it('searches payload, type, and target case-insensitively', () => {
    const [rule] = normalizeRules([{ type: 'DomainSuffix', payload: 'Example.COM', proxy: 'DIRECT' }])
    expect(containsRule(rule!, 'example')).toBe(true)
    expect(containsRule(rule!, 'domainsuffix')).toBe(true)
    expect(containsRule(rule!, 'direct')).toBe(true)
    expect(containsRule(rule!, 'reject')).toBe(false)
  })
})
