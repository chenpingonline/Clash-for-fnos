import type { LogItem } from '@/types/api'

export interface NormalizedLog { time: string; level: string; message: string }

export function displayLogTime(value: unknown): string {
  if (!value) return new Date().toLocaleTimeString()
  const date = new Date(value as string | number)
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleTimeString()
}

export function normalizeLog(item: LogItem): NormalizedLog {
  return {
    time: displayLogTime(item.time),
    level: String(item.level || item.type || 'info').replace('warning', 'warn'),
    message: String(item.message || item.payload || ''),
  }
}

export function containsLog(item: NormalizedLog, query: string): boolean {
  const needle = query.trim().toLowerCase()
  return !needle || [item.time, item.level, item.message].some(value => value.toLowerCase().includes(needle))
}

export function highlightParts(text: string, query: string): Array<{ text: string; match: boolean }> {
  const needle = query.trim()
  if (!needle) return [{ text, match: false }]
  const escaped = needle.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const pattern = new RegExp(escaped, 'gi')
  const parts: Array<{ text: string; match: boolean }> = []
  let offset = 0
  for (const match of text.matchAll(pattern)) {
    if (match.index! > offset) parts.push({ text: text.slice(offset, match.index), match: false })
    parts.push({ text: match[0], match: true })
    offset = match.index! + match[0].length
  }
  if (offset < text.length) parts.push({ text: text.slice(offset), match: false })
  return parts.length ? parts : [{ text, match: false }]
}
