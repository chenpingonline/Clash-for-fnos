<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import BaseModal from '@/components/BaseModal.vue'
import RuleVirtualList from '@/components/RuleVirtualList.vue'
import { api, errorMessage } from '@/services/api'
import { providerUpdatedText } from '@/services/format'
import { containsRule, normalizeRules } from '@/services/rules'
import { notify } from '@/services/toast'
import type { RuleProvider, RulesResponse } from '@/types/api'

const rulesLoading = ref(true)
const rulesError = ref('')
const rawRules = ref<RulesResponse['rules']>([])
const query = ref('')
const providerOpen = ref(false)
const providerLoading = ref(true)
const providerError = ref('')
const providers = ref<Record<string, RuleProvider>>({})
const updating = ref(new Set<string>())
let timer = 0

const rules = computed(() => normalizeRules(rawRules.value || []))
const filteredRules = computed(() => rules.value.filter(rule => containsRule(rule, query.value)))
const providerEntries = computed(() => Object.entries(providers.value).sort(([a], [b]) => a.localeCompare(b)))
const providerRuleCount = computed(() => providerEntries.value.reduce((sum, [, provider]) => sum + Number(provider.ruleCount || 0), 0))

async function loadRules(showLoading = true) {
  if (showLoading) rulesLoading.value = true
  try {
    const value = await api<RulesResponse>('/api/rules')
    rawRules.value = value.rules || []
    rulesError.value = ''
  } catch (cause) { rulesError.value = errorMessage(cause) }
  finally { rulesLoading.value = false }
}

async function loadProviders(showLoading = true) {
  if (showLoading) providerLoading.value = true
  try {
    const value = await api<{ providers?: Record<string, RuleProvider> }>('/api/rule-providers')
    providers.value = value.providers || {}
    providerError.value = ''
  } catch (cause) { providerError.value = errorMessage(cause) }
  finally { providerLoading.value = false }
}

async function load() {
  await Promise.all([loadRules(), loadProviders()])
}

function scheduleReload() {
  window.clearTimeout(timer)
  timer = window.setTimeout(() => Promise.all([loadRules(false), loadProviders(false)]), 800)
}

async function updateOne(name: string, silent = false) {
  updating.value = new Set(updating.value).add(name)
  try {
    const result = await api<{ method?: string }>(`/api/rule-providers/${encodeURIComponent(name)}/update`, { method: 'PUT' })
    if (!silent) notify(result.method === 'direct-fallback' ? `${name} 常规更新失败，已通过直连兜底更新` : `${name} 更新已触发`)
    if (!silent) scheduleReload()
    return result.method === 'direct-fallback'
  } catch (cause) {
    if (!silent) notify(`${name}: ${errorMessage(cause)}`, true)
    throw cause
  } finally {
    const next = new Set(updating.value)
    next.delete(name)
    updating.value = next
  }
}

async function updateAll() {
  let ok = 0, failed = 0, fallback = 0
  for (const [name] of providerEntries.value) {
    try { if (await updateOne(name, true)) fallback += 1; ok += 1 } catch { failed += 1 }
  }
  notify(`规则集更新完成：成功 ${ok}${fallback ? `（直连兜底 ${fallback}）` : ''}${failed ? `，失败 ${failed}` : ''}`, failed > 0)
  scheduleReload()
}

defineExpose({ refreshPage: load })
onMounted(load)
onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <Teleport defer to="#page-actions">
    <button v-if="providerEntries.length || providerError" class="ghost rule-provider-trigger" @click="providerOpen = true">规则集 <span v-if="providerEntries.length">{{ providerEntries.length }}</span></button>
  </Teleport>

  <section class="card rules-panel">
    <div class="rules-toolbar">
      <div class="rules-heading">
        <h2>生效规则</h2>
        <p v-if="!rulesLoading && !rulesError">{{ rules.length }} 条规则<span v-if="providerEntries.length"> · {{ providerEntries.length }} 个规则集，共 {{ providerRuleCount }} 条规则集内容</span></p>
        <p v-else>来自 Mihomo 当前运行配置</p>
      </div>
      <label class="rule-search">
        <span aria-hidden="true">⌕</span>
        <input v-model="query" type="search" placeholder="搜索规则、类型或策略" aria-label="搜索规则、类型或策略">
        <button v-if="query" type="button" class="search-clear" aria-label="清空搜索" @click="query = ''">×</button>
      </label>
    </div>

    <AsyncState :loading="rulesLoading" :error="rulesError">
      <RuleVirtualList v-if="filteredRules.length" :items="filteredRules" />
      <div v-else class="empty rules-empty">{{ query ? `没有匹配“${query}”的规则` : '当前运行配置没有生效规则' }}</div>
    </AsyncState>
  </section>

  <BaseModal :open="providerOpen" :title="`规则集 · ${providerEntries.length}`" @close="providerOpen = false">
    <div class="provider-modal-head">
      <p>管理当前配置中的 Rule Providers；更新后会同步刷新生效规则。</p>
      <button v-if="providerEntries.length" class="ghost small" :disabled="updating.size > 0" @click="updateAll">{{ updating.size ? '更新中…' : '全部更新' }}</button>
    </div>
    <AsyncState :loading="providerLoading" :error="providerError">
      <div v-if="providerEntries.length" class="rule-provider-list">
        <div v-for="[name, provider] in providerEntries" :key="name" class="rule-provider-row">
          <div class="rule-provider-main"><strong :title="name">{{ name }}</strong><span>{{ provider.behavior || provider.vehicleType || provider.type || 'Rule Provider' }} · {{ Number(provider.ruleCount || 0) }} 条</span></div>
          <span class="rule-provider-updated">{{ providerUpdatedText(provider.updatedAt) }}</span>
          <button class="ghost small" :disabled="updating.has(name)" @click="updateOne(name)">{{ updating.has(name) ? '处理中…' : '更新' }}</button>
        </div>
      </div>
      <div v-else class="empty compact">当前配置没有 Rule Provider</div>
    </AsyncState>
    <div class="actions provider-modal-actions"><button class="ghost" @click="providerOpen = false">关闭</button></div>
  </BaseModal>
</template>
