import { describe, expect, it } from 'vitest'
import { containsLog, highlightParts, normalizeLog } from './logs'

describe('log presentation', () => {
  it('filters case-insensitively and normalizes warning', () => {
    const item = normalizeLog({ time: '2026-09-05T11:00:00Z', level: 'warning', message: 'ChatGPT.com [a+b] <img>' })
    expect(item.level).toBe('warn')
    expect(containsLog(item, 'chatgpt')).toBe(true)
    expect(containsLog(item, 'missing')).toBe(false)
  })

  it('highlights literal metacharacters without producing HTML', () => {
    expect(highlightParts('ChatGPT.com [a+b] <img>', '[a+b]')).toEqual([
      { text: 'ChatGPT.com ', match: false },
      { text: '[a+b]', match: true },
      { text: ' <img>', match: false },
    ])
  })
})
