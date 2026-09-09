<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import AsyncState from '@/components/AsyncState.vue'
import HelpPopover from '@/components/HelpPopover.vue'
import PortRow from '@/components/settings/PortRow.vue'
import SettingToggle from '@/components/settings/SettingToggle.vue'
import { useAutosave } from '@/composables/useAutosave'
import { api, APP_PREFIX, errorMessage, jsonRequest } from '@/services/api'
import { formatBytes, formatTime } from '@/services/format'
import { notify } from '@/services/toast'
import type { AppIconsResponse, AppUpdateInfo, DnsMapping, DnsSetting, GeoAsset, GeoStatus, HostMapping, ManagerSettings, NetworkSetting, NetworkSettingsResponse, PortSetting, ProxyEnvironmentResponse, SystemStatus, TunSetting } from '@/types/api'

type Section = 'network' | 'dns' | 'tun' | 'advanced' | 'behavior' | 'update'
type NetworkForm = {
  controller: PortSetting; mixed: PortSetting; socks: PortSetting; http: PortSetting; redir: PortSetting; tproxy: PortSetting
  allowLan: boolean; core: { ipv6: boolean; unifiedDelay: boolean }; tun: Required<TunSetting>; dnsOverrideEnabled: boolean; dns: Required<DnsSetting>
}
type ProxyEnvForm = { enabled: boolean; followMixedPort: boolean; port: number; noProxy: string; targets: { environment: boolean; profile: boolean; bashrc: boolean } }
type TunOperationStatus = { active?: boolean; enabled?: boolean; stage?: string; message?: string }

const defaultDns: Required<DnsSetting> = {
  enable: true, listen: '127.0.0.1:1053', enhancedMode: 'fake-ip', fakeIpRange: '198.18.0.1/16', fakeIpRange6: 'fdfe:dcba:9876::1/64', fakeIpFilterMode: 'blacklist', ipv6: true, preferH3: false, respectRules: false, useHosts: false, useSystemHosts: false, directNameserverFollowPolicy: false,
  defaultNameserver: ['system', '223.6.6.6', '8.8.8.8', '2400:3200::1', '2001:4860:4860::8888'], nameserver: ['8.8.8.8', 'https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query'], fallback: [], proxyServerNameserver: ['https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query', 'tls://223.5.5.5'], directNameserver: [], fakeIpFilter: ['*.lan', '*.local', '*.arpa', 'time.*.com', 'ntp.*.com', '+.market.xiaomi.com', 'localhost.ptlogin2.qq.com', '*.msftncsi.com', 'www.msftconnecttest.com'], nameserverPolicy: [], fallbackGeoip: true, fallbackGeoipCode: 'CN', fallbackIpCidr: ['240.0.0.0/4', '0.0.0.0/32'], fallbackDomain: ['+.google.com', '+.facebook.com', '+.youtube.com'], hosts: [],
}
const defaultNetwork = (): NetworkForm => ({
  controller: { enabled: true, port: 9090 }, mixed: { enabled: true, port: 7890 }, socks: { enabled: false, port: 7898 }, http: { enabled: false, port: 7899 }, redir: { enabled: false, port: 7895 }, tproxy: { enabled: false, port: 7896 }, allowLan: false,
  core: { ipv6: true, unifiedDelay: false }, tun: { enabled: false, stack: 'mixed', mtu: 1500, routeExcludeAddress: [], autoRoute: true, autoRedirect: true, autoDetectInterface: true, dnsHijack: true, strictRoute: false }, dnsOverrideEnabled: false, dns: structuredClone(defaultDns),
})

const requestedSection = new URLSearchParams(location.hash.split('?')[1] || '').get('section')
const loading = ref(true), error = ref(''), open = ref<Section | null>(requestedSection === 'tun' ? 'tun' : null), busy = ref(''), tunSwitching = ref(false)
const tunProgress = ref('')
const coreDetailsOpen = ref(false)
const system = ref<SystemStatus>({}), manager = reactive<ManagerSettings>({}), network = reactive<NetworkForm>(defaultNetwork())
const tunRouteExcludeText = ref('')
const environment = ref<ProxyEnvironmentResponse>({}), proxyForm = reactive<ProxyEnvForm>({ enabled: true, followMixedPort: true, port: 7890, noProxy: 'localhost,127.0.0.1,::1', targets: { environment: true, profile: true, bashrc: true } })
const appUpdate = ref<AppUpdateInfo>({}), icons = ref<AppIconsResponse>({}), selectedIcon = ref('cat-orbit'), tunSupported = ref(true), tunSupportText = ref('当前 Mihomo 具备 TUN 所需权限，可直接启用')
const geo = ref<GeoStatus>({}), geoForm = reactive({ autoUpdate: false, updateInterval: 24 }), geoBusy = ref(''), geoState = ref<'idle' | 'saving' | 'updating' | 'success' | 'error'>('idle'), geoMessage = ref('')
const dnsText = reactive({ defaultNameserver: '', nameserver: '', fallback: '', proxyServerNameserver: '', directNameserver: '', fakeIpFilter: '', nameserverPolicy: '', fallbackIpCidr: '', fallbackDomain: '', hosts: '' })
const netSave = useAutosave('/api/network/settings'), dnsSave = useAutosave('/api/network/settings'), behaviorSave = useAutosave('/api/settings')
const envSave = useAutosave<ProxyEnvironmentResponse>('/api/system/proxy-environment', { onSaved: result => { environment.value = result } })
const netState = netSave.state, netMessage = netSave.message, dnsState = dnsSave.state, dnsMessage = dnsSave.message, behaviorState = behaviorSave.state, behaviorMessage = behaviorSave.message, envState = envSave.state, envMessage = envSave.message
let tunProgressTimer: ReturnType<typeof setTimeout> | null = null
let geoMessageTimer: ReturnType<typeof setTimeout> | null = null
const dnsServerFields: Array<{ key: keyof Pick<typeof dnsText, 'defaultNameserver' | 'nameserver' | 'fallback' | 'proxyServerNameserver' | 'directNameserver'>; label: string }> = [{ key: 'defaultNameserver', label: '默认域名服务器' }, { key: 'nameserver', label: '域名服务器' }, { key: 'fallback', label: '回退服务器' }, { key: 'proxyServerNameserver', label: '代理节点 DNS' }, { key: 'directNameserver', label: '直连域名服务器' }]

const categories: Array<{ key: Section; icon: string; title: string; description: string }> = [
  { key: 'network', icon: '◎', title: '网络与端口', description: '代理端口、IPv6、统一延迟与局域网选项' },
  { key: 'dns', icon: 'DNS', title: 'DNS 与解析', description: 'Mihomo DNS、解析服务器、Fake IP、回退策略与 Hosts' },
  { key: 'tun', icon: '◇', title: '虚拟网卡(TUN)设置', description: 'TUN 详细参数与系统流量接管' },
  { key: 'advanced', icon: '⌘', title: '环境变量设置', description: '管理系统登录与 Shell 的代理环境变量' },
  { key: 'behavior', icon: '⚙', title: '其他设置', description: '软件图标、Controller 与启动行为' },
  { key: 'update', icon: '↻', title: '更新设置', description: '应用、Mihomo Core 与 GEO 数据更新' },
]
const dnsStatus = computed(() => dnsState.value === 'idle' ? (network.dnsOverrideEnabled ? '已启用 DNS 覆写' : 'DNS 覆写已关闭') : dnsMessage.value)
const proxyAvailable = computed(() => system.value.available !== false && system.value.privileged !== false && Boolean(environment.value.management))

function list(value: unknown) { return Array.isArray(value) ? value.map(String).join('\n') : '' }
function lines(value: string) { return value.split(/[\n,]+/).map(item => item.trim()).filter(Boolean) }
function mappings(value: string, kind: 'dns' | 'hosts'): DnsMapping[] | HostMapping[] {
  return value.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map(line => {
    const at = line.indexOf('=')
    if (at <= 0) throw new Error(`${kind === 'hosts' ? 'Hosts' : '域名服务器策略'}每行必须使用“键 = 值”格式`)
    const key = line.slice(0, at).trim(), values = line.slice(at + 1).split(';').map(item => item.trim()).filter(Boolean)
    if (!key || !values.length) throw new Error(`${kind === 'hosts' ? 'Hosts' : '域名服务器策略'}存在空值`)
    return kind === 'hosts' ? { host: key, values } : { matcher: key, servers: values }
  }) as DnsMapping[] | HostMapping[]
}
function syncDnsText() {
  dnsText.defaultNameserver = list(network.dns.defaultNameserver); dnsText.nameserver = list(network.dns.nameserver); dnsText.fallback = list(network.dns.fallback); dnsText.proxyServerNameserver = list(network.dns.proxyServerNameserver); dnsText.directNameserver = list(network.dns.directNameserver); dnsText.fakeIpFilter = list(network.dns.fakeIpFilter); dnsText.nameserverPolicy = network.dns.nameserverPolicy.map(item => `${item.matcher} = ${item.servers.join('; ')}`).join('\n'); dnsText.fallbackIpCidr = list(network.dns.fallbackIpCidr); dnsText.fallbackDomain = list(network.dns.fallbackDomain); dnsText.hosts = network.dns.hosts.map(item => `${item.host} = ${item.values.join('; ')}`).join('\n')
}
function networkPayload() {
  const mixed = Boolean(network.mixed.enabled)
  return { controller: { port: Number(network.controller.port) }, mixed: { ...network.mixed, enabled: mixed }, socks: { ...network.socks, enabled: !mixed && Boolean(network.socks.enabled) }, http: { ...network.http, enabled: !mixed && Boolean(network.http.enabled) }, redir: { ...network.redir, enabled: !mixed && Boolean(network.redir.enabled) }, tproxy: { ...network.tproxy, enabled: !mixed && Boolean(network.tproxy.enabled) }, allowLan: network.allowLan, core: network.core }
}
function numericDelay(value: number | Event | undefined, fallback: number) { return typeof value === 'number' ? value : fallback }
function platformLabel(value?: string) {
  const platform = String(value || '').trim().toLowerCase()
  if (platform === 'x86' || platform === 'amd64' || platform === 'x86_64') return 'x86_64'
  if (platform === 'arm' || platform === 'aarch64' || platform === 'arm64') return 'arm64'
  return value || '--'
}
function saveNetwork(delay: number | Event = 250) { netSave.queue(networkPayload(), numericDelay(delay, 250)) }
function saveTunSettings(delay: number | Event = 250) { if (!network.tun.autoRoute) network.tun.autoRedirect = false; network.tun.routeExcludeAddress = lines(tunRouteExcludeText.value); netSave.queue({ tun: network.tun }, numericDelay(delay, 250)) }
function stopTunProgressPolling() {
  if (tunProgressTimer) clearTimeout(tunProgressTimer)
  tunProgressTimer = null
}
async function pollTunProgress() {
  if (!tunSwitching.value) return
  try {
    const status = await api<TunOperationStatus>('/api/network/tun/status')
    if (tunSwitching.value && status.active && status.message) tunProgress.value = status.message
  } catch {
    // 切换请求是最终依据，临时状态读取失败不打断操作。
  } finally {
    if (tunSwitching.value) tunProgressTimer = setTimeout(pollTunProgress, 120)
  }
}
async function toggleTunSetting() {
  const next = network.tun.enabled
  const previous = !next
  tunSwitching.value = true
  tunProgress.value = next ? '正在准备开启 TUN…' : '正在准备关闭 TUN…'
  void pollTunProgress()
  try {
    const result = await api<{ enabled?: boolean }>('/api/network/tun', jsonRequest('PUT', { enabled: next }))
    if (result.enabled !== next) throw new Error('TUN 状态未按预期生效')
    notify(next ? '虚拟网卡(TUN)模式已开启' : '虚拟网卡(TUN)模式已关闭')
  } catch (cause) {
    network.tun.enabled = previous
    notify(errorMessage(cause), true)
  } finally {
    tunSwitching.value = false
    stopTunProgressPolling()
    tunProgress.value = ''
  }
}
function dnsPayload() {
  network.dns.defaultNameserver = lines(dnsText.defaultNameserver); network.dns.nameserver = lines(dnsText.nameserver); network.dns.fallback = lines(dnsText.fallback); network.dns.proxyServerNameserver = lines(dnsText.proxyServerNameserver); network.dns.directNameserver = lines(dnsText.directNameserver); network.dns.fakeIpFilter = lines(dnsText.fakeIpFilter); network.dns.nameserverPolicy = mappings(dnsText.nameserverPolicy, 'dns') as DnsMapping[]; network.dns.fallbackIpCidr = lines(dnsText.fallbackIpCidr); network.dns.fallbackDomain = lines(dnsText.fallbackDomain); network.dns.hosts = mappings(dnsText.hosts, 'hosts') as HostMapping[]; network.dns.fallbackGeoipCode = network.dns.fallbackGeoipCode.trim().toUpperCase()
  return { dnsOverrideEnabled: network.dnsOverrideEnabled, dns: network.dns }
}
function saveDns(delay = 1000) { try { dnsSave.queue(dnsPayload(), delay) } catch (cause) { notify(errorMessage(cause), true) } }
function resetDns() { Object.assign(network.dns, structuredClone(defaultDns)); syncDnsText(); saveDns(0); notify('已恢复默认值，并自动保存') }
function saveBehavior(delay: number | Event = 250) { behaviorSave.queue({ controllerAutoDetect: true, persistSelections: Boolean(manager.persistSelections), applyManagedConfigOnStart: Boolean(manager.applyManagedConfigOnStart), healthcheckUrl: manager.healthcheckUrl || '', healthcheckTimeout: Number(manager.healthcheckTimeout || 0) }, numericDelay(delay, 250)) }
function saveEnvironment(delay: number | Event = 250) { envSave.queue({ ...proxyForm, port: Number(proxyForm.followMixedPort ? network.mixed.port : proxyForm.port) }, numericDelay(delay, 250)) }

function applyNetwork(value: NetworkSetting, fallbackPort: number) {
  const defaults = defaultNetwork()
  Object.assign(network.controller, defaults.controller, value.controller || {})
  for (const key of ['mixed', 'socks', 'http', 'redir', 'tproxy'] as const) Object.assign(network[key], defaults[key], value[key] || {})
  network.mixed.port ||= fallbackPort; network.allowLan = Boolean(value.allowLan); Object.assign(network.core, defaults.core, value.core || {}); Object.assign(network.tun, defaults.tun, value.tun || {}); tunRouteExcludeText.value = list(network.tun.routeExcludeAddress); network.dnsOverrideEnabled = value.dnsOverrideEnabled === true; Object.assign(network.dns, structuredClone(defaultDns), value.dns || {}); syncDnsText()
}
async function load() {
  loading.value = true
  try {
    const [sys, settings, net, proxy, update, iconData, geoData] = await Promise.all([
      api<SystemStatus>('/api/system/status').catch((cause): SystemStatus => ({ available: false, error: errorMessage(cause) })), api<ManagerSettings>('/api/settings'), api<NetworkSettingsResponse>('/api/network/settings').catch((cause): NetworkSettingsResponse => ({ error: errorMessage(cause), settings: null, tunCapability: { supported: false } })), api<ProxyEnvironmentResponse>('/api/system/proxy-environment').catch((cause): ProxyEnvironmentResponse => ({ ok: false, error: errorMessage(cause) })), api<AppUpdateInfo>('/api/app/update-info').catch((cause): AppUpdateInfo => ({ error: errorMessage(cause) })), api<AppIconsResponse>('/api/app/icons').catch((cause): AppIconsResponse => ({ ok: false, error: errorMessage(cause), selected: 'cat-orbit', options: [] })),
      api<GeoStatus>('/api/geo/status').catch((cause): GeoStatus => ({ error: errorMessage(cause), canUpdate: false })),
    ])
    system.value = sys; Object.assign(manager, settings); applyNetwork(net.settings || {}, Number(sys.managedMixedPort || 7890)); environment.value = proxy; appUpdate.value = update; icons.value = iconData; selectedIcon.value = iconData.selected || iconData.defaultId || 'cat-orbit'; geo.value = geoData; Object.assign(geoForm, { autoUpdate: geoData.settings?.autoUpdate === true, updateInterval: Number(geoData.settings?.updateInterval || 24) })
    const setting = proxy.management?.settings
    Object.assign(proxyForm, { enabled: setting?.enabled !== false, followMixedPort: setting?.followMixedPort !== false, port: Number(setting?.port || network.mixed.port || 7890), noProxy: setting?.noProxy || 'localhost,127.0.0.1,::1', targets: { environment: setting?.targets?.environment !== false, profile: setting?.targets?.profile !== false, bashrc: setting?.targets?.bashrc !== false } })
    tunSupported.value = net.tunCapability?.supported !== false; tunSupportText.value = net.tunCapability?.message || (tunSupported.value ? '当前 Mihomo 具备 TUN 所需权限，可直接启用' : !net.tunCapability?.tunDevice ? '当前系统没有 /dev/net/tun，暂不能启用 TUN' : '当前 Mihomo 不是 root 且没有 CAP_NET_ADMIN，暂不能启用 TUN')
    error.value = ''
  } catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}
async function chooseIcon(id: string) {
  if (id === selectedIcon.value) return
  busy.value = `icon-${id}`
  try { const result = await api<AppIconsResponse>('/api/app/icon', jsonRequest('PUT', { iconId: id })); selectedIcon.value = result.selected || id; notify('软件图标已保存；刷新 fnOS 桌面并重新打开窗口后生效') }
  catch (cause) { notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}
async function testController() {
  busy.value = 'test'
  try { await behaviorSave.flush(); const result = await api<{ version?: { version?: string } }>('/api/settings/test', { method: 'POST' }); notify(`连接成功：${result.version?.version || 'Mihomo'}`) }
  catch (cause) { notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}
async function retryBootstrap() { busy.value = 'bootstrap'; try { await api('/api/core/bootstrap/retry', { method: 'POST' }); notify('Mihomo Core 已准备完成'); await load() } catch (cause) { notify(errorMessage(cause), true) } finally { busy.value = '' } }
async function checkAppUpdate() {
  busy.value = 'app-update'
  try {
    const result = await api<{ sourceConfigured?: boolean; updateAvailable?: boolean; currentVersion?: string; latest?: { tag?: string; htmlUrl?: string; asset?: { url?: string } } }>('/api/app/check-update', { method: 'POST' })
    if (!result.sourceConfigured) notify('当前构建未绑定公开 Release 仓库，请通过 fnOS 应用中心或手动 FPK 升级')
    else if (!result.updateAvailable) notify('当前已经是最新版本')
    else if (result.latest?.asset?.url && confirm(`发现 ${result.latest?.tag || '新版本'}，是否下载当前架构的 FPK？`)) window.open(result.latest.asset.url, '_blank', 'noopener')
    else if (result.latest?.htmlUrl) window.open(result.latest.htmlUrl, '_blank', 'noopener')
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}
async function checkCoreUpdate() {
  busy.value = 'core-update'
  try {
    const result = await api<{ updateAvailable?: boolean; currentVersion?: string; canRestartService?: boolean; latest?: { tag?: string } }>('/api/core/check-update', { method: 'POST' })
    if (!result.updateAvailable) return notify('当前已经是最新 Mihomo')
    const restart = result.canRestartService === true && confirm(`发现 ${result.latest?.tag || '新版本'}。确定后将更新内核；选择“确定”会在更新后重启可安全管理的 Core，选择“取消”仅更新文件。`)
    const proceed = restart || confirm(`仅更新 Mihomo 内核文件到 ${result.latest?.tag || '新版本'}，下次重启 Core 后生效。继续？`)
    if (!proceed) return
    const updated = await api<{ alreadyLatest?: boolean; release?: { tag?: string } }>('/api/core/update', jsonRequest('POST', { restart }))
    notify(updated.alreadyLatest ? '当前已经是最新 Mihomo' : `Mihomo 已更新到 ${updated.release?.tag || '新版本'}`); await load()
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}
function clearGeoMessageTimer() {
  if (geoMessageTimer) clearTimeout(geoMessageTimer)
  geoMessageTimer = null
}
function setGeoOperation(state: typeof geoState.value, message: string, dismiss = false) {
  clearGeoMessageTimer()
  geoState.value = state
  geoMessage.value = message
  if (!dismiss) return
  geoMessageTimer = setTimeout(() => {
    geoState.value = 'idle'
    geoMessage.value = ''
    geoMessageTimer = null
  }, 3000)
}
onBeforeUnmount(() => {
  stopTunProgressPolling()
  clearGeoMessageTimer()
})
function applyGeoStatus(value: GeoStatus) {
  geo.value = value
  Object.assign(geoForm, { autoUpdate: value.settings?.autoUpdate === true, updateInterval: Number(value.settings?.updateInterval || 24) })
}
async function saveGeoSettings() {
  if (geo.value.readOnly || !geo.value.canUpdate || geoBusy.value) return
  geoBusy.value = 'settings'; setGeoOperation('saving', '正在备份配置 → Mihomo 校验 → 安全应用 → 确认运行状态…')
  try {
    const result = await api<GeoStatus>('/api/geo/settings', jsonRequest('PUT', { autoUpdate: geoForm.autoUpdate, updateInterval: Number(geoForm.updateInterval) }))
    applyGeoStatus(result); setGeoOperation('success', 'GEO 自动更新设置已校验、应用并持久保存', true)
    notify('GEO 自动更新设置已生效')
  } catch (cause) { setGeoOperation('error', `设置未生效，已保持原配置：${errorMessage(cause)}`, true); notify(errorMessage(cause), true) }
  finally { geoBusy.value = '' }
}
async function downloadGeoAsset(asset: GeoAsset) {
  if (asset.present || geo.value.readOnly || !geo.value.canUpdate || geoBusy.value) return
  const key = String(asset.key || '')
  geoBusy.value = `download-${key}`; setGeoOperation('updating', `正在下载并校验 ${asset.label || key}…`)
  try {
    const result = await api<GeoStatus>('/api/geo/download', jsonRequest('POST', { key }))
    applyGeoStatus(result)
    const downloaded = result.assets?.find(item => item.key === key)
    if (!downloaded?.present) throw new Error(`${asset.label || key} 下载完成，但未检测到文件`)
    setGeoOperation('success', `${asset.label || key} 已下载并校验完成`, true)
    notify(`${asset.label || key} 下载完成`)
  } catch (cause) {
    setGeoOperation('error', `${asset.label || key} 下载失败：${errorMessage(cause)}`, true); notify(errorMessage(cause), true)
  } finally { geoBusy.value = '' }
}
async function updateGeoData() {
  if (!geo.value.canUpdate || geoBusy.value) return
  geoBusy.value = 'update'; setGeoOperation('updating', '正在请求 Mihomo 下载并安全替换 GEO 数据库…')
  try {
    const result = await api<GeoStatus>('/api/geo/update', { method: 'POST' })
    applyGeoStatus(result); setGeoOperation('success', 'GeoIP、GeoSite、Country MMDB 与 ASN MMDB 更新完成', true)
    notify('GEO 数据更新成功')
  } catch (cause) { setGeoOperation('error', `GEO 数据更新失败：${errorMessage(cause)}`, true); notify(errorMessage(cause), true) }
  finally { geoBusy.value = '' }
}
async function initialize() {
  await load()
  if (requestedSection !== 'tun' || error.value) return
  await nextTick()
  document.getElementById('tun-settings')?.scrollIntoView({ block: 'start' })
}
onMounted(initialize)
</script>

<template>
  <AsyncState :loading="loading" :error="error">
    <div class="settings-home"><div class="settings-accordion-list">
      <div v-for="category in categories" :id="category.key === 'tun' ? 'tun-settings' : undefined" :key="category.key" class="settings-accordion-item" :class="{ open: open === category.key }"><button class="settings-accordion-head" type="button" :aria-expanded="open === category.key" @click="open = open === category.key ? null : category.key"><span class="settings-category-icon" :class="{ 'text-icon': category.key === 'dns' }">{{ category.icon }}</span><span class="settings-category-copy"><strong>{{ category.title }}</strong><small>{{ category.description }}</small></span><span class="settings-accordion-chevron" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="m5.5 7.5 4.5 4.5 4.5-4.5" /></svg></span></button>
        <div v-if="open === category.key" class="settings-accordion-body">
          <div v-if="category.key === 'network'" class="settings-accordion-panel network-card"><div class="port-list"><PortRow id="netController" v-model="network.controller" title="Controller API" description="Mihomo REST API · Manager 使用 127.0.0.1 连接" locked @change="saveNetwork" /><PortRow id="netMixed" v-model="network.mixed" title="混合代理端口" description="同时接受 HTTP 与 SOCKS5 代理" @change="saveNetwork(120)" /><PortRow id="netSocks" v-model="network.socks" title="SOCKS5 代理端口" description="单独提供 SOCKS5 入站" :disabled="network.mixed.enabled" @change="saveNetwork" /><PortRow id="netHttp" v-model="network.http" title="HTTP(S) 代理端口" description="单独提供 HTTP CONNECT/HTTP 代理" :disabled="network.mixed.enabled" @change="saveNetwork" /><PortRow id="netRedir" v-model="network.redir" title="Redir 透明代理端口" description="Linux TCP REDIRECT 入站" :disabled="network.mixed.enabled" @change="saveNetwork" /><PortRow id="netTproxy" v-model="network.tproxy" title="TProxy 透明代理端口" description="Linux TPROXY TCP/UDP 入站" :disabled="network.mixed.enabled" @change="saveNetwork" /></div><div class="network-options"><SettingToggle v-model="network.allowLan" title="允许局域网连接" description="允许其他设备访问已启用的代理端口" @change="saveNetwork(120)" /><SettingToggle v-model="network.core.ipv6" title="全局 IPv6" description="允许 Mihomo 接收和处理 IPv6 流量" @change="saveNetwork(120)" /><SettingToggle v-model="network.core.unifiedDelay" title="统一延迟" description="使用统一 RTT 算法，使不同协议的测速更便于比较" @change="saveNetwork(120)" /></div><div class="dns-autosave-state" :class="netState">{{ netMessage }}</div></div>

          <div v-else-if="category.key === 'tun'" class="settings-accordion-panel tun-card" :class="{ 'tun-on': network.tun.enabled }">
            <div class="section-head">
              <div class="tun-section-heading">
                <div class="tun-section-title-row"><h2>TUN 详细设置</h2><span v-if="tunSwitching" class="dashboard-tun-progress settings-tun-progress" role="status" aria-live="polite"><i aria-hidden="true" /><span>{{ tunProgress }}</span></span></div>
                <p>接管 NAS 系统流量；首页可以快速开关，这里配置完整参数</p>
              </div>
              <div class="tun-master"><span :class="network.tun.enabled ? 'good-text' : 'muted-text'">{{ tunSwitching ? (network.tun.enabled ? '正在开启' : '正在关闭') : network.tun.enabled ? '已开启' : '已关闭' }}</span><label class="switch large"><input v-model="network.tun.enabled" type="checkbox" :disabled="tunSwitching || netState === 'pending' || netState === 'saving' || (!tunSupported && !network.tun.enabled)" @change="toggleTunSetting"><span /></label></div>
            </div>
            <div class="tun-capability" :class="tunSupported ? 'ok' : 'warn'"><strong>{{ tunSupported ? '可用' : '不可用' }}</strong><span>{{ tunSupportText }}</span></div>
            <div class="tun-main-grid">
              <div class="field">
                <div class="field-label-row"><label>TUN Stack</label><HelpPopover label="TUN Stack"><strong>协议栈决定 Mihomo 如何处理 TUN 流量</strong><span><b>mixed（推荐）</b>：TCP 使用 System，UDP 使用 gVisor，兼顾稳定性与兼容性。</span><span><b>system</b>：使用 Linux 系统协议栈，通常更稳定、资源占用更低。</span><span><b>gVisor</b>：在用户态处理网络协议，隔离性更强，可用于排查特殊网络兼容问题。</span></HelpPopover></div>
                <select v-model="network.tun.stack" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings(150)"><option value="mixed">mixed（推荐）</option><option value="system">system</option><option value="gvisor">gVisor</option></select>
              </div>
              <div class="field">
                <div class="field-label-row"><label>MTU</label><HelpPopover label="MTU"><strong>单个网络数据包的最大传输尺寸</strong><span>默认值为 <b>1500</b>，与常见以太网环境一致，优先保证兼容性。</span><span>如果 VPN、PPPoE 或多层隧道下仍出现部分网站打不开或连接卡顿，可尝试调整为 <b>1400</b>。</span></HelpPopover></div>
                <input v-model.number="network.tun.mtu" type="number" min="1280" max="65535" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings">
              </div>
            </div>
            <div class="tun-option-grid"><SettingToggle v-model="network.tun.autoRoute" title="自动路由" description="自动把系统流量路由到 TUN" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings(120)" /><SettingToggle v-model="network.tun.autoRedirect" title="Auto Redirect" description="Linux 自动配置 nftables/iptables TCP 重定向" :disabled="tunSwitching || !network.tun.enabled || !network.tun.autoRoute" @change="saveTunSettings(120)" /><SettingToggle v-model="network.tun.autoDetectInterface" title="自动检测出口网卡" description="自动选择实际的外网出口接口" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings(120)" /><SettingToggle v-model="network.tun.dnsHijack" title="DNS 劫持" description="劫持 UDP/TCP 53 到 Mihomo DNS 模块" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings(120)" /><SettingToggle v-model="network.tun.strictRoute" title="严格路由" description="减少流量/DNS 泄漏；复杂网络可能影响其他虚拟网卡" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings(120)" /></div>
            <div class="field tun-route-exclude">
              <div class="field-label-row"><label>排除自定义网段</label><HelpPopover label="排除自定义网段"><strong>让指定目标网段绕过 TUN 自动路由</strong><span>仅在开启“自动路由”时生效，适合局域网、VPN 和其他虚拟网络。</span><span>仅支持 IPv4/IPv6 CIDR，每行填写一个。</span></HelpPopover></div>
              <textarea v-model="tunRouteExcludeText" placeholder="192.168.0.0/16&#10;10.0.0.0/8&#10;fc00::/7" :disabled="tunSwitching || !network.tun.enabled || !network.tun.autoRoute" @change="saveTunSettings(80)" @input="saveTunSettings(1000)" />
              <span class="field-note">每行一个 IPv4/IPv6 CIDR；留空表示不额外排除。</span>
            </div>
            <div v-if="network.tun.enabled && network.tun.dnsHijack && !network.dns.enable" class="tun-capability warn"><strong>DNS</strong><span>开启 DNS 劫持前建议先启用 Mihomo DNS。</span></div>
            <div class="tun-note"><strong>注意</strong><span>TUN 会修改 fnOS 的系统路由与 DNS 流向。默认关闭；配置不可用时可能影响 NAS 访问互联网。</span></div>
          </div>

          <div v-else-if="category.key === 'dns'" class="settings-accordion-panel dns-settings-panel" :class="{ 'dns-on': network.dnsOverrideEnabled }"><div class="dns-overview"><div><strong>DNS 覆写</strong><span>默认关闭；关闭时只保存 DNS 模板，不写入当前启动配置</span></div><label class="switch large"><input v-model="network.dnsOverrideEnabled" type="checkbox" @change="saveDns(120)"><span /></label></div><details class="dns-group"><summary><span><strong>基础设置</strong><small>监听地址、增强模式与常用解析行为</small></span><span class="dns-group-chevron">⌄</span></summary><div class="dns-group-body"><div class="dns-field-grid"><div class="field"><label>DNS 监听地址</label><input v-model="network.dns.listen" class="mono" @change="saveDns(80)"></div><div class="field"><label>增强模式</label><select v-model="network.dns.enhancedMode" @change="saveDns(180)"><option value="fake-ip">Fake IP</option><option value="redir-host">Redir Host</option></select></div><div class="field"><label>Fake IP IPv4 范围</label><input v-model="network.dns.fakeIpRange" class="mono" @change="saveDns(80)"></div><div class="field"><label>Fake IP IPv6 范围</label><input v-model="network.dns.fakeIpRange6" class="mono" @change="saveDns(80)"></div><div class="field"><label>Fake IP 过滤模式</label><select v-model="network.dns.fakeIpFilterMode" @change="saveDns(180)"><option value="blacklist">黑名单</option><option value="whitelist">白名单</option><option value="rule">规则模式</option></select></div></div><div class="dns-toggle-grid"><SettingToggle v-model="network.dns.enable" title="启用 DNS" description="写入覆写配置时启用 Mihomo DNS" @change="saveDns(120)" /><SettingToggle v-model="network.dns.ipv6" title="IPv6 DNS 解析" description="是否返回 AAAA 记录；与全局 IPv6 开关不同" @change="saveDns(180)" /><SettingToggle v-model="network.dns.preferH3" title="优先使用 HTTP/3" description="DoH 优先尝试 HTTP/3" @change="saveDns(180)" /><SettingToggle v-model="network.dns.respectRules" title="DNS 遵循路由规则" description="需要配置代理节点 DNS，避免解析循环" @change="saveDns(180)" /><SettingToggle v-model="network.dns.useHosts" title="使用配置 Hosts" description="使用 Mihomo 配置中的 hosts 映射" @change="saveDns(180)" /><SettingToggle v-model="network.dns.useSystemHosts" title="使用系统 Hosts" description="读取 fnOS 的系统 hosts 文件" @change="saveDns(180)" /><SettingToggle v-model="network.dns.directNameserverFollowPolicy" title="直连 DNS 遵循策略" description="直连域名解析遵循 nameserver-policy" @change="saveDns(180)" /></div></div></details><details class="dns-group"><summary><span><strong>解析服务器</strong><small>每行一个，按用途分开设置</small></span><span class="dns-group-chevron">⌄</span></summary><div class="dns-group-body dns-text-grid"><div v-for="field in dnsServerFields" :key="field.key" class="field"><label>{{ field.label }}</label><textarea v-model="dnsText[field.key]" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div></div></details><details class="dns-group"><summary><span><strong>Fake IP 与域名策略</strong><small>兼容局域网和指定域名 DNS</small></span><span class="dns-group-chevron">⌄</span></summary><div class="dns-group-body dns-text-grid"><div class="field"><label>Fake IP 过滤</label><textarea v-model="dnsText.fakeIpFilter" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div><div class="field"><label>域名服务器策略</label><textarea v-model="dnsText.nameserverPolicy" class="dns-list-input mono" placeholder="+.example.com = server1; server2" @change="saveDns(80)" @input="saveDns(1000)" /></div></div></details><details class="dns-group"><summary><span><strong>回退过滤</strong><small>仅在 fallback 非空时生效</small></span><span class="dns-group-chevron">⌄</span></summary><div class="dns-group-body"><div class="dns-toggle-grid single"><SettingToggle v-model="network.dns.fallbackGeoip" title="启用 GeoIP 过滤" description="结果不属于指定国家时采用 fallback" @change="saveDns(180)" /></div><div class="dns-field-grid"><div class="field"><label>GeoIP 国家代码</label><input v-model="network.dns.fallbackGeoipCode" maxlength="2" @change="saveDns(80)"></div><div class="field"><label>污染结果 IP CIDR</label><textarea v-model="dnsText.fallbackIpCidr" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div><div class="field"><label>直接使用 fallback 的域名</label><textarea v-model="dnsText.fallbackDomain" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div></div></div></details><details class="dns-group"><summary><span><strong>Hosts 映射</strong><small>自定义域名到 IP 或域名的对应关系</small></span><span class="dns-group-chevron">⌄</span></summary><div class="dns-group-body"><div class="field"><label>Hosts</label><textarea v-model="dnsText.hosts" class="dns-list-input mono" placeholder="example.com = 1.1.1.1; 2.2.2.2" @change="saveDns(80)" @input="saveDns(1000)" /></div></div></details><div class="dns-savebar"><div><div class="dns-autosave-state" :class="netState">{{ dnsStatus }}</div><div class="hint">开启后自动备份、校验并应用；Controller 无法恢复时由后端自动回滚。</div></div><div class="actions"><a class="ghost btn" href="#config">查看原始配置</a><button class="ghost" @click="resetDns">恢复默认值</button></div></div></div>

          <div v-else-if="category.key === 'advanced'" class="settings-accordion-panel proxy-env-card"><div v-if="environment.error" class="local-warning">读取系统代理环境失败：{{ environment.error }}</div><div class="proxy-env-manage-card"><div class="proxy-env-manage-head"><div><h3>代理环境变量</h3><p>为命令行与登录会话提供本机 Mihomo 代理</p></div><span class="proxy-env-status" :class="environment.management?.active ? 'active' : ''">{{ !proxyForm.enabled ? '已关闭' : environment.management?.active ? '已启用' : '已配置' }}</span></div><div v-if="!proxyAvailable" class="local-warning">Root Helper 不可用，当前只能查看环境变量，无法修改系统文件。</div><div class="proxy-env-manage-grid"><SettingToggle v-model="proxyForm.enabled" title="启用代理环境变量" description="关闭时仅移除本应用管理的配置块" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.followMixedPort" title="自动跟随 Mixed Port" description="端口变化时自动同步" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><div class="field proxy-env-port-field"><label>代理地址</label><div class="proxy-env-address"><span class="mono">127.0.0.1 :</span><input v-model.number="proxyForm.port" class="mono" type="number" min="1" max="65535" :disabled="!proxyAvailable || proxyForm.followMixedPort" @change="saveEnvironment"></div></div><div class="field proxy-env-no-proxy"><label>NO_PROXY</label><input v-model="proxyForm.noProxy" class="mono" :disabled="!proxyAvailable" @change="saveEnvironment"></div></div><div class="proxy-env-target-title">应用范围</div><div class="proxy-env-target-grid"><SettingToggle v-model="proxyForm.targets.environment" title="系统登录环境" description="/etc/environment" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.targets.profile" title="登录 Shell" description="/etc/profile" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.targets.bashrc" title="Bash 交互环境" description="/etc/bash.bashrc" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /></div><div class="hint proxy-env-manage-note">修改前由后端自动备份原文件；关闭后只移除 Clash for fnos 管理块。新值主要对新登录会话生效。</div></div><div class="proxy-env-divider"><span>当前检测结果</span></div><div class="proxy-env-files"><div v-for="file in environment.files || []" :key="file.path" class="proxy-env-source"><div class="proxy-env-source-head"><strong class="mono">{{ file.path || '--' }}</strong><span class="proxy-env-status" :class="file.variables?.length ? 'active' : ''">{{ !file.exists ? '不存在' : file.readable === false ? '不可读' : file.variables?.length ? '已检测到代理设置' : '未设置' }}</span></div><div v-if="file.error" class="proxy-env-error">{{ file.error }}</div><div v-else class="proxy-env-vars"><div v-for="variable in file.variables || []" :key="`${variable.key}-${variable.line}`" class="proxy-env-var"><span class="mono proxy-env-key">{{ variable.key }}</span><span class="mono proxy-env-value">{{ variable.value }}</span><span v-if="variable.line" class="proxy-env-line">L{{ variable.line }}</span></div><span v-if="!file.variables?.length" class="proxy-env-empty">未设置代理环境变量</span></div></div></div><div class="dns-autosave-state" :class="envState">{{ envMessage }}</div></div>

          <div v-else-if="category.key === 'behavior'" class="settings-accordion-panel"><div class="app-icon-settings"><div class="section-head"><div><h2>软件图标</h2></div></div><div v-if="icons.error" class="local-warning">读取图标设置失败：{{ icons.error }}</div><div class="app-icon-picker"><button v-for="icon in icons.options || []" :key="icon.id" type="button" class="app-icon-choice" :class="{ active: icon.id === selectedIcon }" :disabled="icons.ok === false || Boolean(busy)" :title="icon.name || icon.id" @click="chooseIcon(icon.id)"><img :src="icon.preview || `${APP_PREFIX}/icons/${icon.id}_256.png`" alt=""><span class="app-icon-choice-state">{{ icon.id === selectedIcon ? '✓' : '' }}</span></button></div></div><div class="settings-inner-divider" /><div class="section-head"><div><h2>内核行为</h2><p>Controller 检测、延迟测试与启动行为</p></div><span class="auto-detected">自动管理</span></div><div class="system-grid"><div><span class="system-label">Controller</span><strong class="mono">{{ manager.controller || '--' }}</strong></div><div><span class="system-label">Secret</span><span>{{ manager.hasSecret ? '已从配置读取' : '配置中未设置' }}</span></div></div><div class="form-grid" style="margin-top:14px"><div class="field"><label>延迟测试 URL</label><input v-model="manager.healthcheckUrl" @change="saveBehavior"></div><div class="field"><label>超时（毫秒）</label><input v-model.number="manager.healthcheckTimeout" type="number" @change="saveBehavior"></div><div class="field full"><label><input v-model="manager.persistSelections" type="checkbox" style="width:auto" @change="saveBehavior(120)"> 记住策略组选择</label></div><div class="field full"><label><input v-model="manager.applyManagedConfigOnStart" type="checkbox" style="width:auto" @change="saveBehavior(120)"> Manager 启动后重新应用已保存配置</label></div></div><div class="actions settings-actions"><button class="ghost" :disabled="busy === 'test'" @click="testController">{{ busy === 'test' ? '测试中…' : '测试 Controller' }}</button></div><div class="dns-autosave-state" :class="behaviorState">{{ behaviorMessage }}</div></div>

          <div v-else class="settings-accordion-panel update-panel">
            <div class="update-row app-update-row">
              <div class="update-row-copy">
                <h2>Clash for fnOS</h2>
                <p>应用版本与运行平台</p>
              </div>
              <div class="update-row-facts" aria-label="Clash for fnOS 版本信息">
                <strong>v{{ String(appUpdate.currentVersion || '--').replace(/^v/, '') }}</strong>
                <span class="update-meta">{{ platformLabel(appUpdate.platform) }}</span>
              </div>
              <div class="update-row-actions">
                <button class="ghost" :disabled="busy === 'app-update'" @click="checkAppUpdate">{{ busy === 'app-update' ? '检查中…' : '检查更新' }}</button>
              </div>
            </div>

            <div class="update-row core-update-row">
              <div class="update-row-copy">
                <h2>Mihomo Core</h2>
                <p>核心版本与运行方式</p>
              </div>
              <div class="update-row-facts" aria-label="Mihomo Core 版本信息">
                <strong>{{ system.currentVersion || system.controllerVersion?.version || '--' }}</strong>
                <span class="update-meta">{{ system.mode === 'managed' ? 'Manager 托管' : system.mode === 'external' ? '本机 Core' : '自动检测' }}</span>
              </div>
              <div class="update-details" @mouseleave="coreDetailsOpen = false">
                <button class="update-details-trigger" type="button" :aria-expanded="coreDetailsOpen" @click="coreDetailsOpen = !coreDetailsOpen">查看详情</button>
                <div v-if="coreDetailsOpen" class="update-details-body">
                  <div><span>二进制</span><strong class="mono">{{ system.binaryPath || '--' }}</strong></div>
                  <div><span>启动配置</span><strong class="mono">{{ system.configPath || '--' }}</strong></div>
                </div>
              </div>
              <div class="update-row-actions">
                <button v-if="system.bootstrap?.state === 'error'" :disabled="busy === 'bootstrap'" @click="retryBootstrap">重新检测并启用</button>
                <button class="ghost" :disabled="busy === 'core-update'" @click="checkCoreUpdate">{{ busy === 'core-update' ? '检查中…' : '检查更新' }}</button>
              </div>
            </div>

            <details class="geo-update-section">
              <summary class="geo-update-summary">
                <div class="geo-update-title">
                  <h2>GEO 数据</h2>
                  <span v-if="geoMessage" class="geo-operation-state" :class="geoState" role="status" aria-live="polite"><span v-if="geoBusy" class="geo-spinner" /><span>{{ geoMessage }}</span></span>
                </div>
                <span class="geo-update-chevron" aria-hidden="true">⌄</span>
              </summary>
              <div class="geo-update-body">
              <div class="geo-update-head">
                <div class="update-row-copy"><p>{{ geo.message || '由 Mihomo 管理地理数据库' }}</p></div>
                <div class="geo-controls">
                  <label class="geo-auto"><input v-model="geoForm.autoUpdate" type="checkbox" :disabled="geo.readOnly || !geo.canUpdate || Boolean(geoBusy)"> 自动更新</label>
                  <label class="geo-interval">周期 <select v-model.number="geoForm.updateInterval" :disabled="geo.readOnly || !geo.canUpdate || Boolean(geoBusy)"><option :value="12">12 小时</option><option :value="24">24 小时</option><option :value="72">3 天</option><option :value="168">7 天</option></select></label>
                  <button class="ghost" :disabled="geo.readOnly || !geo.canUpdate || Boolean(geoBusy)" @click="saveGeoSettings">{{ geoBusy === 'settings' ? '应用中…' : '保存设置' }}</button>
                  <button :disabled="!geo.canUpdate || Boolean(geoBusy)" @click="updateGeoData">{{ geoBusy === 'update' ? '更新中…' : '立即更新' }}</button>
                </div>
              </div>
              <div v-if="geo.error" class="local-warning">读取 GEO 状态失败：{{ geo.error }}</div>
              <div class="geo-asset-grid">
                <div v-for="asset in geo.assets || []" :key="asset.key" class="geo-asset-row">
                  <div class="geo-asset-name"><strong>{{ asset.label || asset.key }}</strong><span class="mono">{{ asset.fileName }}</span></div>
                  <span class="geo-asset-size">{{ asset.present ? formatBytes(asset.size) : '未下载' }}</span>
                  <span class="geo-asset-time">{{ asset.present ? formatTime(asset.updatedAt) : '--' }}</span>
                  <span class="geo-asset-status" :class="asset.present ? 'ready' : 'missing'">{{ asset.present ? '可用' : '缺失' }}</span>
                  <div class="geo-asset-actions">
                    <button v-if="!asset.present" class="ghost geo-download-button" :disabled="geo.readOnly || !geo.canUpdate || Boolean(geoBusy)" @click="downloadGeoAsset(asset)">{{ geoBusy === `download-${asset.key}` ? '下载中…' : '下载' }}</button>
                    <a class="geo-asset-source" :href="asset.source" target="_blank" rel="noopener" :title="asset.source">来源</a>
                  </div>
                </div>
              </div>
              </div>
            </details>
          </div>
        </div>
      </div>
    </div></div>
  </AsyncState>
</template>
