<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import SystemProxyCard from '@/components/SystemProxyCard.vue'
import { refreshCoreHealth } from '@/composables/useCoreHealth'
import { api, APP_PREFIX, errorMessage, jsonRequest } from '@/services/api'
import { formatBytes, formatRate } from '@/services/format'
import { notify } from '@/services/toast'
import type { CoreHealth, ProxyEnvironmentResponse, RuntimeConfig } from '@/types/api'

const loading = ref(true)
const error = ref('')
const status = ref<CoreHealth | null>(null)
const environment = ref<ProxyEnvironmentResponse>({})
const traffic = ref({ up: 0, down: 0, upTotal: 0, downTotal: 0 })
let stream: EventSource | null = null
let retryTimer = 0

const working = computed(() => ['checking', 'downloading', 'installing', 'starting'].includes(status.value?.bootstrap?.state || ''))
const config = computed<RuntimeConfig>(() => status.value?.configs || {})

async function load() {
  loading.value = true
  error.value = ''
  stream?.close()
  try {
    const [health, proxyEnvironment] = await Promise.all([
      refreshCoreHealth(),
      api<ProxyEnvironmentResponse>('/api/system/proxy-environment').catch(error => ({ error: errorMessage(error) })),
    ])
    status.value = health
    environment.value = proxyEnvironment
    if (health.online) startTraffic()
    else if (working.value) retryTimer = window.setTimeout(load, 1500)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}

function startTraffic() {
  stream?.close()
  stream = new EventSource(`${APP_PREFIX}/api/stream/traffic`)
  stream.onmessage = event => {
    try {
      const value = JSON.parse(event.data) as typeof traffic.value
      traffic.value = value
    } catch {
      // Ignore malformed stream events and keep the last valid sample.
    }
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
onBeforeUnmount(() => {
  stream?.close()
  window.clearTimeout(retryTimer)
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
      <div class="grid stats">
        <div class="card stat"><div class="label">核心版本</div><div class="value" style="font-size:20px">{{ status.version?.version || '-' }}</div><div class="sub">External Controller 在线</div></div>
        <div class="card stat"><div class="label">活动连接</div><div class="value">{{ status.connections?.count || 0 }}</div><div class="sub">实时连接数量</div></div>
        <div class="card stat"><div class="label">累计下载</div><div class="value">{{ formatBytes(status.connections?.downloadTotal) }}</div><div class="sub">核心启动以来</div></div>
        <div class="card stat"><div class="label">内存</div><div class="value">{{ formatBytes(status.connections?.memory) }}</div><div class="sub">Mihomo 当前占用</div></div>
      </div>
      <SystemProxyCard :config="config" :environment="environment" :online="true" />
      <div class="card section"><div class="section-head"><div><h2>实时流量</h2><p>数据来自 Mihomo /traffic</p></div></div><div class="traffic-wrap"><div class="meter"><span class="muted">上传</span><div class="big up">{{ formatRate(traffic.up) }}</div><small class="muted">累计 {{ formatBytes(traffic.upTotal || status.connections?.uploadTotal) }}</small></div><div class="meter"><span class="muted">下载</span><div class="big down">{{ formatRate(traffic.down) }}</div><small class="muted">累计 {{ formatBytes(traffic.downTotal || status.connections?.downloadTotal) }}</small></div></div></div>
      <div class="card section"><div class="section-head"><div><h2>端口与网络</h2><p>当前运行配置摘要</p></div></div><div class="table-wrap"><table><tbody><tr><td class="muted">Mixed Port</td><td class="mono">{{ config['mixed-port'] ?? '-' }}</td><td class="muted">Allow LAN</td><td>{{ config['allow-lan'] ? '开启' : '关闭' }}</td></tr><tr><td class="muted">HTTP Port</td><td class="mono">{{ config.port ?? '-' }}</td><td class="muted">SOCKS Port</td><td class="mono">{{ config['socks-port'] ?? '-' }}</td></tr><tr><td class="muted">IPv6</td><td>{{ config.ipv6 ? '开启' : '关闭' }}</td><td class="muted">TUN</td><td>{{ config.tun?.enable ? '开启' : '关闭' }}</td></tr></tbody></table></div></div>
    </template>
  </AsyncState>
</template>
