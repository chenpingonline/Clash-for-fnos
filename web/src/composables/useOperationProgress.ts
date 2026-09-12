import { onBeforeUnmount, ref } from 'vue'
import { api } from '@/services/api'
import { openStatusStream } from '@/services/status-stream'

export function useOperationProgress() {
  const message = ref('')
  let generation = 0, disposed = false, closeProgress: (() => void) | null = null
  function stop() { generation++; closeProgress?.(); closeProgress = null }
  function show(text: string) { stop(); message.value = text }
  async function request<T>(path: string, options: RequestInit, statusPath: string, initial: string): Promise<T> {
    show(initial)
    const current = generation
    const id = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    closeProgress = openStatusStream<{ id?: string; active?: boolean; message?: string }>(statusPath, status => {
      if (!disposed && generation === current && status.id === id && status.active && status.message) message.value = status.message
    })
    const headers = new Headers(options.headers)
    headers.set('X-Network-Operation', id)
    try { return await api<T>(path, { ...options, headers }) }
    finally { if (generation === current) stop() }
  }
  onBeforeUnmount(() => { disposed = true; stop() })
  return { message, show, request }
}
