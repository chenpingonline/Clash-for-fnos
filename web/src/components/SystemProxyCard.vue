<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { NetworkSettingsResponse, ProxyEnvironmentManagement, ProxyEnvironmentResponse, RuntimeConfig, RuntimeMode, TunSetting } from '@/types/api'

type TunForm = Required<TunSetting>

const defaultTun = (): TunForm => ({
  enabled: false,
  stack: 'mixed',
  mtu: 9000,
  autoRoute: true,
  autoRedirect: true,
  autoDetectInterface: true,
  dnsHijack: true,
  strictRoute: false,
})

const props = defineProps<{ config: RuntimeConfig; environment: ProxyEnvironmentResponse; online: boolean }>()
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
const modes: Array<{ key: RuntimeMode; label: string }> = [{ key: 'rule', label: '规则' }, { key: 'global', label: '全局' }, { key: 'direct', label: '直连' }]

watch(() => props.environment.management, value => { management.value = value || null })
watch(() => props.config.mode, value => { runtimeMode.value = value || 'rule' })

const enabled = computed(() => management.value?.settings?.enabled === true)
const tunEnabled = computed(() => tun.value.enabled)
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
  if (saving.value || !management.value?.active) return
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
  try {
    await api('/api/network/settings', jsonRequest('PUT', { tun: { ...tun.value, enabled: next } }))
    await loadTun()
    if (tun.value.enabled !== next) throw new Error('TUN 状态未按预期生效')
    notify(next ? 'TUN 模式已开启' : 'TUN 模式已关闭')
    emit('updated')
  } catch (error) {
    tun.value = { ...tun.value, enabled: previous }
    input.checked = previous
    notify(errorMessage(error), true)
  } finally {
    tunSaving.value = false
    tunTarget.value = null
  }
}

onMounted(loadTun)
</script>

<template>
  <div class="runtime-control-grid">
    <div class="card section runtime-control-card system-proxy-card">
      <div class="runtime-control-head">
        <div><h2>系统代理</h2><p :class="enabled ? 'good-text' : 'muted-text'">{{ enabled ? '已开启' : '已关闭' }}</p></div>
        <label class="runtime-control-switch"><span class="switch"><input type="checkbox" :checked="enabled" :disabled="saving || !management || (!online && !enabled)" aria-label="系统代理" @change="toggle"><span /></span></label>
      </div>
      <div class="mode-row" :aria-label="`当前运行模式：${runtimeMode}`">
        <button v-for="item in modes" :key="item.key" class="mode-btn" :class="{ active: enabled && management?.active && runtimeMode === item.key }" :disabled="saving || !online || !enabled || !management?.active" @click="changeMode(item.key)">{{ item.label }}</button>
      </div>
    </div>
    <div class="card section runtime-control-card tun-quick-card" :class="{ 'tun-on': tunEnabled }">
      <div class="runtime-control-head">
        <div><h2>TUN 模式</h2><p :class="tunEnabled ? 'good-text' : tunSupported ? 'muted-text' : 'warn-text'">{{ tunStatus }}</p></div>
        <label class="runtime-control-switch"><span class="switch"><input type="checkbox" :checked="tunEnabled" :disabled="tunLoading || tunSaving || Boolean(tunError) || (!tunSupported && !tunEnabled)" aria-label="TUN 模式" @change="toggleTun"><span /></span></label>
      </div>
      <div class="tun-quick-footer">
        <span :class="{ 'warn-text': !tunSupported && !tunEnabled }">{{ tunDescription }}</span>
        <a href="#settings?section=tun">详细设置</a>
      </div>
    </div>
  </div>
</template>
