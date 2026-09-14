<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseModal from '@/components/BaseModal.vue'
import HelpPopover from '@/components/HelpPopover.vue'
import SettingToggle from '@/components/settings/SettingToggle.vue'
import { useOperationProgress } from '@/composables/useOperationProgress'
import { api, errorMessage, jsonRequest } from '@/services/api'
import { notify } from '@/services/toast'
import type { NetworkSettingsResponse, TunSetting } from '@/types/api'

type TunForm = Required<TunSetting>

const defaultTun = (): TunForm => ({
  enabled: false,
  stack: 'mixed',
  mtu: 1500,
  routeExcludeAddress: [],
  autoRoute: true,
  autoRedirect: true,
  autoDetectInterface: true,
  dnsHijack: false,
  strictRoute: false,
})

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; saved: [response: NetworkSettingsResponse] }>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const offline = ref(false)
const dnsEnabled = ref(false)
const capability = ref<NonNullable<NetworkSettingsResponse['tunCapability']>>({ supported: false })
const routeExcludeText = ref('')
const form = reactive<TunForm>(defaultTun())
const operation = useOperationProgress()
let loadRequest = 0

const supported = computed(() => capability.value.supported === true)
const controlsDisabled = computed(() => loading.value || saving.value || !supported.value || !form.enabled)
const capabilityText = computed(() => capability.value.message || (supported.value ? '当前 fnOS 环境支持 TUN。' : '当前环境暂不支持 TUN。'))

function applyResult(result: NetworkSettingsResponse) {
  Object.assign(form, defaultTun(), result.settings?.tun || {})
  capability.value = result.tunCapability || { supported: false }
  offline.value = result.offline === true
  dnsEnabled.value = result.settings?.dns?.enable === true
  routeExcludeText.value = form.routeExcludeAddress.join('\n')
}

async function load() {
  const request = ++loadRequest
  loading.value = true
  error.value = ''
  try {
    const result = await api<NetworkSettingsResponse>('/api/network/settings')
    if (request === loadRequest) applyResult(result)
  } catch (cause) {
    if (request === loadRequest) error.value = errorMessage(cause)
  } finally {
    if (request === loadRequest) loading.value = false
  }
}

function close() {
  if (!saving.value) emit('close')
}

function parseRouteExcludes() {
  return [...new Set(routeExcludeText.value
    .split(/[\n,]+/)
    .map(item => item.trim())
    .filter(Boolean))]
}

async function save() {
  if (!Number.isInteger(form.mtu) || form.mtu < 1280 || form.mtu > 65535) {
    error.value = 'MTU 必须是 1280–65535 之间的整数'
    return
  }
  if (!supported.value) {
    error.value = capabilityText.value
    return
  }
  saving.value = true
  error.value = ''
  form.routeExcludeAddress = parseRouteExcludes()
  if (!form.autoRoute) form.autoRedirect = false
  try {
    const result = await operation.request<NetworkSettingsResponse>(
      '/api/network/settings',
      jsonRequest('PUT', { tun: { ...form, routeExcludeAddress: [...form.routeExcludeAddress] } }),
      '/api/network/settings/status',
      '正在校验并保存 TUN 设置…',
    )
    applyResult(result)
    emit('saved', result)
    notify(result.activation === 'saved-only' ? 'TUN 设置已保存，Core 启动后生效' : 'TUN 设置已保存并生效')
    emit('close')
  } catch (cause) {
    error.value = errorMessage(cause)
    notify(error.value, true)
  } finally {
    saving.value = false
  }
}

watch(() => props.open, open => {
  if (open) void load()
  else ++loadRequest
})
</script>

<template>
  <BaseModal :open="open" title="虚拟网卡(TUN)设置" card-class="tun-settings-modal-card" :closable="!saving" @close="close">
    <template #header>
      <div class="settings-modal-header">
        <div class="settings-modal-heading">
          <h3>虚拟网卡(TUN)设置</h3>
          <span v-if="saving" class="tun-settings-save-progress" role="status" aria-live="polite">{{ operation.message }}</span>
        </div>
        <div class="settings-modal-header-actions">
          <button class="ghost" type="button" :disabled="saving" @click="close">取消</button>
          <button type="button" :disabled="loading || saving || !supported" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </div>
    </template>
    <div class="tun-settings-modal-content">
      <div v-if="loading" class="tun-settings-modal-loading">正在读取 TUN 设置…</div>
      <div v-else class="tun-settings-modal-scroll">
        <div v-if="error" class="local-warning">{{ error }}</div>

        <div class="tun-capability" :class="supported ? 'ok' : 'warn'">
          <strong>{{ supported ? (form.enabled ? '已开启' : '已关闭') : '不可用' }}</strong>
          <span>{{ capabilityText }}</span>
        </div>
        <p v-if="!form.enabled && supported" class="tun-settings-disabled-hint">请先在首页开启虚拟网卡(TUN)模式，再调整以下参数。</p>
        <p v-if="offline" class="tun-settings-disabled-hint">Core 已停止；保存后将在下次启动时生效。</p>

        <div class="tun-settings-main-grid">
          <div class="field tun-settings-field">
            <div class="field-label-row">
              <label>TUN Stack</label>
              <HelpPopover label="TUN Stack"><strong>协议栈决定 Mihomo 如何处理 TUN 流量</strong><span><b>mixed（推荐）</b>：TCP 使用 System，UDP 使用 gVisor，兼顾稳定性与兼容性。</span><span><b>system</b>：使用 Linux 系统协议栈，资源占用更低。</span><span><b>gVisor</b>：在用户态处理网络协议，可排查特殊网络兼容问题。</span></HelpPopover>
            </div>
            <select v-model="form.stack" :disabled="controlsDisabled"><option value="mixed">mixed（推荐）</option><option value="system">system</option><option value="gvisor">gVisor</option></select>
          </div>
          <div class="field tun-settings-field">
            <div class="field-label-row">
              <label>MTU</label>
              <HelpPopover label="MTU"><strong>单个网络数据包的最大传输尺寸</strong><span>默认值为 <b>1500</b>。VPN、PPPoE 或多层隧道环境异常时，可尝试 <b>1400</b>。</span></HelpPopover>
            </div>
            <input v-model.number="form.mtu" type="number" min="1280" max="65535" :disabled="controlsDisabled" aria-label="TUN MTU">
          </div>
        </div>

        <div class="tun-settings-option-grid">
          <SettingToggle v-model="form.autoRoute" title="自动路由" description="自动把系统流量路由到 TUN" :disabled="controlsDisabled" />
          <SettingToggle v-model="form.autoRedirect" title="Auto Redirect" description="Linux 自动配置 nftables/iptables TCP 重定向" :disabled="controlsDisabled || !form.autoRoute" />
          <SettingToggle v-model="form.autoDetectInterface" title="自动检测出口网卡" description="自动选择实际的外网出口接口" :disabled="controlsDisabled" />
          <SettingToggle v-model="form.dnsHijack" title="DNS 劫持" description="劫持 UDP/TCP 53 到 Mihomo DNS 模块" :disabled="controlsDisabled" />
          <SettingToggle v-model="form.strictRoute" title="严格路由" description="减少流量/DNS 泄漏；复杂网络可能影响其他虚拟网卡" :disabled="controlsDisabled" />
        </div>

        <div class="field tun-settings-route-exclude">
          <div class="field-label-row">
            <label>排除自定义网段</label>
            <HelpPopover label="排除自定义网段"><strong>让指定目标网段绕过 TUN 自动路由</strong><span>仅在开启“自动路由”时生效。支持 IPv4/IPv6 CIDR，每行填写一个。</span></HelpPopover>
          </div>
          <textarea v-model="routeExcludeText" placeholder="192.168.0.0/16&#10;10.0.0.0/8&#10;fc00::/7" :disabled="controlsDisabled || !form.autoRoute" />
          <span class="field-note">每行一个 IPv4/IPv6 CIDR；留空表示不额外排除。</span>
        </div>

        <div v-if="form.enabled && form.dnsHijack && !dnsEnabled" class="tun-capability warn"><strong>DNS</strong><span>开启 DNS 劫持前建议先启用 Mihomo DNS。</span></div>
        <div class="tun-note"><strong>注意</strong><span>TUN 会修改 fnOS 的系统路由与 DNS 流向。默认关闭；配置不可用时可能影响 NAS 访问互联网。</span></div>
      </div>
    </div>
  </BaseModal>
</template>
