export type ProxyNodeSort = 'default' | 'delay' | 'name'

export function parseProxyGroupSortPreferences(raw: string | null): Record<string, ProxyNodeSort> {
  if (!raw) return {}
  try {
    const value: unknown = JSON.parse(raw)
    if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
    return Object.fromEntries(Object.entries(value).filter(([name, sort]) =>
      Boolean(name) && (sort === 'delay' || sort === 'name'),
    )) as Record<string, ProxyNodeSort>
  } catch {
    return {}
  }
}

export function shouldShowProxyNode(delayState: string | undefined, showTimeoutNodes: boolean): boolean {
  return showTimeoutNodes || delayState !== 'timeout'
}

export function sortProxyNodeNames(
  names: readonly string[],
  sort: ProxyNodeSort,
  delayFor: (name: string) => number,
): string[] {
  if (sort === 'default') return [...names]
  return names.map((name, index) => ({ name, index })).sort((left, right) => {
    if (sort === 'name') {
      return left.name.localeCompare(right.name, 'zh-CN', { numeric: true, sensitivity: 'base' }) || left.index - right.index
    }
    const leftDelay = delayFor(left.name)
    const rightDelay = delayFor(right.name)
    const leftRank = leftDelay > 0 ? leftDelay : Number.POSITIVE_INFINITY
    const rightRank = rightDelay > 0 ? rightDelay : Number.POSITIVE_INFINITY
    return leftRank - rightRank || left.index - right.index
  }).map(item => item.name)
}
