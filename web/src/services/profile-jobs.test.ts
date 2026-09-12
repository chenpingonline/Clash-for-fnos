import { afterEach, describe, expect, it, vi } from 'vitest'
import { streamProfileJob } from './profile-jobs'

class EventSourceMock {
  static instances: EventSourceMock[] = []
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(readonly url: string) { EventSourceMock.instances.push(this) }
  close() { this.closed = true }
  emit(value: unknown) { this.onmessage?.({ data: JSON.stringify(value) } as MessageEvent<string>) }
}

afterEach(() => {
  EventSourceMock.instances = []
  vi.unstubAllGlobals()
})

describe('streamProfileJob', () => {
  it('uses one event stream until the job completes', async () => {
    vi.stubGlobal('EventSource', EventSourceMock)
    const updates: unknown[] = []
    const completion = streamProfileJob('job/a', value => updates.push(value))
    const source = EventSourceMock.instances[0]!

    expect(source.url).toBe('/api/jobs/job%2Fa/stream')
    source.emit({ jobId: 'job/a', state: 'running', stage: 'download' })
    source.emit({ jobId: 'job/a', state: 'done', result: { ok: true } })

    await expect(completion).resolves.toMatchObject({ state: 'done' })
    expect(updates).toHaveLength(2)
    expect(source.closed).toBe(true)
    expect(EventSourceMock.instances).toHaveLength(1)
  })

  it('closes the stream when the caller aborts', async () => {
    vi.stubGlobal('EventSource', EventSourceMock)
    const controller = new AbortController()
    const completion = streamProfileJob('job-1', () => undefined, controller.signal)
    controller.abort()

    await expect(completion).rejects.toMatchObject({ name: 'AbortError' })
    expect(EventSourceMock.instances[0]?.closed).toBe(true)
  })
})
