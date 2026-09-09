<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { DelayResponse, ProxiesResponse, ProxyNode } from '@/types/api'

type DelayState = { value: number; state: 'idle' | 'testing' | 'done' | 'timeout' | 'error' }
type ProxyGroup = { name: string; proxy: ProxyNode }

const loading = ref(true), error = ref(''), query = ref(''), testingAll = ref(false)
const groups = ref<ProxyGroup[]>([])
const expanded = reactive(new Set<string>())
const delays = reactive(new Map<string, DelayState>())
const testingGroups = reactive(new Set<string>())

const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return groups.value.map(group => ({
    ...group,
    nodes: (group.proxy.all || []).filter(node => !needle || group.name.toLowerCase().includes(needle) || node.toLowerCase().includes(needle)),
  })).filter(group => !needle || group.name.toLowerCase().includes(needle) || group.nodes.length)
})
const visibleNodeCount = computed(() => filtered.value.reduce((sum, group) => sum + group.nodes.length, 0))
const allExpanded = computed(() => groups.value.length > 0 && groups.value.every(group => expanded.has(group.name)))

function latencyClass(value: number) { return !value ? '' : value < 100 ? 'good' : value < 250 ? 'warn' : 'bad' }
function snapshot(name: string) {
  const item = delays.get(name)
  if (item?.state === 'testing') return { text: '测速中…', className: 'testing' }
  if (item?.state === 'timeout') return { text: '超时', className: 'bad' }
  if (item?.state === 'error') return { text: '失败', className: 'bad' }
  return item?.value ? { text: `${item.value} ms`, className: latencyClass(item.value) } : { text: '--', className: '' }
}
function testable(name: string) {
  const node = groups.value.flatMap(group => group.proxy.all || []).includes(name)
  const proxy = rawProxies.value[name] || {}
  return node && !['direct', 'reject', 'pass'].includes(String(proxy.type || name).toLowerCase())
}
const rawProxies = ref<Record<string, ProxyNode>>({})

async function load() {
  loading.value = true
  try {
    const data = await api<ProxiesResponse>('/api/proxies')
    rawProxies.value = data.proxies || {}
    for (const [name, proxy] of Object.entries(rawProxies.value)) {
      const last = proxy.history?.[proxy.history.length - 1]?.delay || 0
      if (last > 0 && !delays.has(name)) delays.set(name, { value: last, state: 'done' })
    }
    const entries = Object.entries(rawProxies.value).filter(([, proxy]) => proxy.all?.length)
    const map = new Map(entries), seen = new Set<string>(), ordered: ProxyGroup[] = []
    for (const name of data.groupOrder || []) if (map.has(name) && !seen.has(name)) { ordered.push({ name, proxy: map.get(name)! }); seen.add(name) }
    for (const [name, proxy] of entries) if (!seen.has(name)) ordered.push({ name, proxy })
    groups.value = ordered
    error.value = ''
  } catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}
async function select(group: ProxyGroup, name: string) {
  try { await api(`/api/proxies/${encodeURIComponent(group.name)}`, jsonRequest('PUT', { name })); group.proxy.now = name; notify(`已切换到 ${name}`) }
  catch (cause) { notify(errorMessage(cause), true) }
}
async function testOne(name: string) {
  if (!testable(name)) return
  delays.set(name, { value: 0, state: 'testing' })
  try {
    const result = await api<DelayResponse>(`/api/delay/${encodeURIComponent(name)}`)
    const value = Number(result.delay || 0)
    delays.set(name, { value, state: value > 0 ? 'done' : 'error' })
  } catch (cause) { delays.set(name, { value: 0, state: /timeout|超时|abort/i.test(errorMessage(cause)) ? 'timeout' : 'error' }) }
}
async function testMany(names: string[], label: string) {
  const queue = [...new Set(names)].filter(testable)
  if (!queue.length) return notify('没有可测速节点')
  let cursor = 0
  const worker = async () => { while (cursor < queue.length) await testOne(queue[cursor++]!) }
  await Promise.all(Array.from({ length: Math.min(6, queue.length) }, worker))
  notify(label)
}
function groupTesting(name: string) {
  return testingAll.value || testingGroups.has(name)
}
async function testGroup(group: ProxyGroup) {
  if (groupTesting(group.name)) return
  testingGroups.add(group.name)
  try { await testMany(group.proxy.all || [], `${group.name} 测速完成`) }
  finally { testingGroups.delete(group.name) }
}
function toggleAll() {
  if (allExpanded.value) expanded.clear()
  else groups.value.forEach(group => expanded.add(group.name))
}
onMounted(load)
</script>

<template>
  <AsyncState :loading="loading" :error="error">
    <div class="section-head proxy-page-head"><div class="proxy-heading"><h2>代理组</h2><span class="proxy-group-count">{{ groups.length }} 个策略组</span></div><div class="actions proxy-toolbar"><div class="proxy-search"><span>⌕</span><input v-model="query" placeholder="搜索代理组或节点" autocomplete="off"><button v-if="query" class="search-clear" title="清空搜索" @click="query = ''">×</button></div><span class="search-result">{{ query ? `${filtered.length} 组 · ${visibleNodeCount} 个节点` : '' }}</span><button class="ghost" @click="toggleAll">{{ allExpanded ? '全部收起' : '全部展开' }}</button><button class="ghost" :disabled="testingAll" @click="testingAll = true; testMany(groups.flatMap(item => item.proxy.all || []), '全部节点测速完成').finally(() => testingAll = false)">{{ testingAll ? '测速中…' : '延迟测试' }}</button></div></div>
    <div class="proxy-groups">
      <section v-for="group in filtered" :key="group.name" class="card proxy-card" :class="{ expanded: expanded.has(group.name), 'search-expanded': query }">
        <div class="proxy-card-head" @click="expanded.has(group.name) ? expanded.delete(group.name) : expanded.add(group.name)"><div class="proxy-summary"><div class="proxy-name-row"><h3>{{ group.name }}</h3><span class="tag">{{ group.proxy.type || 'Selector' }}</span></div><div class="proxy-current"><span>当前</span><strong>{{ group.proxy.now || '-' }}</strong><span v-if="snapshot(group.proxy.now || '').text !== '--'" class="current-delay" :class="snapshot(group.proxy.now || '').className">{{ snapshot(group.proxy.now || '').text }}</span></div></div><div class="proxy-head-actions"><button class="proxy-test ghost small" :disabled="groupTesting(group.name)" @click.stop="testGroup(group)">{{ groupTesting(group.name) ? '测速中…' : '延迟测试' }}</button><span class="node-count">{{ group.proxy.all?.length || 0 }}</span><span class="proxy-chevron">⌄</span></div></div>
        <div class="proxy-body"><div class="node-list"><div v-for="name in group.nodes" :key="name" class="node-row" :class="{ active: group.proxy.now === name }"><button class="node-select" @click="select(group, name)"><span class="node-name">{{ name }}</span><span v-if="rawProxies[name]?.type" class="node-type">{{ rawProxies[name]?.type }}</span><span v-if="group.proxy.now === name" class="node-selected-mark">当前</span></button><button class="node-delay" :class="snapshot(name).className" title="单独测试该节点延迟" @click="testOne(name)">{{ snapshot(name).text }}</button></div></div></div>
      </section>
    </div>
  </AsyncState>
</template>
