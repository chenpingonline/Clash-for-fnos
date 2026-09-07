import { computed, readonly, ref } from 'vue'
import { api, errorMessage } from '@/services/api'
import type { CoreHealth, SystemStatus } from '@/types/api'

const health = ref<CoreHealth | null>(null)
const loading = ref(false)

export async function refreshCoreHealth(): Promise<CoreHealth> {
  if (loading.value && health.value) return health.value
  loading.value = true
  try {
    try {
      const status = await api<CoreHealth>('/api/status')
      health.value = { ...status, online: true }
    } catch (error) {
      const system = await api<SystemStatus>('/api/system/status')
      health.value = {
        online: false,
        system,
        bootstrap: system.bootstrap,
        error: system.bootstrap?.error || system.bootstrap?.message || errorMessage(error),
      }
    }
    return health.value!
  } finally {
    loading.value = false
  }
}

export function useCoreHealth() {
  const footer = computed(() => {
    const value = health.value
    if (value?.online) return { className: 'online', state: '已连接', version: value.version?.version || 'Mihomo' }
    const bootstrap = value?.bootstrap
    if (['checking', 'downloading', 'installing', 'starting'].includes(bootstrap?.state || '')) {
      return { className: 'working', state: '正在准备 Core', version: `${bootstrap?.progress || 0}% · ${bootstrap?.message || '处理中'}` }
    }
    if (bootstrap?.state === 'error') return { className: 'offline', state: 'Core 安装失败', version: '点击仪表盘重试' }
    if (bootstrap?.state === 'external-stopped') return { className: 'offline', state: '外部 Core 未运行', version: '已检测到本机安装' }
    return { className: 'offline', state: '未连接', version: '检查设置' }
  })
  return { health: readonly(health), loading: readonly(loading), footer, refresh: refreshCoreHealth }
}
