import { describe, expect, it } from 'vitest'
import { parseProxyGroupSortPreferences, shouldShowProxyNode, sortProxyNodeNames } from './proxy-view'

const names = ['香港 10', 'DIRECT', '香港 2', '美国']

describe('proxy group node sorting', () => {
  it('preserves the configuration order by default', () => {
    expect(sortProxyNodeNames(names, 'default', () => 0)).toEqual(names)
  })

  it('sorts measured nodes by delay and keeps unmeasured nodes last', () => {
    const delays = new Map([['香港 10', 180], ['香港 2', 42], ['美国', 95]])
    expect(sortProxyNodeNames(names, 'delay', name => delays.get(name) || 0)).toEqual(['香港 2', '美国', '香港 10', 'DIRECT'])
  })

  it('sorts names naturally without changing the source list', () => {
    expect(sortProxyNodeNames(names, 'name', () => 0)).toEqual(['美国', '香港 2', '香港 10', 'DIRECT'])
    expect(names).toEqual(['香港 10', 'DIRECT', '香港 2', '美国'])
  })
})

describe('proxy group timeout visibility', () => {
  it('hides only timed-out nodes when the option is disabled', () => {
    expect(shouldShowProxyNode('timeout', false)).toBe(false)
    expect(shouldShowProxyNode('error', false)).toBe(true)
    expect(shouldShowProxyNode(undefined, false)).toBe(true)
    expect(shouldShowProxyNode('timeout', true)).toBe(true)
  })
})

describe('proxy group sort preferences', () => {
  it('restores only supported non-default per-group sorts', () => {
    expect(parseProxyGroupSortPreferences(JSON.stringify({ Bilibili: 'delay', AI: 'name', Game: 'default', invalid: 'fast' }))).toEqual({
      Bilibili: 'delay',
      AI: 'name',
    })
  })

  it('ignores missing, malformed, and non-object storage values', () => {
    expect(parseProxyGroupSortPreferences(null)).toEqual({})
    expect(parseProxyGroupSortPreferences('{bad')).toEqual({})
    expect(parseProxyGroupSortPreferences('[]')).toEqual({})
  })
})
