import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  beforeUnmount: undefined as (() => void) | undefined,
  mounted: undefined as (() => void) | undefined,
  getPlatformConfig: vi.fn(),
  on: vi.fn(),
  off: vi.fn(),
}))

vi.mock('vue', () => ({
  onMounted: (callback: () => void) => { mocks.mounted = callback },
  onBeforeUnmount: (callback: () => void) => { mocks.beforeUnmount = callback },
}))

vi.mock('@trimjs/web-app', () => ({
  TrimApp: class {
    isWeb = true
    isStandaloneWeb = false
    getPlatformConfig = mocks.getPlatformConfig
    $on = mocks.on
    $off = mocks.off
  },
}))

import { useFnosTheme } from './useFnosTheme'

describe('useFnosTheme', () => {
  const media = {
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }
  const root = {
    dataset: {} as Record<string, string>,
    style: {} as Record<string, string>,
  }

  beforeEach(() => {
    mocks.beforeUnmount = undefined
    mocks.mounted = undefined
    mocks.getPlatformConfig.mockReset()
    mocks.on.mockReset().mockResolvedValue(undefined)
    mocks.off.mockReset().mockResolvedValue(undefined)
    media.matches = false
    media.addEventListener.mockReset()
    media.removeEventListener.mockReset()
    root.dataset = {}
    root.style = {}
    vi.stubGlobal('matchMedia', vi.fn(() => media))
    vi.stubGlobal('document', { documentElement: root })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('reads the initial host theme and subscribes to real-time changes', async () => {
    mocks.getPlatformConfig.mockResolvedValue({ theme: 'dark' })
    useFnosTheme()
    mocks.mounted?.()
    await vi.waitFor(() => expect(mocks.on).toHaveBeenCalledOnce())

    expect(root.dataset.theme).toBe('dark')
    expect(mocks.on).toHaveBeenCalledWith('os/theme', expect.any(Function))

    const callback = mocks.on.mock.calls[0]?.[1] as (theme: unknown) => void
    callback({ theme: { theme: 'light' } })
    expect(root.dataset.theme).toBe('light')
  })

  it('uses the browser theme only when the host SDK is unavailable', async () => {
    mocks.getPlatformConfig.mockRejectedValue(new Error('standalone'))
    useFnosTheme()
    mocks.mounted?.()
    await Promise.resolve()

    expect(root.dataset.theme).toBe('light')
    expect(root.dataset.fnosThemeMode).toBe('system')
    expect(mocks.on).not.toHaveBeenCalled()
  })

  it('unsubscribes from the host event when unmounted', async () => {
    mocks.getPlatformConfig.mockResolvedValue({ theme: 'dark' })
    useFnosTheme()
    mocks.mounted?.()
    await vi.waitFor(() => expect(mocks.on).toHaveBeenCalledOnce())
    const callback = mocks.on.mock.calls[0]?.[1]

    mocks.beforeUnmount?.()

    expect(media.removeEventListener).toHaveBeenCalledWith('change', expect.any(Function))
    expect(mocks.off).toHaveBeenCalledWith('os/theme', callback)
  })
})
