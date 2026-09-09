<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { APP_PREFIX, errorMessage } from '@/services/api'

interface EffectiveConfig { configPath?: string; path?: string; pid?: number; content?: string }

const CONFIG_MEMORY_TTL = 60_000
let configCache: { value: EffectiveConfig; etag: string; expiresAt: number } | null = null
let evictionTimer = 0

function rememberConfig(value: EffectiveConfig, etag: string) {
  configCache = { value, etag, expiresAt: Date.now() + CONFIG_MEMORY_TTL }
  window.clearTimeout(evictionTimer)
  evictionTimer = window.setTimeout(() => { configCache = null }, CONFIG_MEMORY_TTL)
}

function currentCache() {
  if (configCache && configCache.expiresAt > Date.now()) return configCache
  configCache = null
  return null
}

const loading = ref(true)
const error = ref('')
const config = ref<EffectiveConfig | null>(null)

async function loadConfig(showLoading = config.value === null) {
  const cached = currentCache()
  if (!config.value && cached) config.value = cached.value
  if (showLoading && !config.value) loading.value = true
  try {
    const response = await fetch(`${APP_PREFIX}/api/config/effective`, {
      headers: cached?.etag ? { 'If-None-Match': cached.etag } : undefined,
    })
    if (response.status === 304 && cached) {
      rememberConfig(cached.value, cached.etag)
      error.value = ''
      return
    }
    const payload = await response.json() as EffectiveConfig & { error?: string; message?: string }
    if (!response.ok) throw new Error(payload.error || payload.message || `HTTP ${response.status}`)
    config.value = payload
    rememberConfig(payload, response.headers.get('etag') || '')
    error.value = ''
  } catch (cause) {
    if (!config.value) error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}

defineExpose({ refreshPage: () => loadConfig(false) })
onMounted(() => {
  const cached = currentCache()
  if (cached) {
    config.value = cached.value
    loading.value = false
    void loadConfig(false)
  } else {
    void loadConfig(true)
  }
})
</script>

<template>
  <Teleport to="#page-actions"><span class="tag config-readonly-tag">只读</span></Teleport>
  <AsyncState :loading="loading" :error="error">
    <div v-if="config" class="config-workspace">
      <div class="config-meta"><span class="tag">当前生效</span><span class="mono config-path" :title="config.path || config.configPath || ''">{{ config.path || config.configPath || '未检测到启动配置路径' }}</span><span v-if="config.pid" class="muted">PID {{ config.pid }}</span></div>
      <div class="config-scroll"><pre class="config-yaml mono">{{ config.content || '' }}</pre></div>
    </div>
  </AsyncState>
</template>
