import { describe, expect, it } from 'vitest'
import { isBrowserHistoryShortcut } from './navigation'

describe('embedded navigation shortcuts', () => {
  it.each([
    { key: 'ArrowLeft', altKey: true, metaKey: false },
    { key: 'ArrowRight', altKey: true, metaKey: false },
    { key: '[', altKey: false, metaKey: true },
    { key: ']', altKey: false, metaKey: true },
    { key: 'BrowserBack', altKey: false, metaKey: false },
    { key: 'BrowserForward', altKey: false, metaKey: false },
  ])('blocks $key browser history navigation', event => {
    expect(isBrowserHistoryShortcut(event)).toBe(true)
  })

  it('keeps ordinary arrow-key editing available', () => {
    expect(isBrowserHistoryShortcut({ key: 'ArrowLeft', altKey: false, metaKey: false })).toBe(false)
  })
})
