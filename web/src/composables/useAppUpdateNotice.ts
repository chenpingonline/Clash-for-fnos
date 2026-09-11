import { computed, ref } from 'vue'
import { api } from '@/services/api'
import type { AppUpdateInfo, ManagerSettings } from '@/types/api'

const enabled = ref(true)
const updateDetected = ref(false)
const available = computed(() => enabled.value && updateDetected.value)

function applyResult(result: AppUpdateInfo) {
  updateDetected.value = result.sourceConfigured === true && result.updateAvailable === true
}

async function check(signal?: AbortSignal) {
  const result = await api<AppUpdateInfo>('/api/app/check-update', { method: 'POST', signal })
  applyResult(result)
  return result
}

async function initialize(signal?: AbortSignal) {
  const settings = await api<ManagerSettings>('/api/settings')
  enabled.value = settings.notifyAppUpdates !== false
  if (!enabled.value) return null
  return check(signal)
}

function setEnabled(value: boolean) {
  enabled.value = value
}

export function useAppUpdateNotice() {
  return { available, enabled, applyResult, check, initialize, setEnabled }
}
