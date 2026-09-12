<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { openStatusStream } from '@/services/status-stream'
import { notify } from '@/services/toast'
import type { NetworkSettingsResponse, ProxyEnvironmentManagement, ProxyEnvironmentResponse, RuntimeConfig, RuntimeMode, TunSetting } from '@/types/api'

type TunForm = Required<TunSetting>
type TunOperationStatus = { active?: boolean; enabled?: boolean; stage?: string; message?: string }

const defaultTun = (): TunForm => ({
  enabled: false,
  stack: 'mixed',
  mtu: 1500,
  routeExcludeAddress: [],
  autoRoute: true,
  autoRedirect: true,
  autoDetectInterface: true,
  dnsHijack: true,
  strictRoute: false,
})

const props = withDefaults(defineProps<{ config: RuntimeConfig; environment: ProxyEnvironmentResponse; online: boolean; variant?: 'cards' | 'dashboard' }>(), {
  variant: 'cards',
})
const emit = defineEmits<{ updated: [] }>()
const management = ref<ProxyEnvironmentManagement | null>(props.environment.management || null)
const saving = ref(false)
const runtimeMode = ref<RuntimeMode>(props.config.mode || 'rule')
const tun = ref<TunForm>(defaultTun())
const tunCapability = ref<NonNullable<NetworkSettingsResponse['tunCapability']>>({ supported: false })
const tunLoading = ref(true)
const tunSaving = ref(false)
const tunTarget = ref<boolean | null>(null)
const tunError = ref('')
const tunProgress = ref('')
let closeTunProgressStream: (() => void) | null = null
const modes: Array<{ key: RuntimeMode; label: string }> = [{ key: 'rule', label: '规则' }, { key: 'global', label: '全局' }, { key: 'direct', label: '直连' }]

watch(() => props.environment.management, value => { management.value = value || null })
watch(() => props.config.mode, value => { runtimeMode.value = value || 'rule' })

const enabled = computed(() => management.value?.settings?.enabled === true)
const tunEnabled = computed(() => tun.value.enabled)
const tunDisplayedEnabled = computed(() => tunTarget.value ?? tunEnabled.value)
const tunSupported = computed(() => tunCapability.value.supported === true)
const tunStatus = computed(() => {
  if (tunLoading.value) return '正在检测'
  if (tunSaving.value) return tunTarget.value ? '正在开启' : '正在关闭'
  if (tunError.value) return '状态读取失败'
  return tunEnabled.value ? '已开启' : '已关闭'
})
const tunDescription = computed(() => {
  if (tunError.value) return tunError.value
  if (!tunSupported.value && !tunEnabled.value) return tunCapability.value.message || '当前环境暂不支持 TUN'
  return '接管 fnOS 系统流量'
})
async function toggle(event: Event) {
  const next = (event.target as HTMLInputElement).checked
  saving.value = true
  try {
    const result = await api<ProxyEnvironmentResponse>('/api/system/proxy-environment', jsonRequest('PUT', { enabled: next }))
    if (!result.management) throw new Error(result.error || '未能确认系统代理状态')
    management.value = result.management
    notify(next ? (result.management.active ? '系统代理已开启' : '系统代理已配置，等待代理端口启用') : '系统代理已关闭，核心与监听端口保持运行')
    emit('updated')
  } catch (error) {
    notify(errorMessage(error), true)
  } finally {
    saving.value = false
  }
}

async function changeMode(mode: RuntimeMode) {
  if (saving.value || !props.online) return
  saving.value = true
  try {
    await api('/api/runtime-config', jsonRequest('PATCH', { mode }))
    runtimeMode.value = mode
    notify(`已切换到 ${{ rule: '规则', global: '全局', direct: '直连' }[mode]}`)
    emit('updated')
  } catch (error) {
    notify(errorMessage(error), true)
  } finally {
    saving.value = false
  }
}

async function loadTun() {
  tunLoading.value = true
  try {
    const result = await api<NetworkSettingsResponse>('/api/network/settings')
    tun.value = { ...defaultTun(), ...(result.settings?.tun || {}) }
    tunCapability.value = result.tunCapability || { supported: false }
    tunError.value = ''
  } catch (error) {
    tunError.value = errorMessage(error)
  } finally {
    tunLoading.value = false
  }
}

async function toggleTun(event: Event) {
  const input = event.target as HTMLInputElement
  const next = input.checked
  const previous = tun.value.enabled
  tunSaving.value = true
  tunTarget.value = next
  tunProgress.value = next ? '正在准备开启 TUN…' : '正在准备关闭 TUN…'
  closeTunProgressStream?.()
  closeTunProgressStream = openStatusStream<TunOperationStatus>('/api/network/tun/status', status => {
    if (tunSaving.value && status.active && status.message) tunProgress.value = status.message
  })
  try {
    const result = await api<{ enabled?: boolean }>('/api/network/tun', jsonRequest('PUT', { enabled: next }))
    if (result.enabled !== next) throw new Error('TUN 状态未按预期生效')
    tun.value = { ...tun.value, enabled: next }
    notify(next ? '虚拟网卡(TUN)模式已开启' : '虚拟网卡(TUN)模式已关闭')
    emit('updated')
  } catch (error) {
    tun.value = { ...tun.value, enabled: previous }
    input.checked = previous
    notify(errorMessage(error), true)
  } finally {
    tunSaving.value = false
    tunTarget.value = null
    closeTunProgressStream?.()
    closeTunProgressStream = null
    tunProgress.value = ''
  }
}

onMounted(loadTun)
onUnmounted(() => closeTunProgressStream?.())
</script>

<template>
  <section v-if="variant === 'dashboard'" class="card dashboard-runtime-panel" aria-labelledby="dashboard-runtime-title">
    <div class="dashboard-runtime-head">
      <h2 id="dashboard-runtime-title"><a class="dashboard-section-link" href="#settings"><span class="dashboard-section-title">运行控制</span><span class="dashboard-title-arrow" aria-hidden="true" /></a></h2>
      <span v-if="tunSaving" class="dashboard-tun-progress" role="status" aria-live="polite"><i aria-hidden="true" />{{ tunProgress }}</span>
    </div>
    <div class="dashboard-runtime-controls">
      <div class="dashboard-runtime-segment dashboard-runtime-proxy">
        <label class="dashboard-runtime-toggle">
          <span>系统代理</span>
          <span class="switch"><input type="checkbox" :checked="enabled" :disabled="saving || !management || (!online && !enabled)" aria-label="系统代理" @change="toggle"><span /></span>
        </label>
      </div>
      <div class="dashboard-runtime-segment dashboard-runtime-tun">
        <label class="dashboard-runtime-toggle">
          <span>虚拟网卡(TUN)模式</span>
          <span class="switch" :class="{ switching: tunSaving }"><input type="checkbox" :checked="tunDisplayedEnabled" :disabled="tunLoading || tunSaving || Boolean(tunError) || (!tunSupported && !tunEnabled)" :aria-busy="tunSaving" aria-label="虚拟网卡(TUN)模式" @change="toggleTun"><span /></span>
        </label>
        <a class="dashboard-settings-link" href="#settings?section=tun">详细设置</a>
      </div>
      <div class="dashboard-runtime-segment dashboard-runtime-mode">
        <span class="dashboard-mode-label">运行模式</span>
        <div class="mode-row dashboard-mode-row" :aria-label="`当前运行模式：${runtimeMode}`">
          <button v-for="item in modes" :key="item.key" class="mode-btn" :class="{ active: online && runtimeMode === item.key }" :aria-pressed="online && runtimeMode === item.key" :disabled="saving || !online" @click="changeMode(item.key)">{{ item.label }}</button>
        </div>
      </div>
    </div>
  </section>
  <div v-else class="runtime-control-grid">
    <div class="card section runtime-control-card system-proxy-card">
      <div class="runtime-control-head">
        <div><h2>系统代理</h2><p :class="enabled ? 'good-text' : 'muted-text'">{{ enabled ? '已开启' : '已关闭' }}</p></div>
        <label class="runtime-control-switch"><span class="switch"><input type="checkbox" :checked="enabled" :disabled="saving || !management || (!online && !enabled)" aria-label="系统代理" @change="toggle"><span /></span></label>
      </div>
      <div class="mode-row" :aria-label="`当前运行模式：${runtimeMode}`">
        <button v-for="item in modes" :key="item.key" class="mode-btn" :class="{ active: online && runtimeMode === item.key }" :disabled="saving || !online" @click="changeMode(item.key)">{{ item.label }}</button>
      </div>
    </div>
    <div class="card section runtime-control-card tun-quick-card" :class="{ 'tun-on': tunEnabled }">
      <div class="runtime-control-head">
        <div><h2>虚拟网卡(TUN)模式</h2><p :class="tunEnabled ? 'good-text' : tunSupported ? 'muted-text' : 'warn-text'">{{ tunStatus }}</p></div>
        <label class="runtime-control-switch"><span class="switch" :class="{ switching: tunSaving }"><input type="checkbox" :checked="tunDisplayedEnabled" :disabled="tunLoading || tunSaving || Boolean(tunError) || (!tunSupported && !tunEnabled)" :aria-busy="tunSaving" aria-label="虚拟网卡(TUN)模式" @change="toggleTun"><span /></span></label>
      </div>
      <div class="tun-quick-footer">
        <span :class="{ 'warn-text': !tunSupported && !tunEnabled }">{{ tunDescription }}</span>
        <a href="#settings?section=tun">详细设置</a>
      </div>
    </div>
  </div>
</template>
