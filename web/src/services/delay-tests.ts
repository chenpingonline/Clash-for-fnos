import { api, APP_PREFIX, jsonRequest } from './api'

export type DelayTestResult = {
  name: string
  delay: number
  state: 'done' | 'timeout' | 'error'
  error?: string
}

export type DelayTestStatus = {
  jobId?: string
  state: 'idle' | 'running' | 'done'
  names: string[]
  results: DelayTestResult[]
  createdAt?: number
  updatedAt?: number
}

export function loadDelayTestStatus(): Promise<DelayTestStatus> {
  return api<DelayTestStatus>('/api/delays')
}

export function createDelayTest(names: string[]): Promise<DelayTestStatus> {
  return api<DelayTestStatus>('/api/delays', jsonRequest('POST', { names: [...new Set(names)] }))
}

export function openDelayTestStream(jobId: string, onUpdate: (status: DelayTestStatus) => void): () => void {
  const source = new EventSource(`${APP_PREFIX}/api/delays/${encodeURIComponent(jobId)}/stream`)
  source.onmessage = event => {
    try { onUpdate(JSON.parse(event.data) as DelayTestStatus) }
    catch { /* Ignore one malformed event; EventSource keeps the current task subscription alive. */ }
  }
  return () => source.close()
}
