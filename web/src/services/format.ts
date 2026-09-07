export function formatBytes(value: unknown): string {
  let bytes = Number(value || 0)
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let index = 0
  while (bytes >= 1024 && index < units.length - 1) {
    bytes /= 1024
    index += 1
  }
  return `${bytes.toFixed(index ? 1 : 0)} ${units[index]}`
}

export const formatRate = (value: unknown): string => `${formatBytes(value)}/s`
export const formatTime = (value: unknown): string => value ? new Date(Number(value)).toLocaleString() : '从未'

export interface SubscriptionInfo {
  upload: number
  download: number
  total: number
  expire: number
}

export function normalizeSubscriptionInfo(raw: unknown): SubscriptionInfo | null {
  if (!raw || typeof raw !== 'object') return null
  const source = raw as Record<string, unknown>
  const pick = (...keys: string[]) => {
    for (const key of keys) {
      if (source[key] === undefined || source[key] === null) continue
      const number = Number(source[key])
      if (Number.isFinite(number) && number >= 0) return number
    }
    return 0
  }
  const info = {
    upload: pick('upload', 'Upload'),
    download: pick('download', 'Download'),
    total: pick('total', 'Total'),
    expire: pick('expire', 'Expire'),
  }
  return info.upload || info.download || info.total || info.expire ? info : null
}

export function providerUpdatedText(value: unknown): string {
  const raw = String(value || '')
  return !raw || /^0001-01-01/i.test(raw) ? '--' : raw
}
