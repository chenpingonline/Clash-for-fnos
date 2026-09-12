import { onBeforeUnmount, ref } from 'vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { openStatusStream } from '@/services/status-stream'
import { notify } from '@/services/toast'

type AutosaveOptions<T> = {
  progressEndpoint?: string
  mergePending?: boolean
  onSaved?: (response: T) => void | Promise<void>
}

export function useAutosave<T = unknown>(endpoint: string, options: AutosaveOptions<T> = {}) {
  const state = ref<'idle' | 'pending' | 'saving' | 'saved' | 'error'>('idle')
  const message = ref('')
  let disposed = false
  let timer = 0, running = false, pending: unknown, lastJson = '', closeProgress: (() => void) | null = null

  async function flush() {
    window.clearTimeout(timer)
    if (running || pending === undefined) return
    const payload = pending, json = JSON.stringify(payload)
    pending = undefined; running = true; state.value = 'saving'; message.value = '正在保存…'
    const operationID = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    closeProgress?.()
    closeProgress = null
    if (options.progressEndpoint) {
      message.value = '正在提交，等待校验端口与配置…'
      closeProgress = openStatusStream<{ id?: string; active?: boolean; message?: string }>(options.progressEndpoint, status => {
        if (!disposed && status.id === operationID && status.active && status.message) message.value = status.message
      })
    }
    try {
      const response = await api<T>(endpoint, { ...jsonRequest('PUT', payload), headers: { 'Content-Type': 'application/json', 'X-Network-Operation': operationID } })
      await options.onSaved?.(response)
      lastJson = json; state.value = 'saved'; message.value = response && typeof response === 'object' && 'activation' in response && response.activation === 'saved-only' ? '校验通过 → 配置已保存；Core 已停止，启动后生效' : options.progressEndpoint ? '端口与配置校验通过 → 已应用 → 状态确认完成 → 已保存' : '已自动保存'
    } catch (cause) {
      state.value = 'error'; message.value = `保存失败：${errorMessage(cause)}`; notify(message.value, true)
    } finally {
      closeProgress?.(); closeProgress = null
      running = false
      if (pending !== undefined) timer = window.setTimeout(flush, 180)
    }
  }
  function queue(payload: unknown, delay = 250) {
    if (options.mergePending && pending !== undefined) payload = mergeAutosavePatch(pending, payload)
    const json = JSON.stringify(payload)
    if ((!options.mergePending && json === lastJson) || (pending !== undefined && json === JSON.stringify(pending))) return
    pending = JSON.parse(json); state.value = 'pending'; message.value = '等待自动保存…'
    window.clearTimeout(timer); timer = window.setTimeout(flush, Math.max(0, delay))
  }
  onBeforeUnmount(() => { disposed = true; window.clearTimeout(timer); closeProgress?.(); closeProgress = null })
  return { state, message, queue, flush }
}

function mergeAutosavePatch(previous: unknown, next: unknown): unknown {
  if (!previous || !next || typeof previous !== 'object' || typeof next !== 'object' || Array.isArray(previous) || Array.isArray(next)) return next
  const result: Record<string, unknown> = { ...previous }
  for (const [key, value] of Object.entries(next)) result[key] = mergeAutosavePatch(result[key], value)
  return result
}
