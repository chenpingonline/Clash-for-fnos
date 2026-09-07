<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { api, errorMessage } from '@/services/api'
import { providerUpdatedText } from '@/services/format'
import { notify } from '@/services/toast'
import type { RuleProvider } from '@/types/api'

const loading = ref(true)
const error = ref('')
const providers = ref<Record<string, RuleProvider>>({})
const updating = ref(new Set<string>())
let timer = 0
const entries = computed(() => Object.entries(providers.value).sort(([a], [b]) => a.localeCompare(b)))

async function load() {
  loading.value = true
  try {
    const value = await api<{ providers?: Record<string, RuleProvider> }>('/api/rule-providers')
    providers.value = value.providers || {}
    error.value = ''
  } catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}

async function updateOne(name: string, silent = false) {
  updating.value = new Set(updating.value).add(name)
  try {
    const result = await api<{ method?: string }>(`/api/rule-providers/${encodeURIComponent(name)}/update`, { method: 'PUT' })
    if (!silent) notify(result.method === 'direct-fallback' ? `${name} 常规更新失败，已通过直连兜底更新` : `${name} 更新已触发`)
    return result.method === 'direct-fallback'
  } catch (cause) {
    if (!silent) notify(`${name}: ${errorMessage(cause)}`, true)
    throw cause
  } finally {
    const next = new Set(updating.value); next.delete(name); updating.value = next
  }
}

async function updateAll() {
  let ok = 0, failed = 0, fallback = 0
  for (const [name] of entries.value) {
    try { if (await updateOne(name, true)) fallback += 1; ok += 1 } catch { failed += 1 }
  }
  notify(`Rule Providers 更新完成：成功 ${ok}${fallback ? `（直连兜底 ${fallback}）` : ''}${failed ? `，失败 ${failed}` : ''}`, failed > 0)
  timer = window.setTimeout(load, 800)
}

onMounted(load)
onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <AsyncState :loading="loading">
    <div class="card section">
      <div class="section-head"><div><h2>Rule Providers</h2><p>通过 Mihomo Core API 查看和更新当前配置中的 rule-providers</p></div><button v-if="entries.length" class="ghost" :disabled="updating.size > 0" @click="updateAll">全部更新</button></div>
      <div v-if="error" class="local-warning">读取 Rule Providers 失败：{{ error }}</div>
      <div v-if="entries.length" class="profile-list">
        <div v-for="[name, provider] in entries" :key="name" class="profile"><div><div class="profile-name">{{ name }}</div><div class="profile-meta">{{ provider.behavior || provider.vehicleType || provider.type || 'Rule Provider' }} · {{ Number(provider.ruleCount || 0) }} 条规则</div></div><div><div class="profile-url">{{ providerUpdatedText(provider.updatedAt) }}</div></div><div class="actions"><button class="ghost small" :disabled="updating.has(name)" @click="updateOne(name)">{{ updating.has(name) ? '处理中…' : '更新' }}</button></div></div>
      </div>
      <div v-else class="empty">当前配置没有 Rule Provider，或核心不支持该接口</div>
    </div>
  </AsyncState>
</template>
