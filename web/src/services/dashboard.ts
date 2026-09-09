import { normalizeSubscriptionInfo } from './format'
import type { ProfileItem, ProxiesResponse, ProxyNode, RuntimeConfig } from '@/types/api'

export interface DashboardProxyGroup {
  name: string
  proxy: ProxyNode
}

export function orderedProxyGroups(data: ProxiesResponse): DashboardProxyGroup[] {
  const entries = Object.entries(data.proxies || {}).filter(([, proxy]) => Boolean(proxy.all?.length))
  const available = new Map(entries)
  const seen = new Set<string>()
  const ordered: DashboardProxyGroup[] = []
  for (const name of data.groupOrder || []) {
    const proxy = available.get(name)
    if (!proxy || seen.has(name)) continue
    ordered.push({ name, proxy })
    seen.add(name)
  }
  for (const [name, proxy] of entries) {
    if (seen.has(name)) continue
    ordered.push({ name, proxy })
  }
  return ordered
}

export function listeningPorts(config: RuntimeConfig): string[] {
  const definitions: Array<[keyof RuntimeConfig, string]> = [
    ['mixed-port', 'Mixed'],
    ['port', 'HTTP'],
    ['socks-port', 'SOCKS5'],
    ['redir-port', 'Redir'],
    ['tproxy-port', 'TProxy'],
  ]
  return definitions.flatMap(([key, label]) => {
    const port = Number(config[key] || 0)
    return Number.isInteger(port) && port > 0 && port <= 65535 ? [`${label} ${port}`] : []
  })
}

export function profileSource(item: ProfileItem | null): string {
  if (!item) return '—'
  if (item.type === 'remote' && item.url) {
    try {
      return new URL(item.url).host || item.url
    } catch {
      return item.url
    }
  }
  return item.sourcePath || '本地配置'
}

export function subscriptionQuota(item: ProfileItem | null) {
  const info = normalizeSubscriptionInfo(item?.subscriptionInfo)
  if (!info) return null
  const used = info.upload + info.download
  const remaining = info.total > 0 ? Math.max(0, info.total - used) : 0
  const percent = info.total > 0 ? Math.max(0, Math.min(100, used / info.total * 100)) : 0
  return { used, remaining, total: info.total, percent }
}

export function compactUTCOffset(value?: string): string {
  const matched = String(value || '').match(/^([+-])(\d{2}):(\d{2})$/)
  if (!matched) return value || ''
  const hours = Number(matched[2])
  return `UTC${matched[1]}${hours}${matched[3] === '00' ? '' : `:${matched[3]}`}`
}

export function formatQuotaPercent(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0.0%'
  return value < 0.1 ? '<0.1%' : `${value.toFixed(1)}%`
}

export function memorySample(payload: unknown): number | null {
  if (!payload || typeof payload !== 'object') return null
  const value = Number((payload as { inuse?: unknown }).inuse)
  return Number.isFinite(value) && value > 0 ? value : null
}

export function trafficScaleMaximum(values: number[]): number {
  const value = Math.max(1024, ...values.filter(Number.isFinite)) * 1.08
  const magnitude = 10 ** Math.floor(Math.log10(value))
  const normalized = value / magnitude
  const step = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10
  return step * magnitude
}

export interface TrafficChartSample {
  time: number
  up: number
  down: number
}

export function trafficSampleIndexAtTime(samples: TrafficChartSample[], targetTime: number): number {
  if (samples.length <= 1) return 0
  let low = 0
  let high = samples.length - 1
  while (low < high) {
    const middle = Math.floor((low + high) / 2)
    if (samples[middle]!.time < targetTime) low = middle + 1
    else high = middle
  }
  if (low === 0) return 0
  const previous = samples[low - 1]!
  const current = samples[low]!
  return targetTime - previous.time <= current.time - targetTime ? low - 1 : low
}

export function trafficTooltipLeft(cursorX: number, chartWidth: number, tooltipWidth = 142, gap = 10, padding = 8): number {
  const rightSide = cursorX + gap
  if (rightSide + tooltipWidth <= chartWidth - padding) return rightSide
  return Math.max(padding, cursorX - gap - tooltipWidth)
}

export interface TrafficChartPoint { x: number; y: number }

/** Build a smooth segment that ends exactly on the real sample without overshooting its value. */
export function trafficCurveSegment(previous: TrafficChartPoint, current: TrafficChartPoint) {
  const middleX = previous.x + (current.x - previous.x) / 2
  return {
    control1: { x: middleX, y: previous.y },
    control2: { x: middleX, y: current.y },
    end: current,
  }
}

/** Keep completed history stable by anchoring every aggregate to an absolute time bucket. */
export function bucketTrafficSamples(samples: TrafficChartSample[], bucketDurationMs: number): TrafficChartSample[] {
  const duration = Math.max(1, Math.floor(bucketDurationMs))
  const buckets = new Map<number, { time: number; up: number; down: number; count: number }>()
  for (const sample of samples) {
    if (![sample.time, sample.up, sample.down].every(Number.isFinite)) continue
    const time = Math.floor(sample.time / duration) * duration
    const current = buckets.get(time) || { time, up: 0, down: 0, count: 0 }
    current.up += Math.max(0, sample.up)
    current.down += Math.max(0, sample.down)
    current.count += 1
    buckets.set(time, current)
  }
  return [...buckets.values()]
    .sort((left, right) => left.time - right.time)
    .map(item => ({ time: item.time, up: item.up / item.count, down: item.down / item.count }))
}

export interface TrafficScaleState {
  maximum: number
  lowSince: number | null
}

export function stabilizeTrafficScale(
  current: TrafficScaleState | undefined,
  targetMaximum: number,
  now: number,
  shrinkDelayMs = 30_000,
  shrinkThreshold = 0.45,
): TrafficScaleState {
  if (!current || targetMaximum > current.maximum) return { maximum: targetMaximum, lowSince: null }
  if (targetMaximum > current.maximum * shrinkThreshold) return { maximum: current.maximum, lowSince: null }
  const lowSince = current.lowSince ?? now
  if (now - lowSince >= shrinkDelayMs) return { maximum: targetMaximum, lowSince: null }
  return { maximum: current.maximum, lowSince }
}
