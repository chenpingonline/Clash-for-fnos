import { afterEach, describe, expect, it, vi } from 'vitest'
import { openStatusStream } from './status-stream'

class EventSourceMock {
  static instance: EventSourceMock | null = null
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  closed = false
  constructor(readonly url: string) { EventSourceMock.instance = this }
  close() { this.closed = true }
}

afterEach(() => {
  EventSourceMock.instance = null
  vi.unstubAllGlobals()
})

describe('openStatusStream', () => {
  it('delivers status events over one closable connection', () => {
    vi.stubGlobal('EventSource', EventSourceMock)
    const update = vi.fn()
    const close = openStatusStream('/api/network/tun/status', update)
    const source = EventSourceMock.instance!

    expect(source.url).toBe('/api/network/tun/status/stream')
    source.onmessage?.({ data: '{"active":true,"message":"正在开启 TUN…"}' } as MessageEvent<string>)
    expect(update).toHaveBeenCalledWith({ active: true, message: '正在开启 TUN…' })
    close()
    expect(source.closed).toBe(true)
  })
})
