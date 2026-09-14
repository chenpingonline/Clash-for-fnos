<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import SettingToggle from '@/components/settings/SettingToggle.vue'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { ProxyEnvironmentManagement, ProxyEnvironmentResponse } from '@/types/api'

type ProxyEnvironmentForm = {
  enabled: boolean
  followMixedPort: boolean
  port: number
  targets: { environment: boolean; profile: boolean; bashrc: boolean }
}

const props = defineProps<{ open: boolean; initialManagement?: ProxyEnvironmentManagement | null }>()
const emit = defineEmits<{ close: []; saved: [response: ProxyEnvironmentResponse] }>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const management = ref<ProxyEnvironmentManagement | null>(null)
const bypassItems = ref<string[]>([])
const bypassInput = ref('')
const form = reactive<ProxyEnvironmentForm>({
  enabled: false,
  followMixedPort: true,
  port: 7890,
  targets: { environment: true, profile: true, bashrc: true },
})
let loadRequest = 0

const serviceAddress = computed(() => `127.0.0.1:${form.port || 7890}`)
const available = computed(() => Boolean(management.value))

function applyManagement(value: ProxyEnvironmentManagement | null | undefined) {
  management.value = value || null
  const settings = value?.settings
  Object.assign(form, {
    enabled: settings?.enabled === true,
    followMixedPort: settings?.followMixedPort !== false,
    port: Number(settings?.port || value?.mixedPort || 7890),
    targets: {
      environment: settings?.targets?.environment !== false,
      profile: settings?.targets?.profile !== false,
      bashrc: settings?.targets?.bashrc !== false,
    },
  })
  bypassItems.value = String(settings?.noProxy || 'localhost,127.0.0.1,::1')
    .split(',')
    .map(item => item.trim())
    .filter((item, index, values) => Boolean(item) && values.indexOf(item) === index)
  bypassInput.value = ''
}

if (props.initialManagement) applyManagement(props.initialManagement)

async function load() {
  const request = ++loadRequest
  loading.value = !management.value
  error.value = ''
  try {
    const result = await api<ProxyEnvironmentResponse>('/api/system/proxy-environment')
    if (request !== loadRequest) return
    if (!result.management) throw new Error(result.error || 'Root Helper 不可用，无法读取系统代理设置')
    applyManagement(result.management)
  } catch (cause) {
    if (request === loadRequest) error.value = errorMessage(cause)
  } finally {
    if (request === loadRequest) loading.value = false
  }
}

function addBypass() {
  const additions = bypassInput.value.split(',').map(item => item.trim()).filter(Boolean)
  if (additions.length) bypassItems.value = [...new Set([...bypassItems.value, ...additions])]
  bypassInput.value = ''
}

function bypassKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' && event.key !== ',') return
  event.preventDefault()
  addBypass()
}

function removeBypass(index: number) {
  bypassItems.value = bypassItems.value.filter((_, itemIndex) => itemIndex !== index)
}

function close() {
  if (!saving.value) emit('close')
}

async function save() {
  addBypass()
  if (!Number.isInteger(form.port) || form.port < 1 || form.port > 65535) {
    error.value = '代理端口必须是 1–65535 之间的整数'
    return
  }
  saving.value = true
  error.value = ''
  try {
    const result = await api<ProxyEnvironmentResponse>('/api/system/proxy-environment', jsonRequest('PUT', {
      enabled: form.enabled,
      followMixedPort: form.followMixedPort,
      port: Number(form.port),
      noProxy: bypassItems.value.join(','),
      targets: { ...form.targets },
    }))
    if (!result.management) throw new Error(result.error || '系统代理设置未能确认生效')
    applyManagement(result.management)
    emit('saved', result)
    notify('系统代理设置已保存')
    emit('close')
  } catch (cause) {
    error.value = errorMessage(cause)
    notify(error.value, true)
  } finally {
    saving.value = false
  }
}

watch(() => props.open, open => {
  if (open) {
    if (props.initialManagement) applyManagement(props.initialManagement)
    void load()
  }
  else ++loadRequest
})
</script>

<template>
  <BaseModal :open="open" title="系统代理设置" card-class="system-proxy-modal-card" :closable="!saving" @close="close">
    <template #header>
      <div class="settings-modal-header">
        <h3>系统代理设置</h3>
        <div class="settings-modal-header-actions">
          <button class="ghost" type="button" :disabled="saving" @click="close">取消</button>
          <button type="button" :disabled="loading || !available || saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </template>
    <div class="system-proxy-modal-content">
      <div v-if="loading" class="system-proxy-modal-loading">正在读取系统代理设置…</div>
      <div v-else class="system-proxy-modal-scroll">
        <div v-if="error" class="local-warning">{{ error }}</div>

        <fieldset class="system-proxy-summary">
          <legend>当前系统代理</legend>
          <div><span>开启状态</span><strong :class="management?.active ? 'good-text' : 'muted-text'">{{ management?.active ? '已启用' : '未启用' }}</strong></div>
          <div><span>服务地址</span><strong class="mono">{{ serviceAddress }}</strong></div>
        </fieldset>

        <div class="system-proxy-modal-grid">
          <SettingToggle v-model="form.enabled" title="启用代理环境变量" description="关闭时仅移除本应用管理的配置" :disabled="!available || saving" />
          <SettingToggle v-model="form.followMixedPort" title="自动跟随 Mixed Port" description="端口变化后自动同步系统代理" :disabled="!available || saving" />
        </div>

        <div class="field system-proxy-bypass-field">
          <label>代理绕过设置</label>
          <div class="system-proxy-bypass-editor" :class="{ disabled: !available || saving }">
            <span v-for="(item, index) in bypassItems" :key="item" class="system-proxy-bypass-tag">
              {{ item }}
              <button type="button" :aria-label="`移除 ${item}`" :disabled="!available || saving" @click="removeBypass(index)">×</button>
            </span>
            <input v-model="bypassInput" :disabled="!available || saving" aria-label="添加代理绕过地址" placeholder="输入域名、IP 或 CIDR，按回车添加" @keydown="bypassKeydown" @blur="addBypass">
          </div>
          <small>保存为 NO_PROXY；多个条目会以英文逗号分隔。</small>
        </div>

        <div class="system-proxy-scope-title">应用范围</div>
        <div class="system-proxy-scope-grid">
          <SettingToggle v-model="form.targets.environment" title="系统登录环境" description="/etc/environment" :disabled="!available || saving" />
          <SettingToggle v-model="form.targets.profile" title="登录 Shell" description="/etc/profile" :disabled="!available || saving" />
          <SettingToggle v-model="form.targets.bashrc" title="Bash 交互环境" description="/etc/bash.bashrc" :disabled="!available || saving" />
        </div>

        <p class="system-proxy-modal-note">fnOS 使用 HTTP_PROXY、HTTPS_PROXY、ALL_PROXY 和 NO_PROXY 环境变量；修改主要对新启动的进程与新登录会话生效。</p>
      </div>
    </div>
  </BaseModal>
</template>
