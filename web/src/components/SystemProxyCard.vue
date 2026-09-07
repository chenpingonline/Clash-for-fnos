<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { ProxyEnvironmentManagement, ProxyEnvironmentResponse, RuntimeConfig, RuntimeMode } from '@/types/api'

const props = defineProps<{ config: RuntimeConfig; environment: ProxyEnvironmentResponse; online: boolean }>()
const emit = defineEmits<{ updated: [] }>()
const management = ref<ProxyEnvironmentManagement | null>(props.environment.management || null)
const saving = ref(false)
const runtimeMode = ref<RuntimeMode>(props.config.mode || 'rule')
const modes: Array<{ key: RuntimeMode; label: string }> = [{ key: 'rule', label: '规则' }, { key: 'global', label: '全局' }, { key: 'direct', label: '直连' }]

watch(() => props.environment.management, value => { management.value = value || null })
watch(() => props.config.mode, value => { runtimeMode.value = value || 'rule' })

const enabled = computed(() => management.value?.settings?.enabled === true)
const status = computed(() => {
  if (!management.value) return '系统代理状态读取失败，请刷新重试'
  if (saving.value) return '正在更新…'
  if (!enabled.value) return '系统代理已关闭，开启后可选择运行模式'
  if (management.value.active) return '系统代理已开启'
  if (management.value.suspendedReason === 'mixed-port-disabled') return '系统代理已暂停，请先开启 Mixed Port'
  return '系统代理未生效'
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
</script>

<template>
  <div class="card section system-proxy-card">
    <div class="section-head">
      <div><h2>运行模式</h2><p>{{ status }}</p></div>
      <div class="system-proxy-controls">
        <label class="system-proxy-toggle"><span>系统代理</span><span class="switch"><input type="checkbox" :checked="enabled" :disabled="saving || !management || (!online && !enabled)" aria-label="系统代理" @change="toggle"><span /></span></label>
        <div class="mode-row">
          <button v-for="item in modes" :key="item.key" class="mode-btn" :class="{ active: enabled && management?.active && runtimeMode === item.key }" :disabled="saving || !online || !enabled || !management?.active" @click="changeMode(item.key)">{{ item.label }}</button>
        </div>
      </div>
    </div>
    <div class="hint">作用于支持代理环境变量的新登录与 Shell 会话；TUN 独立控制。关闭后保留核心和端口，已有进程仍可使用原代理，重新登录后使用更新的环境。</div>
  </div>
</template>
