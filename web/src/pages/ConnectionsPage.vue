<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { api, errorMessage } from '@/services/api'
import { formatBytes } from '@/services/format'
import { notify } from '@/services/toast'
import type { ConnectionItem, ConnectionsResponse } from '@/types/api'

const loading = ref(true)
const error = ref('')
const data = ref<ConnectionsResponse>({})
const items = computed(() => data.value.connections || [])

function target(item: ConnectionItem) {
  const metadata = item.metadata || {}
  const host = String(metadata.host || '').trim()
  const ip = String(metadata.destinationIP || '').trim()
  const port = String(metadata.destinationPort || '').trim()
  const value = host || ip || '-'
  return port && value !== '-' ? `${value}:${port}` : value
}
function targetTitle(item: ConnectionItem) {
  const value = target(item), host = String(item.metadata?.host || '').trim(), ip = String(item.metadata?.destinationIP || '').trim(), port = String(item.metadata?.destinationPort || '').trim()
  return host && ip && ip !== host ? `${value} · ${ip}${port ? `:${port}` : ''}` : value
}
async function load() {
  loading.value = true
  try { data.value = await api<ConnectionsResponse>('/api/connections'); error.value = '' }
  catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}
async function close(id?: string) {
  if (!id && !confirm('关闭所有当前连接？')) return
  try {
    await api(id ? `/api/connections/${encodeURIComponent(id)}` : '/api/connections', { method: 'DELETE' })
    notify(id ? '连接已关闭' : '已关闭全部连接')
    await load()
  } catch (cause) { notify(errorMessage(cause), true) }
}
onMounted(load)
</script>

<template>
  <AsyncState :loading="loading" :error="error">
    <div class="section-head"><div><h2>{{ items.length }} 个活动连接</h2><p>累计上传 {{ formatBytes(data.uploadTotal) }} · 下载 {{ formatBytes(data.downloadTotal) }}</p></div><button class="danger" @click="close()">关闭全部</button></div>
    <div v-if="items.length" class="card table-wrap"><table class="connections-table"><thead><tr><th>目标</th><th>进程</th><th>规则</th><th>代理链</th><th>上传</th><th>下载</th><th /></tr></thead><tbody><tr v-for="item in items" :key="item.id"><td class="conn-target-cell"><div class="conn-target" :title="targetTitle(item)">{{ target(item) }}</div></td><td>{{ item.metadata?.process || '-' }}</td><td><span class="tag">{{ item.rule || '-' }}</span><div class="muted" style="font-size:11px">{{ item.rulePayload || '' }}</div></td><td>{{ (item.chains || []).join(' → ') }}</td><td>{{ formatBytes(item.upload) }}</td><td>{{ formatBytes(item.download) }}</td><td><button class="iconbtn" :aria-label="`关闭 ${target(item)} 连接`" @click="close(item.id)">×</button></td></tr></tbody></table></div>
    <div v-else class="empty">当前没有活动连接</div>
  </AsyncState>
</template>
