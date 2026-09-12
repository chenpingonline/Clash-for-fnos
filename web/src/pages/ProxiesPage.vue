<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import BaseModal from '@/components/BaseModal.vue'
import { api, errorMessage, isAbortError, jsonRequest } from '@/services/api'
import { testDelayBatch, type DelayTestResult } from '@/services/delay-tests'
import { providerUpdatedText } from '@/services/format'
import { parseProxyGroupSortPreferences, shouldShowProxyNode, sortProxyNodeNames, type ProxyNodeSort } from '@/services/proxy-view'
import { notify } from '@/services/toast'
import type { DelayResponse, ProxiesResponse, ProxyNode, ProxyProvider, ProxyProvidersResponse } from '@/types/api'

type DelayState = { value: number; state: 'idle' | 'testing' | 'done' | 'timeout' | 'error' }
type ProxyGroup = { name: string; proxy: ProxyNode }
const GROUP_SORT_STORAGE_KEY = 'clash-for-fnos.proxy-group-sorts.v1'

function storedGroupSorts() {
  try { return parseProxyGroupSortPreferences(window.localStorage.getItem(GROUP_SORT_STORAGE_KEY)) }
  catch { return {} }
}

const loading = ref(true), error = ref(''), query = ref(''), testingAll = ref(false)
const groups = ref<ProxyGroup[]>([])
const expanded = reactive(new Set<string>())
const delays = reactive(new Map<string, DelayState>())
const testingGroups = reactive(new Set<string>())
const groupFilters = reactive(new Map<string, string>())
const groupSorts = reactive(new Map<string, ProxyNodeSort>(Object.entries(storedGroupSorts())))
const showTimeoutNodes = reactive(new Map<string, boolean>())
const providerOpen = ref(false), providerLoading = ref(false), providersLoaded = ref(false), providerError = ref('')
const providers = ref<Record<string, ProxyProvider>>({}), updatingProviders = ref(new Set<string>()), checkingProviders = ref(new Set<string>())
const delayController = new AbortController()

const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return groups.value.map(group => {
    const groupMatches = group.name.toLowerCase().includes(needle)
    const localNeedle = groupFilter(group.name).trim().toLowerCase()
    const nodes = (group.proxy.all || []).filter(node =>
      (!needle || groupMatches || node.toLowerCase().includes(needle)) &&
      (!localNeedle || node.toLowerCase().includes(localNeedle)) &&
      shouldShowProxyNode(delays.get(node)?.state, groupShowsTimeoutNodes(group.name)),
    )
    return {
      ...group,
      nodes: sortProxyNodeNames(nodes, groupSort(group.name), name => delays.get(name)?.value || 0),
    }
  }).filter(group => !needle || group.name.toLowerCase().includes(needle) || group.nodes.length)
})
const visibleNodeCount = computed(() => filtered.value.reduce((sum, group) => sum + group.nodes.length, 0))
const allExpanded = computed(() => groups.value.length > 0 && groups.value.every(group => expanded.has(group.name)))
const providerEntries = computed(() => Object.entries(providers.value).sort(([a], [b]) => a.localeCompare(b)))
const delayTestBusy = computed(() => testingAll.value || testingGroups.size > 0)

function latencyClass(value: number) { return !value ? '' : value < 100 ? 'good' : value < 250 ? 'warn' : 'bad' }
function groupFilter(name: string) { return groupFilters.get(name) || '' }
function groupSort(name: string): ProxyNodeSort { return groupSorts.get(name) || 'default' }
function groupShowsTimeoutNodes(name: string) { return showTimeoutNodes.get(name) !== false }
function updateGroupFilter(name: string, event: Event) {
  const value = (event.target as HTMLInputElement).value
  if (value) groupFilters.set(name, value)
  else groupFilters.delete(name)
}
function updateGroupSort(name: string, event: Event) {
  const sort = (event.target as HTMLSelectElement).value as ProxyNodeSort
  if (sort === 'default') groupSorts.delete(name)
  else groupSorts.set(name, sort)
  try { window.localStorage.setItem(GROUP_SORT_STORAGE_KEY, JSON.stringify(Object.fromEntries(groupSorts))) }
  catch { /* The current selection still works when storage is unavailable. */ }
  expanded.add(name)
}
function updateTimeoutVisibility(name: string, event: Event) {
  showTimeoutNodes.set(name, (event.target as HTMLInputElement).checked)
}
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
    const result = await api<DelayResponse>(`/api/delay/${encodeURIComponent(name)}`, { signal: delayController.signal })
    const value = Number(result.delay || 0)
    delays.set(name, { value, state: value > 0 ? 'done' : 'error' })
  } catch (cause) {
    if (isAbortError(cause)) return
    delays.set(name, { value: 0, state: /timeout|超时/i.test(errorMessage(cause)) ? 'timeout' : 'error' })
  }
}
async function testMany(names: string[], label: string) {
  const queue = [...new Set(names)].filter(testable)
  if (!queue.length) return notify('没有可测速节点')
  queue.forEach(name => delays.set(name, { value: 0, state: 'testing' }))
  try {
    await testDelayBatch(queue, (result: DelayTestResult) => {
      delays.set(result.name, { value: result.delay, state: result.state })
    }, delayController.signal)
    if (!delayController.signal.aborted) notify(label)
  } catch (cause) {
    if (isAbortError(cause)) return
    queue.forEach(name => {
      if (delays.get(name)?.state === 'testing') delays.set(name, { value: 0, state: 'error' })
    })
    notify(errorMessage(cause), true)
  }
}
function groupTesting(name: string) {
  return testingGroups.has(name)
}
async function testGroup(group: ProxyGroup) {
  if (delayTestBusy.value) return
  testingGroups.add(group.name)
  try { await testMany(group.proxy.all || [], `${group.name} 测速完成`) }
  finally { testingGroups.delete(group.name) }
}
async function locateCurrent(group: ProxyGroup) {
  const current = group.proxy.now?.trim()
  if (!current) return notify(`${group.name} 暂无当前节点`)
  expanded.add(group.name)
  if (groupFilter(group.name) && !current.toLowerCase().includes(groupFilter(group.name).trim().toLowerCase())) groupFilters.delete(group.name)
  if (!groupShowsTimeoutNodes(group.name) && delays.get(current)?.state === 'timeout') showTimeoutNodes.set(group.name, true)
  const needle = query.value.trim().toLowerCase()
  if (needle && !group.name.toLowerCase().includes(needle) && !current.toLowerCase().includes(needle)) query.value = ''
  await nextTick()
  const card = Array.from(document.querySelectorAll<HTMLElement>('.proxy-card')).find(item => item.dataset.groupName === group.name)
  const row = Array.from(card?.querySelectorAll<HTMLElement>('.node-row') || []).find(item => item.dataset.nodeName === current)
  if (!row) return notify(`未找到当前节点：${current}`, true)
  row.scrollIntoView({ block: 'center', behavior: 'smooth' })
  row.querySelector<HTMLElement>('.node-select')?.focus({ preventScroll: true })
}
function toggleAll() {
  if (allExpanded.value) expanded.clear()
  else groups.value.forEach(group => expanded.add(group.name))
}
async function loadProviders(showLoading = true) {
  if (showLoading) providerLoading.value = true
  try { const result = await api<ProxyProvidersResponse>('/api/providers'); providers.value = result.providers || {}; providersLoaded.value = true; providerError.value = '' }
  catch (cause) { providerError.value = errorMessage(cause) }
  finally { providerLoading.value = false }
}
function openProviderManager() { providerOpen.value = true; if (!providersLoaded.value) void loadProviders(true) }
async function updateProvider(name: string, silent = false) {
  updatingProviders.value = new Set(updatingProviders.value).add(name)
  try { await api(`/api/providers/${encodeURIComponent(name)}/update`, { method: 'PUT' }); if (!silent) { notify(`${name} 更新成功`); await loadProviders(false) }; return true }
  catch (cause) { if (!silent) notify(`${name}: ${errorMessage(cause)}`, true); return false }
  finally { const next = new Set(updatingProviders.value); next.delete(name); updatingProviders.value = next }
}
async function updateAllProviders() {
  let success = 0, failed = 0
  for (const [name] of providerEntries.value) (await updateProvider(name, true) ? success++ : failed++)
  await loadProviders(false)
  notify(`代理组更新完成：成功 ${success}${failed ? `，失败 ${failed}` : ''}`, failed > 0)
}
async function healthcheckProvider(name: string) {
  checkingProviders.value = new Set(checkingProviders.value).add(name)
  try { await api(`/api/providers/${encodeURIComponent(name)}/healthcheck`); notify(`${name} 健康检查完成`) }
  catch (cause) { notify(`${name}: ${errorMessage(cause)}`, true) }
  finally { const next = new Set(checkingProviders.value); next.delete(name); checkingProviders.value = next }
}
onMounted(load)
onBeforeUnmount(() => delayController.abort())
</script>

<template>
  <Teleport defer to="#page-actions">
    <div class="proxy-topbar-tools">
      <div class="proxy-search">
        <span aria-hidden="true">⌕</span>
        <input v-model="query" placeholder="搜索代理组或节点" aria-label="搜索代理组或节点" autocomplete="off">
        <button v-if="query" type="button" class="search-clear" aria-label="清空搜索" @click="query = ''">×</button>
      </div>
      <span class="search-result">{{ query ? `${filtered.length} 组 · ${visibleNodeCount} 个节点` : '' }}</span>
      <button class="ghost rule-provider-trigger" @click="openProviderManager">策略组 <span v-if="providersLoaded">{{ providerEntries.length }}</span></button>
      <button class="ghost" :disabled="loading || !groups.length" @click="toggleAll">{{ allExpanded ? '全部收起' : '全部展开' }}</button>
      <button class="ghost" :disabled="loading || !groups.length || delayTestBusy" @click="testingAll = true; testMany(groups.flatMap(item => item.proxy.all || []), '全部节点测速完成').finally(() => testingAll = false)">{{ testingAll ? '测速中…' : '延迟测试' }}</button>
    </div>
  </Teleport>

  <AsyncState :loading="loading" :error="error">
    <div class="proxy-groups">
      <section v-for="group in filtered" :key="group.name" class="card proxy-card" :class="{ expanded: expanded.has(group.name), 'search-expanded': query }" :data-group-name="group.name">
        <div class="proxy-card-head" @click="expanded.has(group.name) ? expanded.delete(group.name) : expanded.add(group.name)">
          <div class="proxy-summary"><div class="proxy-name-row"><h3>{{ group.name }}</h3><span class="tag">{{ group.proxy.type || 'Selector' }}</span></div><div class="proxy-current"><span>当前</span><strong>{{ group.proxy.now || '-' }}</strong><span v-if="snapshot(group.proxy.now || '').text !== '--'" class="current-delay" :class="snapshot(group.proxy.now || '').className">{{ snapshot(group.proxy.now || '').text }}</span></div></div>
          <div class="proxy-head-actions">
            <button class="proxy-group-tool proxy-locate ghost small" :disabled="!group.proxy.now" aria-label="定位当前节点" title="定位当前节点" @click.stop="locateCurrent(group)"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="6.5" /><path d="M12 2.5v3M12 18.5v3M2.5 12h3M18.5 12h3" /></svg></button>
            <button class="proxy-group-tool proxy-latency-test ghost small" :disabled="delayTestBusy" title="测试本组全部可测速节点" @click.stop="testGroup(group)">{{ groupTesting(group.name) ? '测试中' : '延迟测试' }}</button>
            <label class="proxy-sort-control" title="设置本组节点排序方式" @click.stop>
              <span>排序</span>
              <select :value="groupSort(group.name)" :aria-label="`${group.name} 排序方式`" @click.stop @change.stop="updateGroupSort(group.name, $event)">
                <option value="default">默认</option>
                <option value="delay">按延迟</option>
                <option value="name">按名称</option>
              </select>
            </label>
            <span class="node-count" :title="`${group.proxy.all?.length || 0} 个节点`">{{ group.proxy.all?.length || 0 }}</span>
            <span class="proxy-chevron" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="m5.5 7.5 4.5 4.5 4.5-4.5" /></svg></span>
          </div>
        </div>
        <div class="proxy-body">
          <div class="proxy-group-filter">
            <label class="proxy-filter-search"><span>筛选</span><input :value="groupFilter(group.name)" :aria-label="`${group.name} 筛选节点`" placeholder="输入节点名称" @input="updateGroupFilter(group.name, $event)"></label>
            <button v-if="groupFilter(group.name)" class="ghost small" @click="groupFilters.delete(group.name)">清除</button>
            <label class="proxy-timeout-toggle" title="关闭后隐藏测速结果为超时的节点"><input type="checkbox" :checked="groupShowsTimeoutNodes(group.name)" @change="updateTimeoutVisibility(group.name, $event)"><span>显示超时节点</span></label>
            <span>{{ group.nodes.length }} / {{ group.proxy.all?.length || 0 }} 个节点</span>
          </div>
          <div v-if="group.nodes.length" class="node-list"><div v-for="name in group.nodes" :key="name" class="node-row" :class="{ active: group.proxy.now === name }" :data-node-name="name"><button class="node-select" @click="select(group, name)"><span class="node-copy"><span class="node-name" :title="name">{{ name }}</span><span v-if="rawProxies[name]?.type" class="node-type">{{ rawProxies[name]?.type }}</span></span></button><button class="node-delay" :class="snapshot(name).className" title="单独测试该节点延迟" @click="testOne(name)">{{ snapshot(name).text }}</button></div></div>
          <div v-else class="proxy-group-empty">没有匹配的节点</div>
        </div>
      </section>
    </div>
  </AsyncState>

  <BaseModal :open="providerOpen" :title="`代理组 · ${providerEntries.length}`" @close="providerOpen = false">
    <div class="provider-modal-head"><p>管理当前配置中的 Proxy Providers，可单独更新或执行健康检查。</p><button v-if="providerEntries.length" class="ghost small" :disabled="updatingProviders.size > 0" @click="updateAllProviders">{{ updatingProviders.size ? '更新中…' : '全部更新' }}</button></div>
    <AsyncState :loading="providerLoading" :error="providerError">
      <div v-if="providerEntries.length" class="rule-provider-list">
        <div v-for="[name, provider] in providerEntries" :key="name" class="rule-provider-row proxy-provider-row">
          <div class="rule-provider-main"><strong :title="name">{{ name }}</strong><span>{{ provider.vehicleType || provider.type || 'Proxy Provider' }} · {{ provider.proxies?.length || 0 }} 个节点</span></div>
          <span class="rule-provider-updated">{{ providerUpdatedText(provider.updatedAt) }}</span>
          <div class="proxy-provider-actions"><button class="ghost small" :disabled="checkingProviders.has(name) || updatingProviders.has(name)" @click="healthcheckProvider(name)">{{ checkingProviders.has(name) ? '检测中…' : '健康检查' }}</button><button class="ghost small" :disabled="updatingProviders.has(name) || checkingProviders.has(name)" @click="updateProvider(name)">{{ updatingProviders.has(name) ? '更新中…' : '更新' }}</button></div>
        </div>
      </div>
      <div v-else class="empty compact">当前配置没有 Proxy Provider</div>
    </AsyncState>
    <div class="actions provider-modal-actions"><button class="ghost" @click="providerOpen = false">关闭</button></div>
  </BaseModal>
</template>
