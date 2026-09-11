<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import PortConflictHelp from '@/components/PortConflictHelp.vue'
import AsyncState from '@/components/AsyncState.vue'
import SystemProxyCard from '@/components/SystemProxyCard.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import { refreshCoreHealth, updateCoreBootstrap } from '@/composables/useCoreHealth'
import { compactUTCOffset, formatQuotaPercent, listeningPorts, memorySample, orderedProxyGroups, profileSource, subscriptionQuota, type DashboardProxyGroup } from '@/services/dashboard'
import { useOperationProgress } from '@/composables/useOperationProgress'
import { api, APP_PREFIX, errorMessage, isAbortError, jsonRequest } from '@/services/api'
import { formatBytes, formatTime } from '@/services/format'
import { parseProxyGroupSortPreferences, sortProxyNodeNames, type ProxyNodeSort } from '@/services/proxy-view'
import { notify } from '@/services/toast'
import type { CoreBootstrap, CoreHealth, CoreMode, DelayResponse, ExitLocationResponse, ProfileItem, ProfileJob, ProfilesResponse, ProxiesResponse, ProxyEnvironmentResponse, RuntimeConfig, TrafficHistoryResponse, TrafficSample } from '@/types/api'

type DelayState = 'idle' | 'testing' | 'done' | 'timeout' | 'error'
type NodeDelay = { value: number; state: DelayState }
const GROUP_SORT_STORAGE_KEY = 'clash-for-fnos.proxy-group-sorts.v1'
type CoreDownloadInfo = {
  tag?: string
  htmlUrl?: string
  target?: { os?: string; arch?: string }
  asset?: { name?: string; url?: string; size?: number; sha256?: string }
}

const operation = useOperationProgress()
const operationMessage = operation.message

const loading = ref(true)
const error = ref('')
const selectedCoreMode = ref<Exclude<CoreMode, 'auto'>>('managed')
const coreModeEdited = ref(false)
const retryingCore = ref(false)
const downloadingCore = ref(false)
const cancelingCoreDownload = ref(false)
const manualCoreInfo = ref<CoreDownloadInfo | null>(null)
const manualCoreLoading = ref(false)
const manualCoreError = ref('')
const uploadingCore = ref(false)
const coreFileInput = ref<HTMLInputElement | null>(null)
const retryCoreError = ref('')
const status = ref<CoreHealth | null>(null)
const environment = ref<ProxyEnvironmentResponse>({})
const traffic = ref({ up: 0, down: 0, upTotal: 0, downTotal: 0 })
const trafficFailed = ref(false)
const trafficHistory = ref<TrafficSample[]>([])
const connectionStatsFailed = ref(false)
const streamedMemory = ref<number | null>(null)
const groups = ref<DashboardProxyGroup[]>([])
const rawProxies = ref<ProxiesResponse['proxies']>({})
const selectedGroupName = ref('')
const profiles = ref<ProfileItem[]>([])
const exitLocation = ref<ExitLocationResponse | null>(null)
const proxyError = ref('')
const profileError = ref('')
const locationError = ref('')
const profileUpdating = ref(false)
const profileJob = ref<ProfileJob | null>(null)
const refreshing = ref(false)
const nodeSelecting = ref(false)
const delayState = ref<DelayState>('idle')
const delayValue = ref(0)
const nodeDelays = ref<Record<string, NodeDelay>>({})
const groupSorts = ref<Record<string, ProxyNodeSort>>((() => {
  try { return parseProxyGroupSortPreferences(window.localStorage.getItem(GROUP_SORT_STORAGE_KEY)) }
  catch { return {} }
})())
const testingGroup = ref(false)
const groupMenuOpen = ref(false)
const groupMenu = ref<HTMLElement | null>(null)
const nodeMenuOpen = ref(false)
const nodeMenu = ref<HTMLElement | null>(null)
let stream: EventSource | null = null
let memoryStream: EventSource | null = null
let retryTimer = 0
let bootstrapStatusTimer = 0
let statsTimer = 0
let statsController: AbortController | null = null
let profileJobTimer = 0
let stopped = false
let coreDownloadCancelRequested = false

const working = computed(() => ['checking', 'downloading', 'canceling', 'installing', 'starting'].includes(status.value?.bootstrap?.state || ''))
const downloadRequired = computed(() => status.value?.bootstrap?.state === 'download-required' && status.value.bootstrap.delivery === 'online')
const downloadAction = computed(() => downloadRequired.value && selectedCoreMode.value === 'managed')
const bootstrapActive = computed(() => working.value || retryingCore.value)
const canStopCoreDownload = computed(() => selectedCoreMode.value === 'managed' && status.value?.bootstrap?.delivery === 'online' && ['checking', 'downloading'].includes(status.value?.bootstrap?.state || '') && bootstrapActive.value)
const bootstrapActionText = computed(() => {
  const state = status.value?.bootstrap?.state
  if (state === 'downloading') return '正在下载…'
  if (state === 'canceling') return '正在停止…'
  if (retryingCore.value || state === 'checking') return '正在检测…'
  return downloadAction.value ? '下载 Core' : '重新检测'
})
const manualCoreAvailable = computed(() => selectedCoreMode.value === 'managed' && status.value?.bootstrap?.delivery === 'online' && !bootstrapActive.value)
const manualCoreDownloadURL = computed(() => manualCoreInfo.value?.asset?.url || manualCoreInfo.value?.htmlUrl || 'https://github.com/MetaCubeX/mihomo/releases/latest')
const config = computed<RuntimeConfig>(() => status.value?.configs || {})
const currentGroup = computed(() => groups.value.find(group => group.name === selectedGroupName.value) || groups.value[0] || null)
const currentNode = computed(() => currentGroup.value?.proxy.now || '')
const currentGroupSort = computed<ProxyNodeSort>(() => groupSorts.value[currentGroup.value?.name || ''] || 'default')
const currentGroupNodes = computed(() => sortProxyNodeNames(currentGroup.value?.proxy.all || [], currentGroupSort.value, name => nodeDelays.value[name]?.value || 0))
const groupSelectDisabled = computed(() => testingGroup.value || Boolean(proxyError.value) || !groups.value.length)
const nodeSelectDisabled = computed(() => nodeSelecting.value || testingGroup.value || Boolean(proxyError.value) || !currentGroup.value)
const currentProfile = computed(() => profiles.value.find(item => item.current) || null)
const quota = computed(() => subscriptionQuota(currentProfile.value))
const activePorts = computed(() => listeningPorts(config.value))
const memoryText = computed(() => {
  const memory = streamedMemory.value ?? status.value?.connections?.memory
  return typeof memory !== 'number' || !Number.isFinite(memory) ? '—' : formatBytes(memory)
})
const locationText = computed(() => {
  if (!exitLocation.value) return '—'
  const values = [exitLocation.value.city, exitLocation.value.region].filter(Boolean)
  return [...new Set(values)].join(' · ') || '—'
})
const timezoneText = computed(() => {
  if (!exitLocation.value) return '—'
  return [exitLocation.value.timezone, compactUTCOffset(exitLocation.value.utcOffset)].filter(Boolean).join(' · ') || '—'
})
const delayText = computed(() => {
  if (testingGroup.value) return '测速中…'
  if (delayState.value === 'testing') return '测速中…'
  if (delayState.value === 'timeout') return '超时'
  if (delayState.value === 'error') return '失败'
  return delayValue.value > 0 ? `${delayValue.value} ms` : '延迟测试'
})
const delayClass = computed(() => {
  if (testingGroup.value) return 'testing'
  if (delayState.value === 'testing') return 'testing'
  if (delayState.value === 'timeout' || delayState.value === 'error') return 'bad'
  return delayValue.value < 1 ? '' : delayValue.value < 100 ? 'good' : delayValue.value < 250 ? 'warn' : 'bad'
})
const showCurrentNodeDelay = computed(() => delayValue.value > 0 || delayState.value !== 'idle' || testingGroup.value)

function syncCurrentDelay() {
  const cached = nodeDelays.value[currentNode.value]
  if (cached) {
    delayValue.value = cached.value
    delayState.value = cached.state
    return
  }
  const history = rawProxies.value?.[currentNode.value]?.history || []
  delayValue.value = Number(history[history.length - 1]?.delay || 0)
  delayState.value = delayValue.value > 0 ? 'done' : 'idle'
}

function nodeDelayText(name: string) {
  const item = nodeDelays.value[name]
  if (item?.state === 'testing') return '测速中…'
  if (item?.state === 'timeout') return '超时'
  if (item?.state === 'error') return '失败'
  return item?.value ? `${item.value} ms` : '--'
}

function nodeDelayClass(name: string) {
  const item = nodeDelays.value[name]
  if (item?.state === 'testing') return 'testing'
  if (item?.state === 'timeout' || item?.state === 'error') return 'bad'
  return !item?.value ? '' : item.value < 100 ? 'good' : item.value < 250 ? 'warn' : 'bad'
}

function toggleGroupMenu() {
  if (groupSelectDisabled.value) return
  closeNodeMenu()
  groupMenuOpen.value = !groupMenuOpen.value
}

function closeGroupMenu() {
  groupMenuOpen.value = false
}

function toggleNodeMenu() {
  if (nodeSelectDisabled.value) return
  closeGroupMenu()
  nodeMenuOpen.value = !nodeMenuOpen.value
}

function closeNodeMenu() {
  nodeMenuOpen.value = false
}

function closeNodeMenuOnOutsidePointer(event: PointerEvent) {
  const target = event.target as Node
  if (groupMenuOpen.value && !groupMenu.value?.contains(target)) closeGroupMenu()
  if (nodeMenuOpen.value && !nodeMenu.value?.contains(target)) closeNodeMenu()
}

async function loadProxies() {
  try {
    const data = await api<ProxiesResponse>('/api/proxies')
    rawProxies.value = data.proxies || {}
    const nextDelays = { ...nodeDelays.value }
    for (const [name, proxy] of Object.entries(rawProxies.value)) {
      if (nextDelays[name]) continue
      const history = proxy.history || []
      const value = Number(history[history.length - 1]?.delay || 0)
      nextDelays[name] = { value, state: value > 0 ? 'done' : 'idle' }
    }
    nodeDelays.value = nextDelays
    groups.value = orderedProxyGroups(data)
    if (!groups.value.some(group => group.name === selectedGroupName.value)) selectedGroupName.value = groups.value[0]?.name || ''
    syncCurrentDelay()
    proxyError.value = ''
  } catch (cause) {
    proxyError.value = errorMessage(cause)
  }
}

async function loadProfiles() {
  try {
    const data = await api<ProfilesResponse>('/api/profiles')
    profiles.value = data.items || []
    profileError.value = ''
  } catch (cause) {
    profileError.value = errorMessage(cause)
  }
}

async function loadExitLocation() {
  try {
    exitLocation.value = await api<ExitLocationResponse>('/api/exit-location')
    locationError.value = ''
  } catch (cause) {
    exitLocation.value = null
    locationError.value = errorMessage(cause)
  }
}

async function loadDashboardDetails() {
  await Promise.all([loadProxies(), loadProfiles(), loadExitLocation()])
}

async function loadTrafficHistory() {
  try {
    const data = await api<TrafficHistoryResponse>('/api/traffic-history')
    trafficHistory.value = data.samples || []
  } catch {
    trafficHistory.value = []
  }
}

async function load() {
  const initialLoad = !status.value
  if (initialLoad) loading.value = true
  error.value = ''
  stream?.close()
  memoryStream?.close()
  streamedMemory.value = null
  statsController?.abort()
  window.clearTimeout(statsTimer)
  connectionStatsFailed.value = false
  try {
    const [health, proxyEnvironment] = await Promise.all([
      refreshCoreHealth(),
      api<ProxyEnvironmentResponse>('/api/system/proxy-environment').catch(cause => ({ error: errorMessage(cause) })),
    ])
    status.value = health
    if (!coreModeEdited.value) selectedCoreMode.value = (health.system?.coreMode || health.bootstrap?.mode) === 'external' ? 'external' : 'managed'
    environment.value = proxyEnvironment
    if (!health.online && health.bootstrap?.delivery === 'online') void loadManualCoreInfo()
    if (health.online) {
      startTraffic()
      startMemory()
      startConnectionStats()
      void loadDashboardDetails()
      void loadTrafficHistory()
    } else if (working.value) {
      retryTimer = window.setTimeout(load, 1500)
    }
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    if (initialLoad) loading.value = false
  }
}

async function refreshPage() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    if (!status.value) {
      await load()
      return
    }
    await Promise.all([refreshRuntime(), loadDashboardDetails()])
  } finally {
    refreshing.value = false
  }
}

async function refreshRuntime() {
  try {
    const [health, proxyEnvironment] = await Promise.all([
      refreshCoreHealth(),
      api<ProxyEnvironmentResponse>('/api/system/proxy-environment'),
    ])
    status.value = health
    if (!coreModeEdited.value) selectedCoreMode.value = (health.system?.coreMode || health.bootstrap?.mode) === 'external' ? 'external' : 'managed'
    environment.value = proxyEnvironment
  } catch (cause) {
    notify(errorMessage(cause), true)
  }
}

function startConnectionStats() {
  window.clearTimeout(statsTimer)
  statsTimer = window.setTimeout(updateConnectionStats, 2000)
}

async function updateConnectionStats() {
  statsController?.abort()
  statsController = new AbortController()
  try {
    const connections = await api<NonNullable<CoreHealth['connections']>>('/api/connection-stats', { signal: statsController.signal })
    if (status.value) status.value = { ...status.value, connections }
    connectionStatsFailed.value = false
  } catch (cause) {
    if (!isAbortError(cause)) connectionStatsFailed.value = true
  } finally {
    if (!stopped) statsTimer = window.setTimeout(updateConnectionStats, 2000)
  }
}

function startTraffic() {
  stream?.close()
  trafficFailed.value = false
  stream = new EventSource(`${APP_PREFIX}/api/stream/traffic`)
  stream.onmessage = event => {
    try {
      traffic.value = JSON.parse(event.data) as typeof traffic.value
      trafficFailed.value = false
    } catch {
      trafficFailed.value = true
    }
  }
  stream.onerror = () => { trafficFailed.value = true }
}

function startMemory() {
  memoryStream?.close()
  memoryStream = new EventSource(`${APP_PREFIX}/api/stream/memory`)
  memoryStream.onmessage = event => {
    try {
      const memory = memorySample(JSON.parse(event.data))
      if (memory !== null) streamedMemory.value = memory
    } catch {
      // EventSource reconnects automatically; keep the last valid sample visible.
    }
  }
}

function chooseGroup(name: string) {
  closeGroupMenu()
  closeNodeMenu()
  selectedGroupName.value = name
  syncCurrentDelay()
}

function updateCurrentGroupSort(event: Event) {
  const groupName = currentGroup.value?.name
  if (!groupName) return
  const sort = (event.target as HTMLSelectElement).value as ProxyNodeSort
  const next = { ...groupSorts.value }
  if (sort === 'default') delete next[groupName]
  else next[groupName] = sort
  groupSorts.value = next
  try { window.localStorage.setItem(GROUP_SORT_STORAGE_KEY, JSON.stringify(next)) }
  catch { /* The current selection still works when storage is unavailable. */ }
}

async function chooseNode(name: string) {
  closeNodeMenu()
  const group = currentGroup.value
  if (!group || !name || name === group.proxy.now) return
  nodeSelecting.value = true
  try {
    await api(`/api/proxies/${encodeURIComponent(group.name)}`, jsonRequest('PUT', { name }))
    group.proxy.now = name
    syncCurrentDelay()
    notify(`已切换到 ${name}`)
    await loadExitLocation()
  } catch (cause) {
    notify(errorMessage(cause), true)
  } finally {
    nodeSelecting.value = false
  }
}

function testableNode(name: string) {
  const proxy = rawProxies.value?.[name] || {}
  return !['direct', 'reject', 'pass'].includes(String(proxy.type || name).toLowerCase())
}

async function testNode(name: string) {
  nodeDelays.value = { ...nodeDelays.value, [name]: { value: 0, state: 'testing' } }
  if (name === currentNode.value) syncCurrentDelay()
  try {
    const result = await api<DelayResponse>(`/api/delay/${encodeURIComponent(name)}`)
    const value = Number(result.delay || 0)
    nodeDelays.value = { ...nodeDelays.value, [name]: { value, state: value > 0 ? 'done' : 'error' } }
  } catch (cause) {
    nodeDelays.value = { ...nodeDelays.value, [name]: { value: 0, state: /timeout|超时|abort/i.test(errorMessage(cause)) ? 'timeout' : 'error' } }
  }
  if (name === currentNode.value) syncCurrentDelay()
}

async function testCurrentGroup() {
  const group = currentGroup.value
  if (!group || testingGroup.value) return
  const queue = [...new Set(group.proxy.all || [])].filter(testableNode)
  if (!queue.length) return notify('当前代理组没有可测速节点')
  testingGroup.value = true
  let cursor = 0
  const worker = async () => { while (cursor < queue.length) await testNode(queue[cursor++]!) }
  try {
    await Promise.all(Array.from({ length: Math.min(6, queue.length) }, worker))
    notify(`${group.name} 测速完成`)
  } finally {
    testingGroup.value = false
    syncCurrentDelay()
  }
}

async function updateCurrentProfile() {
  const profile = currentProfile.value
  if (!profile || profile.type !== 'remote' || profileUpdating.value) return
  profileUpdating.value = true
  try {
    let job = await api<ProfileJob>(`/api/profiles/${profile.id}/update-activate`, { method: 'POST' })
    if (!job.jobId) throw new Error('未获取到订阅更新任务')
    profileJob.value = job
    while (!stopped) {
      if (job.state === 'done') {
        const unchanged = Boolean(job.result?.unchanged || job.result?.lastDownload?.unchanged)
        profileJob.value = { ...job, message: unchanged ? '订阅内容没有变化，无需重新应用' : '订阅已更新并应用，新节点已经生效' }
        notify(unchanged ? '订阅内容没有变化' : '订阅已更新并应用')
        await Promise.all([loadProfiles(), loadProxies(), refreshRuntime()])
        window.clearTimeout(profileJobTimer)
        profileJobTimer = window.setTimeout(() => { profileJob.value = null }, 2400)
        return
      }
      if (job.state === 'failed') {
        profileJob.value = { ...job, message: job.error ? `${job.message || '更新失败'}：${job.error}` : job.message }
        throw new Error(job.error || '订阅更新失败')
      }
      await new Promise(resolve => window.setTimeout(resolve, 500))
      try {
        job = await api<ProfileJob>(`/api/jobs/${job.jobId}`)
      } catch {
        profileJob.value = { ...job, state: 'running', message: '暂时无法读取任务状态，正在重试…' }
        await new Promise(resolve => window.setTimeout(resolve, 1500))
        continue
      }
      profileJob.value = job
    }
  } catch (cause) {
    notify(errorMessage(cause), true)
  } finally {
    profileUpdating.value = false
  }
}

async function retryBootstrap() {
  if (retryingCore.value || working.value) return
  downloadingCore.value = downloadAction.value
  retryingCore.value = true
  retryCoreError.value = ''
  window.clearTimeout(retryTimer)
  pollBootstrapStatus()
  try {
    await operation.request('/api/core/mode', jsonRequest('PUT', { mode: selectedCoreMode.value }), '/api/core/operation/status', '正在应用运行方式并检测 Core…')
    if (coreDownloadCancelRequested) return
    operation.show('Core 检测完成 → 正在验证 Controller 连接…')
    await api('/api/status')
    operation.show('检测完成 → Controller 连接成功')
    notify('已应用 Core 运行方式并重新检测')
  } catch (cause) {
    if (coreDownloadCancelRequested) {
      operation.show('Core 下载已停止')
      return
    }
    retryCoreError.value = errorMessage(cause)
    operation.show(`检测失败：${retryCoreError.value}`)
    notify(retryCoreError.value, true)
  } finally {
    coreModeEdited.value = false
    retryingCore.value = false
    downloadingCore.value = false
    coreDownloadCancelRequested = false
    window.clearTimeout(bootstrapStatusTimer)
    await load()
  }
}

async function stopCoreDownload() {
  if (!canStopCoreDownload.value || cancelingCoreDownload.value) return
  cancelingCoreDownload.value = true
  coreDownloadCancelRequested = true
  operation.show('正在停止 Core 下载…')
  try {
    applyBootstrapStatus(await api<CoreBootstrap>('/api/core/bootstrap/cancel', jsonRequest('POST', {})))
  } catch (cause) {
    coreDownloadCancelRequested = false
    retryCoreError.value = errorMessage(cause)
    operation.show(`停止下载失败：${retryCoreError.value}`)
    notify(retryCoreError.value, true)
  } finally {
    cancelingCoreDownload.value = false
  }
}

async function loadManualCoreInfo() {
  if (manualCoreLoading.value || manualCoreInfo.value) return
  manualCoreLoading.value = true
  manualCoreError.value = ''
  try {
    manualCoreInfo.value = await api<CoreDownloadInfo>('/api/core/download-info')
  } catch (cause) {
    manualCoreError.value = errorMessage(cause)
  } finally {
    manualCoreLoading.value = false
  }
}

function chooseCoreFile() {
  coreFileInput.value?.click()
}

async function uploadCoreFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file || uploadingCore.value) return
  uploadingCore.value = true
  retryCoreError.value = ''
  applyBootstrapStatus({ state: 'installing', mode: 'managed', delivery: 'online', progress: 0, message: '正在上传并校验 Mihomo Core' })
  try {
    const body = new FormData()
    body.append('core', file, file.name)
    const result = await api<{ version?: string }>('/api/core/manual-install', { method: 'POST', body })
    notify(`Mihomo Core ${result.version || ''} 已安装并通过检测`.replace('  ', ' '))
  } catch (cause) {
    retryCoreError.value = errorMessage(cause)
    notify(retryCoreError.value, true)
  } finally {
    input.value = ''
    uploadingCore.value = false
    await load()
  }
}

function applyBootstrapStatus(bootstrap: CoreBootstrap) {
  updateCoreBootstrap(bootstrap)
  if (!status.value) return
  status.value = {
    ...status.value,
    bootstrap,
    system: status.value.system ? { ...status.value.system, bootstrap } : status.value.system,
  }
}

async function pollBootstrapStatus() {
  if (!retryingCore.value || stopped) return
  try {
    applyBootstrapStatus(await api<CoreBootstrap>('/api/core/bootstrap/status'))
  } catch {
    // The primary operation owns error reporting; a transient progress read can be retried.
  }
  if (retryingCore.value && !stopped) bootstrapStatusTimer = window.setTimeout(pollBootstrapStatus, 350)
}

onMounted(() => {
  document.addEventListener('pointerdown', closeNodeMenuOnOutsidePointer)
  void load()
})
defineExpose({ refreshPage })
onBeforeUnmount(() => {
  stopped = true
  stream?.close()
  memoryStream?.close()
  statsController?.abort()
  window.clearTimeout(retryTimer)
  window.clearTimeout(bootstrapStatusTimer)
  window.clearTimeout(statsTimer)
  window.clearTimeout(profileJobTimer)
  document.removeEventListener('pointerdown', closeNodeMenuOnOutsidePointer)
})
</script>

<template>
  <AsyncState :loading="loading" :error="error">
    <template v-if="status && !status.online">
      <div class="card section bootstrap-card">
        <div class="section-head">
          <div><h2>{{ bootstrapActive ? '正在准备 Mihomo Core' : downloadAction ? '需要下载 Mihomo Core' : status.bootstrap?.state === 'error' ? 'Mihomo Core 启用失败' : 'Mihomo Core 未连接' }}</h2><p v-if="bootstrapActive || downloadAction">{{ status.bootstrap?.message || 'Manager 正在检查本机 Mihomo' }}</p></div>
          <select v-model="selectedCoreMode" class="bootstrap-mode-select" aria-label="Core 运行方式" :disabled="working || retryingCore" @change="coreModeEdited = true"><option value="managed">Manager 托管</option><option value="external">外部 Core</option></select>
        </div>
        <p class="hint">{{ downloadAction ? 'all 通用安装包不包含 Core。点击“下载 Core”后，Manager 将下载并校验当前设备架构匹配的官方版本，安装完成后自动启动和检测。' : '切换 Mihomo Core 管理模式后请点击“重新检测”' }}</p>
        <template v-if="bootstrapActive">
          <div v-if="status.bootstrap?.state === 'downloading'" class="bootstrap-progress"><span :style="{ width: `${Math.max(0, Math.min(100, Number(status.bootstrap?.progress || 0)))}%` }" /></div>
          <div class="hint">{{ status.bootstrap?.delivery === 'online' ? '当前是 all 通用安装包，Manager 会从官方 Release 下载并校验匹配的 Core。' : '当前架构安装包已内置官方 Mihomo Core，可本地校验后启用。' }}</div>
        </template>
        <template v-else>
          <div v-if="!downloadAction" class="local-warning">{{ retryCoreError || status.bootstrap?.error || (status.bootstrap?.state === 'stopped' ? status.bootstrap.message : status.error) || '未检测到可用 Core' }}</div>
          <PortConflictHelp v-if="!downloadAction" :managed="(status.system?.coreMode || status.bootstrap?.mode) === 'managed'" @updated="load" :error="retryCoreError || status.bootstrap?.error || status.error || ''" />

        </template>
        <div class="actions core-mode-actions"><button class="small" :disabled="retryingCore || working" @click="retryBootstrap">{{ bootstrapActionText }}</button><button v-if="canStopCoreDownload" class="danger small" :disabled="cancelingCoreDownload" @click="stopCoreDownload">{{ cancelingCoreDownload ? '正在停止…' : '停止下载' }}</button><a class="ghost btn small" href="#settings?section=core">打开设置</a><span v-if="operationMessage" class="inline-operation-state" :class="{ 'error-text': retryCoreError }" role="status" aria-live="polite">{{ operationMessage }}</span></div>
        <div v-if="manualCoreAvailable" class="core-manual-fallback">
          <div class="core-manual-copy"><strong>手动下载安装</strong><span>自动下载较慢或失败时，可下载当前设备架构对应的官方 `.gz` 文件，再上传安装。</span></div>
          <div class="actions core-manual-actions">
            <a class="ghost btn" :href="manualCoreDownloadURL" target="_blank" rel="noopener noreferrer">{{ manualCoreLoading ? '正在获取链接…' : manualCoreInfo?.asset?.url ? '下载官方 Core' : '打开官方 Release' }}</a>
            <button class="ghost" :disabled="uploadingCore" @click="chooseCoreFile">{{ uploadingCore ? '正在上传安装…' : '上传 Core 文件' }}</button>
            <input ref="coreFileInput" class="hidden" type="file" accept=".gz,application/gzip,application/x-gzip" @change="uploadCoreFile">
          </div>
          <small v-if="manualCoreInfo?.asset?.name" class="core-manual-file">当前设备：{{ manualCoreInfo.target?.arch || '未知架构' }} · 文件：{{ manualCoreInfo.asset.name }} · {{ formatBytes(manualCoreInfo.asset.size) }}</small>
          <small v-else-if="manualCoreError" class="core-manual-file error-text">暂时无法生成直链，请从官方 Release 页面选择当前设备架构文件。{{ manualCoreError }}</small>
          <small v-if="retryCoreError" class="core-manual-file error-text">{{ retryCoreError }}</small>
        </div>
      </div>
    </template>

    <template v-else-if="status">
      <section class="card dashboard-connection-panel" aria-labelledby="connection-overview-title">
        <div class="dashboard-overview-head">
          <h2 id="connection-overview-title"><a class="dashboard-section-link" href="#proxies"><span class="dashboard-section-title">当前连接</span><span class="dashboard-title-arrow" aria-hidden="true" /></a></h2>
          <span class="dashboard-online"><span class="core-dot online" />已连接</span>
        </div>

        <div class="dashboard-route-grid">
          <div class="dashboard-field dashboard-inline-field">
            <span class="dashboard-field-prefix">代理组</span>
            <div ref="groupMenu" class="dashboard-node-picker" @keydown.esc.stop="closeGroupMenu">
              <button class="dashboard-node-trigger" type="button" :disabled="groupSelectDisabled" aria-label="当前代理组" aria-haspopup="listbox" :aria-expanded="groupMenuOpen" aria-controls="dashboard-group-menu" @click="toggleGroupMenu">
                <span class="dashboard-node-trigger-name" :title="currentGroup?.name || ''">{{ currentGroup?.name || (proxyError ? '更新失败' : '—') }}</span>
                <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m5.5 7.5 4.5 4.5 4.5-4.5" /></svg>
              </button>
              <div v-if="groupMenuOpen" id="dashboard-group-menu" class="dashboard-node-menu dashboard-group-menu" role="listbox" aria-label="选择代理组">
                <button v-for="group in groups" :key="group.name" type="button" class="dashboard-node-option dashboard-group-option" :class="{ active: group.name === currentGroup?.name }" role="option" :aria-selected="group.name === currentGroup?.name" @click="chooseGroup(group.name)">
                  <span class="dashboard-node-option-name" :title="group.name">{{ group.name }}</span>
                </button>
              </div>
            </div>
          </div>
          <div class="dashboard-field dashboard-node-field dashboard-inline-field">
            <span class="dashboard-field-prefix">节点</span>
            <div ref="nodeMenu" class="dashboard-node-picker" @keydown.esc.stop="closeNodeMenu">
              <button class="dashboard-node-trigger" type="button" :disabled="nodeSelectDisabled" aria-label="当前节点" aria-haspopup="listbox" :aria-expanded="nodeMenuOpen" aria-controls="dashboard-node-menu" @click="toggleNodeMenu">
                <span class="dashboard-node-trigger-name" :title="currentNode">{{ currentNode || (proxyError ? '更新失败' : '—') }}</span>
                <svg viewBox="0 0 20 20" aria-hidden="true"><path d="m5.5 7.5 4.5 4.5 4.5-4.5" /></svg>
              </button>
              <div v-if="nodeMenuOpen" id="dashboard-node-menu" class="dashboard-node-menu" role="listbox" aria-label="选择节点">
                <button v-for="node in currentGroupNodes" :key="node" type="button" class="dashboard-node-option" :class="{ active: node === currentNode }" role="option" :aria-selected="node === currentNode" :disabled="nodeSelecting" @click="chooseNode(node)">
                  <span class="dashboard-node-option-name" :title="node">{{ node }}</span>
                  <span class="dashboard-node-delay-text" :class="nodeDelayClass(node)">{{ nodeDelayText(node) }}</span>
                </button>
              </div>
            </div>
          </div>
          <div class="dashboard-route-actions">
            <button class="dashboard-delay" :class="delayClass" :disabled="!currentGroup || testingGroup" title="测试当前代理组全部节点延迟" @click="testCurrentGroup">{{ delayText }}</button>
            <label class="dashboard-sort-control" title="设置当前代理组节点排序方式">
              <span>排序</span>
              <select :value="currentGroupSort" :disabled="!currentGroup || testingGroup" aria-label="当前代理组节点排序方式" @change="updateCurrentGroupSort">
                <option value="default">默认</option>
                <option value="delay">按延迟</option>
                <option value="name">按名称</option>
              </select>
            </label>
          </div>
        </div>

        <div class="dashboard-location-grid" :title="locationError || undefined">
          <div><span>出口 IP</span><strong class="mono" :class="{ 'error-text': locationError }">{{ exitLocation?.ip || (locationError ? '更新失败' : '—') }}</strong></div>
          <div><span>国家/地区</span><strong>{{ exitLocation?.country || '—' }}</strong></div>
          <div><span>位置</span><strong>{{ locationText }}</strong></div>
          <div><span>时区</span><strong>{{ timezoneText }}</strong></div>
        </div>
      </section>

      <SystemProxyCard variant="dashboard" :config="config" :environment="environment" :online="true" @updated="refreshRuntime" />

      <section class="card dashboard-subscription" :class="{ 'has-error': profileError }" aria-labelledby="dashboard-subscription-title">
        <div class="dashboard-subscription-heading">
          <div class="dashboard-subscription-heading-main">
            <h2 id="dashboard-subscription-title"><a class="dashboard-section-link" href="#profiles"><span class="dashboard-section-title">{{ currentProfile?.type === 'remote' ? '当前订阅' : '当前配置' }}</span><span class="dashboard-title-arrow" aria-hidden="true" /></a></h2>
            <div v-if="profileJob" class="dashboard-subscription-progress" :class="profileJob.state" role="status" aria-live="polite">
              <i v-if="profileJob.state === 'running'" aria-hidden="true" />
              <span>{{ profileJob.message }}</span>
            </div>
          </div>
          <button v-if="currentProfile?.type === 'remote'" class="dashboard-update-subscription" :disabled="profileUpdating" @click="updateCurrentProfile">{{ profileUpdating ? '更新订阅并应用中…' : '更新订阅并应用' }}</button>
        </div>
        <div class="subscription-identity">
          <strong>{{ currentProfile?.name || (profileError ? '更新失败' : '未识别') }}</strong>
          <small :title="currentProfile?.url || currentProfile?.sourcePath || ''">来自 {{ profileSource(currentProfile) }}</small>
        </div>
        <div class="subscription-updated"><span>更新时间</span><strong>{{ currentProfile ? formatTime(currentProfile.updatedAt) : '—' }}</strong></div>
        <div class="subscription-quota">
          <div class="subscription-quota-values">
            <span>已用 <strong>{{ quota ? formatBytes(quota.used) : '—' }}</strong></span>
            <span>剩余 <strong>{{ quota ? formatBytes(quota.remaining) : '—' }}</strong></span>
            <span>总量 <strong>{{ quota?.total ? formatBytes(quota.total) : '—' }}</strong></span>
            <b>{{ quota ? formatQuotaPercent(quota.percent) : '—' }}</b>
          </div>
          <div class="quota-track dashboard-quota-track"><span :style="{ width: `${quota?.percent || 0}%` }" /></div>
        </div>
      </section>

      <section class="card dashboard-live" aria-labelledby="dashboard-live-title">
        <h2 id="dashboard-live-title">实时状态与实时流量</h2>
        <div class="dashboard-metrics" aria-label="实时状态摘要">
          <div><span>活动连接</span><strong>{{ connectionStatsFailed ? '—' : (status.connections?.count ?? 0) }}</strong></div>
          <div><span>累计上传</span><strong class="up">{{ connectionStatsFailed ? '—' : formatBytes(status.connections?.uploadTotal) }}</strong></div>
          <div><span>累计下载</span><strong class="down">{{ connectionStatsFailed ? '—' : formatBytes(status.connections?.downloadTotal) }}</strong></div>
          <div><span>内核内存</span><strong>{{ memoryText }}</strong></div>
          <div class="dashboard-ports"><span>监听端口</span><strong class="mono" :title="activePorts.join(' · ')">{{ activePorts.length ? activePorts.join(' · ') : '—' }}</strong></div>
        </div>
        <TrafficChart embedded :up="traffic.up" :down="traffic.down" :failed="trafficFailed" :history="trafficHistory" />
      </section>
    </template>
  </AsyncState>
</template>
