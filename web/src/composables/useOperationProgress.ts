import { onBeforeUnmount, ref } from 'vue'
import { api } from '@/services/api'

export function useOperationProgress() {
  const message = ref('')
  let generation = 0, disposed = false, timer = 0
  function stop() { generation++; window.clearTimeout(timer) }
  function show(text: string) { stop(); message.value = text }
  async function request<T>(path: string, options: RequestInit, statusPath: string, initial: string): Promise<T> {
    show(initial)
    const current = generation
    const id = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    const poll = async () => {
      try {
        const status = await api<{ id?: string; active?: boolean; message?: string }>(statusPath)
        if (!disposed && generation === current && status.id === id && status.active && status.message) message.value = status.message
      } catch { /* The operation request supplies the final result. */ }
      if (!disposed && generation === current) timer = window.setTimeout(poll, 200)
    }
    void poll()
    const headers = new Headers(options.headers)
    headers.set('X-Network-Operation', id)
    try { return await api<T>(path, { ...options, headers }) }
    finally { if (generation === current) stop() }
  }
  onBeforeUnmount(() => { disposed = true; stop() })
  return { message, show, request }
}
