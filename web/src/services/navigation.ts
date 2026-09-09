type HistoryShortcut = Pick<KeyboardEvent, 'key' | 'altKey' | 'metaKey'>

export function isBrowserHistoryShortcut(event: HistoryShortcut): boolean {
  const key = event.key.toLocaleLowerCase()
  return key === 'browserback'
    || key === 'browserforward'
    || (event.altKey && (key === 'arrowleft' || key === 'arrowright'))
    || (event.metaKey && (key === '[' || key === ']'))
}
