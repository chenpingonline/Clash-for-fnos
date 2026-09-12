import { describe, expect, it } from 'vitest'
import { parseChangelog } from './changelog'

describe('parseChangelog', () => {
  it('separates releases while preserving their sections and source order', () => {
    expect(parseChangelog('# 日志\r\n简介\r\n## [2.0.0] - 2026-09-12\r\n### 新增\r\n- 功能\r\n## [1.0.0]\r\n旧内容')).toEqual([
      { version: '2.0.0', date: '2026-09-12', body: '### 新增\r\n- 功能' },
      { version: '1.0.0', date: '', body: '旧内容' },
    ])
  })
  it('keeps hundreds of releases independent and handles missing history', () => {
    const entries = parseChangelog(Array.from({ length: 300 }, (_, i) => `## [1.0.${300 - i}] - 2026-09-12\n- 内容 ${i}`).join('\n'))
    expect(entries).toHaveLength(300)
    expect(entries[0]?.body).toBe('- 内容 0')
    expect(entries[299]?.body).toBe('- 内容 299')
    expect(parseChangelog('# 暂无日志')).toEqual([])
  })
})
