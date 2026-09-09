import { describe, expect, it } from 'vitest'
import { bucketTrafficSamples, compactUTCOffset, formatQuotaPercent, listeningPorts, orderedProxyGroups, profileSource, stabilizeTrafficScale, subscriptionQuota, trafficSampleIndexAtTime, trafficScaleMaximum, trafficTooltipLeft } from './dashboard'

describe('dashboard presentation data', () => {
  it('keeps Mihomo group order before remaining groups', () => {
    const groups = orderedProxyGroups({
      groupOrder: ['节点选择'],
      proxies: {
        自动选择: { all: ['A'], now: 'A' },
        节点选择: { all: ['B'], now: 'B' },
        A: { type: 'ss' },
      },
    })
    expect(groups.map(item => item.name)).toEqual(['节点选择', '自动选择'])
  })

  it('shows every active listening port without assuming mixed-port', () => {
    expect(listeningPorts({ port: 7890, 'socks-port': 7891, 'mixed-port': 0, 'redir-port': 7892 })).toEqual([
      'HTTP 7890', 'SOCKS5 7891', 'Redir 7892',
    ])
    expect(listeningPorts({})).toEqual([])
  })

  it('normalizes subscription source, quota and UTC offset', () => {
    const profile = { id: '1', name: 'DASHO', type: 'remote', url: 'https://dasho.example/path', subscriptionInfo: { upload: 10, download: 20, total: 100 } }
    expect(profileSource(profile)).toBe('dasho.example')
    expect(subscriptionQuota(profile)).toEqual({ used: 30, remaining: 70, total: 100, percent: 30 })
    expect(compactUTCOffset('+08:00')).toBe('UTC+8')
    expect(formatQuotaPercent(0.0098)).toBe('<0.1%')
    expect(formatQuotaPercent(12.84)).toBe('12.8%')
  })

  it('raises the traffic chart scale with the largest visible sample', () => {
    expect(trafficScaleMaximum([623, 3200])).toBe(5000)
    expect(trafficScaleMaximum([623, 12_000])).toBe(20_000)
    expect(trafficScaleMaximum([])).toBe(2000)
  })

  it('folds traffic into absolute time buckets that do not change when new samples arrive', () => {
    expect(bucketTrafficSamples([
      { time: 1100, up: 10, down: 20 },
      { time: 1400, up: 30, down: 40 },
      { time: 2100, up: 50, down: 60 },
    ], 1000)).toEqual([
      { time: 1000, up: 20, down: 30 },
      { time: 2000, up: 50, down: 60 },
    ])

    const source = Array.from({ length: 600 }, (_, index) => ({
      time: index * 1000,
      up: index,
      down: index * 2,
    }))
    const bucketed = bucketTrafficSamples(source, 2000)
    const appended = bucketTrafficSamples([...source, { time: 600_000, up: 600, down: 1200 }], 2000)
    expect(bucketed).toHaveLength(300)
    expect(bucketed[0]).toEqual({ time: 0, up: 0.5, down: 1 })
    expect(bucketed[299]).toEqual({ time: 598000, up: 598.5, down: 1197 })
    expect(appended.slice(0, 300)).toEqual(bucketed)
  })

  it('selects the nearest traffic point by real time and keeps its tooltip inside the chart', () => {
    const samples = [0, 1000, 4000, 5000].map(time => ({ time, up: 0, down: 0 }))
    expect(trafficSampleIndexAtTime(samples, -100)).toBe(0)
    expect(trafficSampleIndexAtTime(samples, 2800)).toBe(2)
    expect(trafficSampleIndexAtTime(samples, 9000)).toBe(3)
    expect(trafficTooltipLeft(120, 600)).toBe(130)
    expect(trafficTooltipLeft(570, 600)).toBe(418)
    expect(trafficTooltipLeft(20, 160)).toBe(8)
  })

  it('expands the traffic scale immediately but delays a large shrink', () => {
    expect(stabilizeTrafficScale({ maximum: 20_000, lowSince: null }, 50_000, 1000)).toEqual({ maximum: 50_000, lowSince: null })
    expect(stabilizeTrafficScale({ maximum: 50_000, lowSince: null }, 20_000, 1000)).toEqual({ maximum: 50_000, lowSince: 1000 })
    expect(stabilizeTrafficScale({ maximum: 50_000, lowSince: 1000 }, 20_000, 30_999)).toEqual({ maximum: 50_000, lowSince: 1000 })
    expect(stabilizeTrafficScale({ maximum: 50_000, lowSince: 1000 }, 20_000, 31_000)).toEqual({ maximum: 20_000, lowSince: null })
    expect(stabilizeTrafficScale({ maximum: 50_000, lowSince: 1000 }, 30_000, 2000)).toEqual({ maximum: 50_000, lowSince: null })
  })
})
