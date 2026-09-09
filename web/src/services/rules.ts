import type { RuleItem } from '@/types/api'

export interface NormalizedRule {
  lineNo: number
  type: string
  payload: string
  proxy: string
}

export function normalizeRules(items: RuleItem[]): NormalizedRule[] {
  return items.map((item, index) => ({
    lineNo: index + 1,
    type: String(item.type || 'Unknown'),
    payload: String(item.payload || ''),
    proxy: String(item.proxy || '-'),
  }))
}

export function containsRule(item: NormalizedRule, query: string): boolean {
  const needle = query.trim().toLocaleLowerCase()
  return !needle || [item.payload, item.type, item.proxy].some(value => value.toLocaleLowerCase().includes(needle))
}
