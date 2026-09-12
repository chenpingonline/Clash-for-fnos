import { computed, readonly, ref } from 'vue'
import { api, errorMessage } from '@/services/api'
import type { CoreBootstrap, CoreHealth, SystemStatus } from '@/types/api'

const health = ref<CoreHealth | null>(null)
const loading = ref(false)
let pending: Promise<CoreHealth> | null = null

export function refreshCoreHealth(): Promise<CoreHealth> {
  if (pending) return pending.then(() => refreshCoreHealth())
  pending = readCoreHealth().finally(() => { pending = null })
  return pending
}

async function readCoreHealth(): Promise<CoreHealth> {
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
        error: system.bootstrap?.error || errorMessage(error),
      }
    }
    return health.value!
  } finally {
    loading.value = false
  }
}

export function updateCoreBootstrap(bootstrap: CoreBootstrap) {
  if (!health.value) return
  health.value = {
    ...health.value,
    bootstrap,
    system: health.value.system ? { ...health.value.system, bootstrap } : health.value.system,
  }
}

export function useCoreHealth() {
  const footer = computed(() => {
    const value = health.value
    if (value?.online) return { className: 'online', state: '已连接', version: value.version?.version || 'Mihomo' }
    const bootstrap = value?.bootstrap
    if (['checking', 'downloading', 'canceling', 'installing', 'starting'].includes(bootstrap?.state || '')) {
      return { className: 'working', state: '正在准备 Core', version: bootstrap?.message || '处理中' }
    }
    if (bootstrap?.state === 'error') return { className: 'offline', state: 'Core 启用失败', version: '点击仪表盘重试' }
    if (bootstrap?.state === 'download-required') return { className: 'offline', state: '需要下载 Core', version: '前往仪表盘下载' }
    if (bootstrap?.state === 'stopped') return { className: 'offline', state: 'Core 已停止', version: '可在设置中启动' }
    if (bootstrap?.state === 'external-stopped') return { className: 'offline', state: '外部 Core 未运行', version: '已检测到本机安装' }
    return { className: 'offline', state: '未连接', version: '检查设置' }
  })
  return { health: readonly(health), loading: readonly(loading), footer, refresh: refreshCoreHealth }
}
