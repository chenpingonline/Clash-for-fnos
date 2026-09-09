<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import SystemProxyCard from '@/components/SystemProxyCard.vue'
import TrafficChart from '@/components/TrafficChart.vue'
import { refreshCoreHealth } from '@/composables/useCoreHealth'
import { compactUTCOffset, formatQuotaPercent, listeningPorts, orderedProxyGroups, profileSource, subscriptionQuota, type DashboardProxyGroup } from '@/services/dashboard'
import { api, APP_PREFIX, errorMessage, isAbortError, jsonRequest } from '@/services/api'
import { formatBytes, formatTime } from '@/services/format'
import { notify } from '@/services/toast'
import type { CoreHealth, DelayResponse, ExitLocationResponse, ProfileItem, ProfilesResponse, ProxiesResponse, ProxyEnvironmentResponse, RuntimeConfig, TrafficHistoryResponse, TrafficSample } from '@/types/api'

type DelayState = 'idle' | 'testing' | 'done' | 'timeout' | 'error'

const loading = ref(true)
const error = ref('')
const status = ref<CoreHealth | null>(null)
const environment = ref<ProxyEnvironmentResponse>({})
const traffic = ref({ up: 0, down: 0, upTotal: 0, downTotal: 0 })
const trafficFailed = ref(false)
const trafficHistory = ref<TrafficSample[]>([])
const connectionStatsFailed = ref(false)
const groups = ref<DashboardProxyGroup[]>([])
const rawProxies = ref<ProxiesResponse['proxies']>({})
const selectedGroupName = ref('')
const profiles = ref<ProfileItem[]>([])
const exitLocation = ref<ExitLocationResponse | null>(null)
const proxyError = ref('')
const profileError = ref('')
const locationError = ref('')
const profileUpdating = ref(false)
const refreshing = ref(false)
const nodeSelecting = ref(false)
const delayState = ref<DelayState>('idle')
const delayValue = ref(0)
let stream: EventSource | null = null
let retryTimer = 0
let statsTimer = 0
let statsController: AbortController | null = null
let stopped = false

const working = computed(() => ['checking', 'downloading', 'installing', 'starting'].includes(status.value?.bootstrap?.state || ''))
const config = computed<RuntimeConfig>(() => status.value?.configs || {})
const currentGroup = computed(() => groups.value.find(group => group.name === selectedGroupName.value) || groups.value[0] || null)
const currentNode = computed(() => currentGroup.value?.proxy.now || '')
const currentProfile = computed(() => profiles.value.find(item => item.current) || null)
const quota = computed(() => subscriptionQuota(currentProfile.value))
const activePorts = computed(() => listeningPorts(config.value))
const memoryText = computed(() => {
  const memory = status.value?.connections?.memory
  return connectionStatsFailed.value || typeof memory !== 'number' || !Number.isFinite(memory) ? '—' : formatBytes(memory)
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
  if (delayState.value === 'testing') return '测速中…'
  if (delayState.value === 'timeout') return '超时'
  if (delayState.value === 'error') return '失败'
  return delayValue.value > 0 ? `${delayValue.value} ms` : '延迟测试'
})
const delayClass = computed(() => {
  if (delayState.value === 'testing') return 'testing'
  if (delayState.value === 'timeout' || delayState.value === 'error') return 'bad'
  return delayValue.value < 1 ? '' : delayValue.value < 100 ? 'good' : delayValue.value < 250 ? 'warn' : 'bad'
})

function syncCurrentDelay() {
  const history = rawProxies.value?.[currentNode.value]?.history || []
  delayValue.value = Number(history[history.length - 1]?.delay || 0)
  delayState.value = delayValue.value > 0 ? 'done' : 'idle'
}

async function loadProxies() {
  try {
    const data = await api<ProxiesResponse>('/api/proxies')
    rawProxies.value = data.proxies || {}
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
  statsController?.abort()
  window.clearTimeout(statsTimer)
  connectionStatsFailed.value = false
  try {
    const [health, proxyEnvironment] = await Promise.all([
      refreshCoreHealth(),
      api<ProxyEnvironmentResponse>('/api/system/proxy-environment').catch(cause => ({ error: errorMessage(cause) })),
    ])
    status.value = health
    environment.value = proxyEnvironment
    if (health.online) {
      startTraffic()
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

function chooseGroup(event: Event) {
  selectedGroupName.value = (event.target as HTMLSelectElement).value
  syncCurrentDelay()
}

async function chooseNode(event: Event) {
  const group = currentGroup.value
  const name = (event.target as HTMLSelectElement).value
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

async function testCurrentNode() {
  if (!currentNode.value || delayState.value === 'testing') return
  delayState.value = 'testing'
  try {
    const result = await api<DelayResponse>(`/api/delay/${encodeURIComponent(currentNode.value)}`)
    delayValue.value = Number(result.delay || 0)
    delayState.value = delayValue.value > 0 ? 'done' : 'error'
  } catch (cause) {
    delayValue.value = 0
    delayState.value = /timeout|超时|abort/i.test(errorMessage(cause)) ? 'timeout' : 'error'
  }
}

async function updateCurrentProfile() {
  const profile = currentProfile.value
  if (!profile || profile.type !== 'remote' || profileUpdating.value) return
  profileUpdating.value = true
  try {
    const result = await api<{ lastDownload?: { unchanged?: boolean } }>(`/api/profiles/${profile.id}/update`, { method: 'POST' })
    notify(result.lastDownload?.unchanged ? '订阅内容没有变化' : '订阅更新完成；需要时请到订阅配置页应用')
    await loadProfiles()
  } catch (cause) {
    notify(errorMessage(cause), true)
  } finally {
    profileUpdating.value = false
  }
}

async function retryBootstrap() {
  try {
    await api('/api/core/bootstrap/retry', jsonRequest('POST'))
    notify('Mihomo Core 已准备完成')
    await load()
  } catch (cause) {
    notify(errorMessage(cause), true)
  }
}

onMounted(load)
defineExpose({ refreshPage })
onBeforeUnmount(() => {
  stopped = true
  stream?.close()
  statsController?.abort()
  window.clearTimeout(retryTimer)
  window.clearTimeout(statsTimer)
})
</script>

<template>
  <AsyncState :loading="loading" :error="error">
    <template v-if="status && !status.online">
      <div class="card section bootstrap-card">
        <div class="section-head">
          <div><h2>{{ working ? '正在准备 Mihomo Core' : status.bootstrap?.state === 'error' ? 'Mihomo Core 启用失败' : 'Mihomo Core 尚未运行' }}</h2><p>{{ status.bootstrap?.message || status.error || 'Manager 正在检查本机 Mihomo' }}</p></div>
          <span v-if="status.bootstrap?.mode" class="tag">{{ status.bootstrap.mode === 'managed' ? 'Manager 托管' : '外部 Core' }}</span>
        </div>
        <template v-if="working">
          <div class="bootstrap-progress"><span :style="{ width: `${Math.max(3, Math.min(100, Number(status.bootstrap?.progress || 0)))}%` }" /></div>
          <div class="hint">{{ status.bootstrap?.delivery === 'online' ? '当前是 all 通用安装包，Manager 会从官方 Release 下载并校验匹配的 Core。' : '当前架构安装包已内置官方 Mihomo Core，可本地校验后启用。' }}</div>
        </template>
        <template v-else>
          <div class="local-warning">{{ status.bootstrap?.error || status.error || '未检测到可用 Core' }}</div>
          <div class="actions core-mode-actions"><button @click="retryBootstrap">重新检测</button><a class="ghost btn" href="#settings">打开设置</a></div>
        </template>
      </div>
      <SystemProxyCard :config="config" :environment="environment" :online="false" />
    </template>

    <template v-else-if="status">
      <section class="card dashboard-connection-panel" aria-labelledby="connection-overview-title">
        <div class="dashboard-overview-head">
          <h2 id="connection-overview-title">当前连接</h2>
          <span class="dashboard-online"><span class="core-dot online" />已连接</span>
        </div>

        <div class="dashboard-route-grid">
          <label class="dashboard-field">
            <span>代理组</span>
            <select :value="currentGroup?.name || ''" :disabled="Boolean(proxyError) || !groups.length" @change="chooseGroup">
              <option v-if="!groups.length" value="">{{ proxyError ? '更新失败' : '—' }}</option>
              <option v-for="group in groups" :key="group.name" :value="group.name">{{ group.name }}</option>
            </select>
          </label>
          <div class="dashboard-field dashboard-node-field">
            <span>节点</span>
            <div class="dashboard-node-control">
              <select :value="currentNode" :disabled="nodeSelecting || Boolean(proxyError) || !currentGroup" aria-label="当前节点" @change="chooseNode">
                <option v-if="!currentGroup" value="">{{ proxyError ? '更新失败' : '—' }}</option>
                <option v-for="node in currentGroup?.proxy.all || []" :key="node" :value="node">{{ node }}</option>
              </select>
              <button class="dashboard-delay" :class="delayClass" :disabled="!currentNode || delayState === 'testing'" title="重新测试当前节点延迟" @click="testCurrentNode">{{ delayText }}</button>
            </div>
          </div>
          <a class="dashboard-open-link" href="#proxies">打开代理节点</a>
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
          <h2 id="dashboard-subscription-title">{{ currentProfile?.type === 'remote' ? '当前订阅' : '当前配置' }}</h2>
          <button v-if="currentProfile?.type === 'remote'" class="dashboard-update-subscription" :disabled="profileUpdating" @click="updateCurrentProfile">{{ profileUpdating ? '更新中…' : '更新订阅' }}</button>
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
