import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  create: vi.fn(),
  load: vi.fn(),
  streamUpdate: null as null | ((status: unknown) => void),
}))

vi.mock('@/services/delay-tests', () => ({
  createDelayTest: mocks.create,
  loadDelayTestStatus: mocks.load,
  openDelayTestStream: vi.fn((_jobId: string, update: (status: unknown) => void) => {
    mocks.streamUpdate = update
    return vi.fn()
  }),
}))

describe('useDelayTests', () => {
  beforeEach(() => {
    mocks.create.mockReset()
    mocks.load.mockReset()
    mocks.streamUpdate = null
  })

  it('shares a restored running job across page composable instances', async () => {
    mocks.load.mockResolvedValue({ jobId: 'delay-1', state: 'running', names: ['node-a'], results: [] })
    const { useDelayTests } = await import('./useDelayTests')
    const proxiesPage = useDelayTests()
    await proxiesPage.restore()

    expect(proxiesPage.testing.value).toBe(true)
    expect(proxiesPage.delays.get('node-a')).toEqual({ value: 0, state: 'testing' })

    const dashboardPage = useDelayTests()
    expect(dashboardPage.testing.value).toBe(true)
    expect(dashboardPage.delays.get('node-a')?.state).toBe('testing')

    mocks.streamUpdate?.({
      jobId: 'delay-1', state: 'done', names: ['node-a'],
      results: [{ name: 'node-a', delay: 24, state: 'done' }],
    })
    expect(dashboardPage.testing.value).toBe(false)
    expect(proxiesPage.delays.get('node-a')).toEqual({ value: 24, state: 'done' })
  })
})
