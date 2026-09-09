import { onBeforeUnmount, ref } from 'vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'

type AutosaveOptions<T> = {
  onSaved?: (response: T) => void | Promise<void>
}

export function useAutosave<T = unknown>(endpoint: string, options: AutosaveOptions<T> = {}) {
  const state = ref<'idle' | 'pending' | 'saving' | 'saved' | 'error'>('idle')
  const message = ref('')
  let timer = 0, running = false, pending: unknown, lastJson = ''

  async function flush() {
    window.clearTimeout(timer)
    if (running || pending === undefined) return
    const payload = pending, json = JSON.stringify(payload)
    pending = undefined; running = true; state.value = 'saving'; message.value = '正在保存…'
    try {
      const response = await api<T>(endpoint, jsonRequest('PUT', payload))
      await options.onSaved?.(response)
      lastJson = json; state.value = 'saved'; message.value = '已自动保存'
    } catch (cause) {
      state.value = 'error'; message.value = `保存失败：${errorMessage(cause)}`; notify(message.value, true)
    } finally {
      running = false
      if (pending !== undefined) timer = window.setTimeout(flush, 180)
    }
  }
  function queue(payload: unknown, delay = 250) {
    const json = JSON.stringify(payload)
    if (json === lastJson || (pending !== undefined && json === JSON.stringify(pending))) return
    pending = payload; state.value = 'pending'; message.value = '等待自动保存…'
    window.clearTimeout(timer); timer = window.setTimeout(flush, Math.max(0, delay))
  }
  onBeforeUnmount(() => window.clearTimeout(timer))
  return { state, message, queue, flush }
}
