import { afterEach, describe, expect, it, vi } from 'vitest'
import { createDelayTest, loadDelayTestStatus, openDelayTestStream } from './delay-tests'

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

describe('delay test jobs', () => {
  it('starts one backend job with de-duplicated node names', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      jobId: 'delay-1', state: 'running', names: ['a', 'b'], results: [],
    }), { status: 202, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(createDelayTest(['a', 'b', 'a'])).resolves.toMatchObject({ jobId: 'delay-1', state: 'running' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.parse(String(fetchMock.mock.calls[0]![1]?.body))).toEqual({ names: ['a', 'b'] })
  })

  it('restores the latest backend job snapshot', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      jobId: 'delay-1', state: 'done', names: ['a'], results: [{ name: 'a', delay: 12, state: 'done' }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } })))

    await expect(loadDelayTestStatus()).resolves.toMatchObject({
      state: 'done', results: [{ name: 'a', delay: 12, state: 'done' }],
    })
  })

  it('subscribes to job completion independently of a page component', () => {
    vi.stubGlobal('EventSource', EventSourceMock)
    const update = vi.fn()
    const close = openDelayTestStream('delay/a', update)
    const source = EventSourceMock.instance!

    expect(source.url).toBe('/api/delays/delay%2Fa/stream')
    source.onmessage?.({ data: '{"jobId":"delay/a","state":"done","names":["a"],"results":[]}' } as MessageEvent<string>)
    expect(update).toHaveBeenCalledWith({ jobId: 'delay/a', state: 'done', names: ['a'], results: [] })
    close()
    expect(source.closed).toBe(true)
  })
})
