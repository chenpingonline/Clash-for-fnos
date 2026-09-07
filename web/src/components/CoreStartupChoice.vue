<script setup lang="ts">
import { ref } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { CoreMode, SystemStatus } from '@/types/api'

defineProps<{ open: boolean; system: SystemStatus | null }>()
const emit = defineEmits<{ selected: [] }>()
const saving = ref<Exclude<CoreMode, 'auto'> | null>(null)

async function select(mode: Exclude<CoreMode, 'auto'>) {
  if (saving.value) return
  saving.value = mode
  try {
    await api('/api/core/mode', jsonRequest('PUT', { mode }))
    notify(mode === 'managed' ? '本次启动将使用 Manager 托管 Core' : '本次启动将使用本机 Core')
    emit('selected')
  } catch (cause) {
    notify(errorMessage(cause), true)
  } finally {
    saving.value = null
  }
}
</script>

<template>
  <BaseModal :open="open" title="选择本次使用的 Core" :closable="false">
    <p class="core-startup-copy">检测到 NAS 上已有 Mihomo。请选择本次打开 Clash for fnOS 时使用的运行方式。</p>
    <div class="core-startup-detected">
      <span>本机 Core</span>
      <strong class="mono">{{ system?.coreAvailability?.external?.binaryPath || '已检测到' }}</strong>
    </div>
    <div class="actions core-startup-actions">
      <button class="ghost" :disabled="saving !== null" @click="select('external')">{{ saving === 'external' ? '连接中…' : '使用本机 Core' }}</button>
      <button :disabled="saving !== null" @click="select('managed')">{{ saving === 'managed' ? '启动中…' : '使用 Manager 托管' }}</button>
    </div>
    <p class="hint core-startup-note">若本机 Core 正在运行，切换到托管方式前需先停止原有服务，以避免端口冲突。</p>
  </BaseModal>
</template>
