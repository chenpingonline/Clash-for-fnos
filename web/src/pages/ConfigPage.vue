<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import { APP_PREFIX, errorMessage } from '@/services/api'
import { highlightParts } from '@/services/logs'

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
const query = ref('')
const activeMatch = ref(0)
const configScroll = ref<HTMLElement | null>(null)
const highlightedContent = computed(() => {
  let matchIndex = 0
  return highlightParts(config.value?.content || '', query.value).map(part => ({
    ...part,
    matchIndex: part.match ? ++matchIndex : 0,
  }))
})
const matchCount = computed(() => highlightedContent.value.reduce((count, part) => count + Number(part.match), 0))

async function revealActiveMatch() {
  await nextTick()
  configScroll.value?.querySelector<HTMLElement>('mark.current')?.scrollIntoView({ block: 'center', inline: 'nearest' })
}

function moveMatch(offset: number) {
  if (!matchCount.value) return
  activeMatch.value = ((activeMatch.value - 1 + offset + matchCount.value) % matchCount.value) + 1
  void revealActiveMatch()
}

function searchKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter') return
  event.preventDefault()
  moveMatch(event.shiftKey ? -1 : 1)
}

watch([query, matchCount], ([value, count]) => {
  activeMatch.value = value.trim() && count ? 1 : 0
  if (activeMatch.value) void revealActiveMatch()
})

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
  <Teleport to="#page-actions">
    <div class="config-tools">
      <div class="config-search-wrap">
        <input v-model="query" type="search" class="config-search" placeholder="搜索配置" aria-label="搜索配置内容" @keydown="searchKeydown">
        <span v-if="query.trim()" class="config-search-count" role="status">{{ matchCount ? `${activeMatch}/${matchCount}` : '0/0' }}</span>
      </div>
      <button class="ghost config-search-nav" type="button" :disabled="!matchCount" aria-label="上一个匹配项" title="上一个匹配项（Shift + Enter）" @click="moveMatch(-1)">↑</button>
      <button class="ghost config-search-nav" type="button" :disabled="!matchCount" aria-label="下一个匹配项" title="下一个匹配项（Enter）" @click="moveMatch(1)">↓</button>
    </div>
  </Teleport>
  <AsyncState :loading="loading" :error="error">
    <div v-if="config" class="config-workspace">
      <div class="config-meta"><span class="tag">当前生效</span><span class="mono config-path" :title="config.path || config.configPath || ''">{{ config.path || config.configPath || '未检测到启动配置路径' }}</span><span v-if="config.pid" class="muted">PID {{ config.pid }}</span><span class="tag config-readonly-tag">只读</span></div>
      <div ref="configScroll" class="config-scroll persistent-horizontal-scrollbar"><pre class="config-yaml mono"><template v-for="(part, index) in highlightedContent" :key="index"><mark v-if="part.match" :class="{ current: part.matchIndex === activeMatch }">{{ part.text }}</mark><template v-else>{{ part.text }}</template></template></pre></div>
    </div>
  </AsyncState>
</template>
