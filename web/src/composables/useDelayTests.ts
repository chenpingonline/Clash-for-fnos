import { computed, reactive, readonly, ref } from 'vue'
import { createDelayTest, loadDelayTestStatus, openDelayTestStream, type DelayTestStatus } from '@/services/delay-tests'
import type { ProxyNode } from '@/types/api'

export type NodeDelayState = { value: number; state: 'idle' | 'testing' | 'done' | 'timeout' | 'error' }

const idleStatus: DelayTestStatus = { state: 'idle', names: [], results: [] }
const status = ref<DelayTestStatus>(idleStatus)
const delays = reactive(new Map<string, NodeDelayState>())
const waiting = new Map<string, Set<(value: DelayTestStatus) => void>>()
let closeStream: (() => void) | null = null
let connectedJobId = ''
let loading: Promise<DelayTestStatus> | null = null

function settle(next: DelayTestStatus) {
  if (!next.jobId || next.state === 'running') return
  const listeners = waiting.get(next.jobId)
  waiting.delete(next.jobId)
  listeners?.forEach(resolve => resolve(next))
}

function applyStatus(next: DelayTestStatus) {
  status.value = next
  if (next.state === 'running') {
    next.names.forEach(name => delays.set(name, { value: 0, state: 'testing' }))
  } else if (next.state === 'done') {
    const completed = new Set(next.results.map(result => result.name))
    next.results.forEach(result => delays.set(result.name, { value: result.delay, state: result.state }))
    next.names.forEach(name => {
      if (!completed.has(name)) delays.set(name, { value: 0, state: 'error' })
    })
  }
  settle(next)
}

function subscribe(next: DelayTestStatus) {
  if (next.state !== 'running' || !next.jobId || connectedJobId === next.jobId) return
  closeStream?.()
  connectedJobId = next.jobId
  closeStream = openDelayTestStream(next.jobId, update => {
    applyStatus(update)
    if (update.state !== 'running') {
      closeStream?.()
      closeStream = null
      connectedJobId = ''
    }
  })
}

export async function restoreDelayTest(): Promise<DelayTestStatus> {
  if (loading) return loading
  loading = loadDelayTestStatus().then(next => {
    applyStatus(next)
    subscribe(next)
    return next
  }).finally(() => { loading = null })
  return loading
}

function waitForCompletion(jobId: string): Promise<DelayTestStatus> {
  if (status.value.jobId === jobId && status.value.state !== 'running') return Promise.resolve(status.value)
  return new Promise(resolve => {
    const listeners = waiting.get(jobId) || new Set()
    listeners.add(resolve)
    waiting.set(jobId, listeners)
  })
}

export async function startDelayTest(names: string[]): Promise<DelayTestStatus> {
  const next = await createDelayTest(names)
  applyStatus(next)
  subscribe(next)
  return next.jobId ? waitForCompletion(next.jobId) : next
}

export function hydrateDelayHistory(proxies: Record<string, ProxyNode>) {
  for (const [name, proxy] of Object.entries(proxies)) {
    if (delays.has(name)) continue
    const value = Number(proxy.history?.[proxy.history.length - 1]?.delay || 0)
    delays.set(name, { value, state: value > 0 ? 'done' : 'idle' })
  }
}

export function useDelayTests() {
  return {
    delays: readonly(delays),
    status: readonly(status),
    testing: computed(() => status.value.state === 'running'),
    restore: restoreDelayTest,
    start: startDelayTest,
    hydrateHistory: hydrateDelayHistory,
  }
}
