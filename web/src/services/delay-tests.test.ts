import { afterEach, describe, expect, it, vi } from 'vitest'
import { testDelayBatch } from './delay-tests'

afterEach(() => vi.unstubAllGlobals())

describe('testDelayBatch', () => {
  it('uses one browser request and emits chunked node results', async () => {
    const encoder = new TextEncoder()
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('{"name":"a","delay":12,"state":"done"}\n{"name":"b"'))
        controller.enqueue(encoder.encode(',"state":"timeout"}\n'))
        controller.close()
      },
    })
    const fetchMock = vi.fn().mockResolvedValue(new Response(stream, {
      status: 200,
      headers: { 'Content-Type': 'application/x-ndjson' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    const results: unknown[] = []

    const completed = await testDelayBatch(['a', 'b', 'a'], result => results.push(result))

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.parse(String(fetchMock.mock.calls[0]![1]?.body))).toEqual({ names: ['a', 'b'] })
    expect(results).toEqual([
      { name: 'a', delay: 12, state: 'done' },
      { name: 'b', delay: 0, state: 'timeout' },
    ])
    expect(completed).toEqual(results)
  })

  it('surfaces a JSON error returned before streaming starts', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"error":"没有可测速节点"}', { status: 400 })))
    await expect(testDelayBatch([], () => undefined)).rejects.toThrow('没有可测速节点')
  })
})
