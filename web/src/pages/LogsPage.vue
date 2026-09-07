<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import HighlightText from '@/components/HighlightText.vue'
import { api, APP_PREFIX, errorMessage, isAbortError } from '@/services/api'
import { containsLog, normalizeLog, type NormalizedLog } from '@/services/logs'
import { notify } from '@/services/toast'
import type { LogItem } from '@/types/api'

const items = ref<NormalizedLog[]>([]), query = ref(''), limit = ref(800), level = ref('info'), running = ref(true), loading = ref(true), error = ref('')
const box = ref<HTMLElement | null>(null)
const filtered = computed(() => items.value.filter(item => containsLog(item, query.value)))
const visible = computed(() => filtered.value.slice(-limit.value))
let stream: EventSource | null = null, controller: AbortController | null = null, renderFrame = 0

function scrollBottom() { cancelAnimationFrame(renderFrame); renderFrame = requestAnimationFrame(() => nextTick(() => { if (box.value) box.value.scrollTop = box.value.scrollHeight })) }
function stopStream() { stream?.close(); stream = null }
function startStream() {
  stopStream()
  if (!running.value) return
  stream = new EventSource(`${APP_PREFIX}/api/stream/logs?level=${encodeURIComponent(level.value)}`)
  stream.onmessage = event => {
    try { items.value.push(normalizeLog(JSON.parse(event.data) as LogItem)); if (items.value.length > 2000) items.value.shift(); scrollBottom() }
    catch { /* Keep streaming after malformed events. */ }
  }
}
async function load() {
  controller?.abort(); controller = new AbortController(); loading.value = true
  try { const data = await api<{ items?: LogItem[] }>(`/api/logs/history?level=${encodeURIComponent(level.value)}&limit=2000`, { signal: controller.signal }); items.value = (data.items || []).slice(-2000).map(normalizeLog); error.value = ''; startStream(); scrollBottom() }
  catch (cause) { if (!isAbortError(cause)) { error.value = errorMessage(cause); notify(error.value, true) } }
  finally { loading.value = false }
}
async function clear() {
  try { await api('/api/logs/history', { method: 'DELETE' }); items.value = []; notify('历史日志已清空') }
  catch (cause) { notify(errorMessage(cause), true) }
}
function toggle() { running.value = !running.value; running.value ? startStream() : stopStream() }
onMounted(load)
onBeforeUnmount(() => { controller?.abort(); stopStream(); cancelAnimationFrame(renderFrame) })
</script>

<template>
  <Teleport to="#page-actions"><div class="log-tools"><input v-model="query" type="search" class="log-search" placeholder="搜索日志" aria-label="搜索日志"><select v-model.number="limit" class="log-limit-select" aria-label="显示行数"><option v-for="value in [100, 200, 500, 800, 2000]" :key="value" :value="value">{{ value }} 行</option></select><select v-model="level" class="log-level-select" aria-label="日志级别" @change="load"><option v-for="value in ['debug', 'info', 'warning', 'error']" :key="value">{{ value }}</option></select><button class="ghost" @click="clear">清空</button><button @click="toggle">{{ running ? '停止' : '继续' }}</button></div></Teleport>
  <div class="log-summary muted">{{ loading ? '正在读取历史日志…' : error ? `读取失败：${error}` : `显示 ${visible.length} / ${filtered.length} 条${query ? '匹配日志' : ''} · 最近 ${items.length} 条日志中筛选` }}</div>
  <div ref="box" class="logs logs-full"><div v-for="(item, index) in visible" :key="`${index}-${item.time}`" class="log-line" :class="`log-${item.level}`"><span><HighlightText :text="item.time" :query="query" /></span><span><HighlightText :text="item.level" :query="query" /></span><span><HighlightText :text="item.message" :query="query" /></span></div><div v-if="!visible.length && !loading" class="empty">{{ query ? '没有匹配的日志' : '暂无日志' }}</div></div>
</template>
