import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAutosave } from './useAutosave'

const mocks = vi.hoisted(() => ({ api: vi.fn(), progress: vi.fn(), closeProgress: vi.fn() }))

vi.mock('@/services/api', () => ({
  api: mocks.api,
  errorMessage: (cause: unknown) => String(cause),
  jsonRequest: (method: string, body: unknown) => ({ method, body: JSON.stringify(body) }),
}))
vi.mock('@/services/toast', () => ({ notify: vi.fn() }))
vi.mock('@/services/status-stream', () => ({
  openStatusStream: (_path: string, onUpdate: (status: unknown) => void) => {
    mocks.progress.mockImplementation(onUpdate)
    return mocks.closeProgress
  },
}))
vi.mock('vue', async () => {
  const actual = await vi.importActual<typeof import('vue')>('vue')
  return { ...actual, onBeforeUnmount: vi.fn() }
})

describe('useAutosave', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.stubGlobal('window', {
      setTimeout: globalThis.setTimeout.bind(globalThis),
      clearTimeout: globalThis.clearTimeout.bind(globalThis),
    })
    mocks.api.mockReset()
    mocks.progress.mockReset()
    mocks.closeProgress.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('merges rapid explicit field changes without losing false values', async () => {
    mocks.api.mockResolvedValue({})
    const save = useAutosave('/api/network/settings', { mergePending: true })
    save.queue({ core: { ipv6: false } })
    save.queue({ core: { unifiedDelay: true } })
    save.queue({ tun: { routeExcludeAddress: [] } })
    await vi.runAllTimersAsync()
    expect(mocks.api).toHaveBeenCalledOnce()
    expect(JSON.parse(mocks.api.mock.calls[0]![1].body)).toEqual({
      core: { ipv6: false, unifiedDelay: true }, tun: { routeExcludeAddress: [] },
    })
  })

  it('keeps changes queued during an in-flight save and sends the latest value', async () => {
    let finish!: (value: unknown) => void
    mocks.api.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
      .mockResolvedValue({})
    const save = useAutosave('/api/network/settings', { mergePending: true })
    save.queue({ core: { ipv6: false } }, 0)
    await vi.advanceTimersByTimeAsync(1)
    save.queue({ core: { ipv6: true } })
    save.queue({ core: { unifiedDelay: false } })
    finish({})
    await vi.runAllTimersAsync()
    expect(mocks.api).toHaveBeenCalledTimes(2)
    expect(JSON.parse(mocks.api.mock.calls[1]![1].body)).toEqual({ core: { ipv6: true, unifiedDelay: false } })
  })

  it('passes the saved response to the caller', async () => {
    const response = { files: [{ path: '/etc/environment', variables: [] }] }
    const onSaved = vi.fn()
    mocks.api.mockResolvedValue(response)
    const autosave = useAutosave('/api/system/proxy-environment', { onSaved })

    autosave.queue({ enabled: false }, 0)
    await vi.runAllTimersAsync()

    expect(onSaved).toHaveBeenCalledOnce()
    expect(onSaved).toHaveBeenCalledWith(response)
    expect(autosave.state.value).toBe('saved')
  })
})

it('uses correlated backend progress and reports save failures', async () => {
  vi.useFakeTimers()
  vi.stubGlobal('window', { setTimeout: globalThis.setTimeout.bind(globalThis), clearTimeout: globalThis.clearTimeout.bind(globalThis) })
  let operation = ''
  let rejectSave!: (error: Error) => void
  mocks.api.mockImplementation((_path: string, options?: RequestInit) => {
    operation = (options?.headers as Record<string, string>)['X-Network-Operation'] ?? ''
    return new Promise((_, reject) => { rejectSave = reject })
  })
  const save = useAutosave('/api/network/settings', { progressEndpoint: '/api/network/settings/status' })
  save.queue({ mixed: { enabled: true, port: 9090 } }, 0)
  await vi.advanceTimersByTimeAsync(1)
  mocks.progress({ id: operation, active: true, message: '2/4 正在写入配置…' })
  expect(save.message.value).toBe('2/4 正在写入配置…')
  rejectSave(new Error('端口冲突'))
  await vi.advanceTimersByTimeAsync(250)
  expect(save.state.value).toBe('error')
  expect(save.message.value).toContain('端口冲突')
  expect(mocks.closeProgress).toHaveBeenCalled()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})
