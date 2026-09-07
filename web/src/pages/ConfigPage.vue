<script setup lang="ts">
import { onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { api, errorMessage } from '@/services/api'

interface EffectiveConfig { configPath?: string; pid?: number; content?: string }
const loading = ref(true)
const error = ref('')
const config = ref<EffectiveConfig | null>(null)
onMounted(async () => {
  try { config.value = await api<EffectiveConfig>('/api/config/effective') }
  catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
})
</script>

<template>
  <Teleport to="#page-actions"><span class="tag config-readonly-tag">只读</span></Teleport>
  <AsyncState :loading="loading" :error="error">
    <div v-if="config" class="config-workspace">
      <div class="config-meta"><span class="tag">当前生效</span><span class="mono config-path" :title="config.configPath || ''">{{ config.configPath || '未检测到启动配置路径' }}</span><span v-if="config.pid" class="muted">PID {{ config.pid }}</span></div>
      <div class="config-scroll"><pre class="config-yaml mono">{{ config.content || '' }}</pre></div>
    </div>
  </AsyncState>
</template>
