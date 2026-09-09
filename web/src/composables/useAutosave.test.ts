import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAutosave } from './useAutosave'

const mocks = vi.hoisted(() => ({ api: vi.fn() }))

vi.mock('@/services/api', () => ({
  api: mocks.api,
  errorMessage: (cause: unknown) => String(cause),
  jsonRequest: (method: string, body: unknown) => ({ method, body: JSON.stringify(body) }),
}))
vi.mock('@/services/toast', () => ({ notify: vi.fn() }))
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
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
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
