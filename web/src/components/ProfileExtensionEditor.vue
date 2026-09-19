<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { streamProfileJob } from '@/services/profile-jobs'
import { notify } from '@/services/toast'
import type { ProfileExtensionKind, ProfileItem, ProfileJob } from '@/types/api'

const props = withDefaults(defineProps<{ open: boolean; profile: ProfileItem | null; kind: ProfileExtensionKind | null; global?: boolean }>(), { global: false })
const emit = defineEmits<{ close: []; saved: [] }>()
const content = ref(''), loading = ref(false), saving = ref(false), customized = ref(false)
const applyMessage = ref('')

const metadata: Record<ProfileExtensionKind, { title: string }> = {
  rules: { title: '编辑规则' },
  proxies: { title: '编辑节点' },
  groups: { title: '编辑代理组' },
  override: { title: '扩展覆写配置' },
  script: { title: '扩展脚本' },
}
const meta = computed(() => props.kind ? metadata[props.kind] : metadata.rules)
const endpoint = computed(() => props.kind
  ? props.global
    ? `/api/profiles/global/extensions/${props.kind}`
    : `/api/profiles/${props.profile?.id}/extensions/${props.kind}`
  : '')
const title = computed(() => props.global ? `全局${meta.value.title}` : `${meta.value.title} · ${props.profile?.name || ''}`)

async function load() {
  if (!props.open || !props.kind || (!props.global && !props.profile)) return
  loading.value = true
  try {
    const result = await api<{ content?: string; customized?: boolean }>(endpoint.value)
    content.value = result.content || ''
    customized.value = Boolean(result.customized)
  } catch (cause) {
    notify(errorMessage(cause), true)
    emit('close')
  } finally { loading.value = false }
}

async function save() {
  if (!props.kind || (!props.global && !props.profile)) return
  saving.value = true
  applyMessage.value = ''
  try {
    let result = await api<ProfileJob & { applied?: boolean }>(endpoint.value, jsonRequest('PUT', { content: content.value, apply: true }))
    if (result.jobId) {
      applyMessage.value = result.message || '准备应用当前配置…'
      result = await streamProfileJob(result.jobId, job => { applyMessage.value = job.message || '正在应用当前配置…' }, new AbortController().signal) as ProfileJob & { applied?: boolean }
      if (result.state === 'failed') throw new Error(`${title.value}已保存，但应用当前配置失败：${result.error ? String(result.error) : '未知错误'}`)
    }
    customized.value = true
    notify(result.jobId ? `${title.value}已保存并应用，配置已立即生效` : `${title.value}已保存；当前没有正在使用的配置`)
    emit('saved')
    emit('close')
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { saving.value = false; applyMessage.value = '' }
}

async function reset() {
  if (!props.kind || (!props.global && !props.profile)) return
  saving.value = true
  try {
    await api(endpoint.value, { method: 'DELETE' })
    notify(`${title.value}已恢复默认`)
    emit('saved')
    emit('close')
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { saving.value = false }
}

watch(() => [props.open, props.profile?.id, props.kind, props.global] as const, load, { immediate: true })
</script>

<template>
  <BaseModal :open="open" :title="title" card-class="profile-extension-modal" @close="emit('close')">
    <div v-if="loading" class="profile-extension-loading">正在读取…</div>
    <textarea v-else v-model="content" class="editor profile-extension-editor" spellcheck="false" :aria-label="meta.title" />
    <div class="actions profile-extension-actions">
      <button class="small" :disabled="loading || saving" @click="save">{{ saving ? (applyMessage || '保存并应用中…') : '保存并应用' }}</button>
      <button v-if="customized" class="danger small" :disabled="saving" @click="reset">恢复默认</button>
      <button class="ghost small" :disabled="saving" @click="emit('close')">取消</button>
    </div>
  </BaseModal>
</template>
