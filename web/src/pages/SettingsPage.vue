<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import PortConflictHelp from '@/components/PortConflictHelp.vue'
import AsyncState from '@/components/AsyncState.vue'
import HelpPopover from '@/components/HelpPopover.vue'
import PortRow from '@/components/settings/PortRow.vue'
import SettingToggle from '@/components/settings/SettingToggle.vue'
import { refreshCoreHealth } from '@/composables/useCoreHealth'
import { useAutosave } from '@/composables/useAutosave'
import { useAppUpdateNotice } from '@/composables/useAppUpdateNotice'
import { api, APP_PREFIX, errorMessage, jsonRequest } from '@/services/api'
import { formatBytes, formatTime } from '@/services/format'
import { openStatusStream } from '@/services/status-stream'
import { notify } from '@/services/toast'
import type { AppIconsResponse, AppUpdateInfo, CoreMode, DnsMapping, DnsSetting, GeoAsset, GeoStatus, HostMapping, ManagerSettings, NetworkSetting, NetworkSettingsResponse, PortSetting, ProxyEnvironmentResponse, SystemStatus, TunSetting } from '@/types/api'

type Section = 'core' | 'network' | 'dns' | 'tun' | 'advanced' | 'behavior' | 'update'
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

const requestedSectionValue = new URLSearchParams(location.hash.split('?')[1] || '').get('section')
const requestedSection = (['core', 'network', 'dns', 'tun', 'advanced', 'behavior', 'update'] as Section[]).find(section => section === requestedSectionValue) || null
const loading = ref(true), error = ref(''), open = ref<Section | null>(requestedSection), busy = ref(''), tunSwitching = ref(false)
const tunProgress = ref('')
const coreDetailsOpen = ref(false)
const coreTabs = [{ key: 'manage', label: '内核管理' }, { key: 'connection', label: '连接管理' }, { key: 'geo', label: 'GEO 数据' }] as const
const coreTab = ref<(typeof coreTabs)[number]['key']>('manage')
const dnsTabs = [{ key: 'basic', label: '基础设置' }, { key: 'servers', label: '解析服务器' }, { key: 'fake-ip', label: 'Fake IP 与域名策略' }, { key: 'fallback', label: '回退过滤' }, { key: 'hosts', label: 'Hosts 映射' }] as const
const dnsTab = ref<(typeof dnsTabs)[number]['key']>('basic')
function navigateCoreTab(event: KeyboardEvent, index: number) {
  let next = index
  if (event.key === 'ArrowRight') next = (index + 1) % coreTabs.length
  else if (event.key === 'ArrowLeft') next = (index + coreTabs.length - 1) % coreTabs.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = coreTabs.length - 1
  else return
  event.preventDefault()
  coreTab.value = coreTabs[next]!.key
  document.getElementById(`core-tab-${coreTab.value}`)?.focus()
}
function navigateDnsTab(event: KeyboardEvent, index: number) {
  let next = index
  if (event.key === 'ArrowRight') next = (index + 1) % dnsTabs.length
  else if (event.key === 'ArrowLeft') next = (index + dnsTabs.length - 1) % dnsTabs.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = dnsTabs.length - 1
  else return
  event.preventDefault()
  dnsTab.value = dnsTabs[next]!.key
  document.getElementById(`dns-tab-${dnsTab.value}`)?.focus()
}
const selectedCoreMode = ref<Exclude<CoreMode, 'auto'>>('managed')
const coreModeError = ref('')
const networkOffline = ref(false)
const externalSecret = ref('')
const clearExternalSecret = ref(false)
const revealedSecret = ref('')
const secretVisible = ref(false)
const secretLoading = ref(false)
const system = ref<SystemStatus>({}), manager = reactive<ManagerSettings>({ notifyAppUpdates: true }), network = reactive<NetworkForm>(defaultNetwork())
const tunRouteExcludeText = ref('')
const environment = ref<ProxyEnvironmentResponse>({}), proxyForm = reactive<ProxyEnvForm>({ enabled: true, followMixedPort: true, port: 7890, noProxy: 'localhost,127.0.0.1,::1', targets: { environment: true, profile: true, bashrc: true } })
const appUpdate = ref<AppUpdateInfo>({}), icons = ref<AppIconsResponse>({}), selectedIcon = ref('cat-orbit'), tunSupported = ref(true), tunSupportText = ref('当前 Mihomo 具备 TUN 所需权限，可直接启用')
const geo = ref<GeoStatus>({}), geoForm = reactive({ autoUpdate: false, updateInterval: 24 }), geoBusy = ref(''), geoState = ref<'idle' | 'saving' | 'updating' | 'success' | 'error'>('idle'), geoMessage = ref('')
const dnsText = reactive({ defaultNameserver: '', nameserver: '', fallback: '', proxyServerNameserver: '', directNameserver: '', fakeIpFilter: '', nameserverPolicy: '', fallbackIpCidr: '', fallbackDomain: '', hosts: '' })
const netSave = useAutosave<NetworkSettingsResponse>('/api/network/settings', { progressEndpoint: '/api/network/settings/status', mergePending: true, onSaved: result => {
  if (!result.proxyEnvironment) return
  environment.value = result.proxyEnvironment
  const syncedPort = Number(result.proxyEnvironment.management?.settings?.port || 0)
  if (proxyForm.followMixedPort && syncedPort > 0) proxyForm.port = syncedPort
} }), dnsSave = useAutosave('/api/network/settings'), behaviorSave = useAutosave('/api/settings')
const { available: appUpdateAvailable, applyResult: applyAppUpdateResult, check: checkAppUpdateNotice, setEnabled: setAppUpdateNoticeEnabled } = useAppUpdateNotice()
const envSave = useAutosave<ProxyEnvironmentResponse>('/api/system/proxy-environment', { onSaved: result => { environment.value = result } })
const netState = netSave.state, netMessage = netSave.message, dnsState = dnsSave.state, dnsMessage = dnsSave.message, behaviorState = behaviorSave.state, behaviorMessage = behaviorSave.message, envState = envSave.state, envMessage = envSave.message
let closeTunProgressStream: (() => void) | null = null
let geoMessageTimer: ReturnType<typeof setTimeout> | null = null
const dnsServerFields: Array<{ key: keyof Pick<typeof dnsText, 'defaultNameserver' | 'nameserver' | 'fallback' | 'proxyServerNameserver' | 'directNameserver'>; label: string }> = [{ key: 'defaultNameserver', label: '默认域名服务器' }, { key: 'nameserver', label: '域名服务器' }, { key: 'fallback', label: '回退服务器' }, { key: 'proxyServerNameserver', label: '代理节点 DNS' }, { key: 'directNameserver', label: '直连域名服务器' }]

const categories: Array<{ key: Section; title: string; description: string }> = [
  { key: 'core', title: 'Mihomo Core 设置', description: '运行方式、启动行为、内核启停与更新、Controller 及 GEO 数据' },
  { key: 'network', title: '网络与端口', description: '代理端口、IPv6、统一延迟与局域网选项' },
  { key: 'dns', title: 'DNS 与解析', description: 'Mihomo DNS、解析服务器、Fake IP、回退策略与 Hosts' },
  { key: 'tun', title: '虚拟网卡(TUN)设置', description: 'TUN 详细参数与系统流量接管' },
  { key: 'advanced', title: '环境变量设置', description: '管理系统登录与 Shell 的代理环境变量' },
  { key: 'behavior', title: '其他设置', description: '软件图标与应用外观' },
  { key: 'update', title: '更新设置', description: '应用版本、检查更新与更新提示' },
]
const dnsStatus = computed(() => dnsState.value === 'idle' ? (network.dnsOverrideEnabled ? '已启用 DNS 覆写' : 'DNS 覆写已关闭') : dnsMessage.value)
const proxyAvailable = computed(() => system.value.available !== false && system.value.privileged !== false && Boolean(environment.value.management))
const latestAppVersion = computed(() => String(appUpdate.value.latest?.tag || '--').replace(/^v/, ''))
const proxyPort = computed({
  get: () => proxyForm.followMixedPort ? network.mixed.port : proxyForm.port,
  set: value => { proxyForm.port = Number(value) },
})

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
async function toggleSection(section: Section, event: MouseEvent) {
  const item = (event.currentTarget as HTMLElement).closest<HTMLElement>('.settings-accordion-item')
  const expanding = open.value !== section
  open.value = expanding ? section : null
  if (!expanding) return
  await nextTick()
  if (open.value !== section || !item?.isConnected) return
  const container = item.closest<HTMLElement>('.content')
  if (container) {
    const top = container.scrollTop + item.getBoundingClientRect().top - container.getBoundingClientRect().top - 12
    container.scrollTo({ top: Math.max(0, top), behavior: 'instant' })
  }
}
async function openSettingsSection(section: Section) {
  open.value = section
  history.replaceState(history.state, '', `#settings?section=${section}`)
  await nextTick()
  document.getElementById(`${section}-settings`)?.scrollIntoView({ block: 'start', behavior: 'instant' })
}
function numericDelay(value: number | Event | undefined, fallback: number) { return typeof value === 'number' ? value : fallback }
function platformLabel(value?: string) {
  const platform = String(value || '').trim().toLowerCase()
  if (platform === 'x86' || platform === 'amd64' || platform === 'x86_64') return 'x86_64'
  if (platform === 'arm' || platform === 'aarch64' || platform === 'arm64') return 'arm64'
  return value || '--'
}
function saveNetwork(key: 'controller' | 'mixed' | 'socks' | 'http' | 'redir' | 'tproxy' | 'allowLan' | 'ipv6' | 'unifiedDelay', delay = 250) {
  const payload: Record<string, unknown> = {}
  if (key === 'ipv6' || key === 'unifiedDelay') payload.core = { [key]: network.core[key] }
  else if (key === 'allowLan') payload.allowLan = network.allowLan
  else {
    payload[key] = { ...network[key], enabled: key === 'controller' || network[key].enabled }
    if (key === 'mixed' && network.mixed.enabled) {
      for (const other of ['socks', 'http', 'redir', 'tproxy'] as const) {
        if (network[other].enabled) { network[other].enabled = false; payload[other] = { ...network[other] } }
      }
    }
  }
  netSave.queue(payload, delay)
}
function saveTunSettings(key: keyof Required<TunSetting>, delay = 250) {
  if (key === 'routeExcludeAddress') network.tun.routeExcludeAddress = lines(tunRouteExcludeText.value)
  const tun: Partial<TunSetting> = { [key]: network.tun[key] }
  if (key === 'autoRoute' && !network.tun.autoRoute) { network.tun.autoRedirect = false; tun.autoRedirect = false }
  netSave.queue({ tun }, delay)
}
async function toggleTunSetting() {
  const next = network.tun.enabled
  const previous = !next
  tunSwitching.value = true
  tunProgress.value = next ? '正在准备开启 TUN…' : '正在准备关闭 TUN…'
  closeTunProgressStream?.()
  closeTunProgressStream = openStatusStream<TunOperationStatus>('/api/network/tun/status', status => {
    if (tunSwitching.value && status.active && status.message) tunProgress.value = status.message
  })
  try {
    const result = await api<{ enabled?: boolean }>('/api/network/tun', jsonRequest('PUT', { enabled: next }))
    if (result.enabled !== next) throw new Error('TUN 状态未按预期生效')
    notify(next ? '虚拟网卡(TUN)模式已开启' : '虚拟网卡(TUN)模式已关闭')
  } catch (cause) {
    network.tun.enabled = previous
    notify(errorMessage(cause), true)
  } finally {
    tunSwitching.value = false
    closeTunProgressStream?.()
    closeTunProgressStream = null
    tunProgress.value = ''
  }
}
function dnsPayload() {
  network.dns.defaultNameserver = lines(dnsText.defaultNameserver); network.dns.nameserver = lines(dnsText.nameserver); network.dns.fallback = lines(dnsText.fallback); network.dns.proxyServerNameserver = lines(dnsText.proxyServerNameserver); network.dns.directNameserver = lines(dnsText.directNameserver); network.dns.fakeIpFilter = lines(dnsText.fakeIpFilter); network.dns.nameserverPolicy = mappings(dnsText.nameserverPolicy, 'dns') as DnsMapping[]; network.dns.fallbackIpCidr = lines(dnsText.fallbackIpCidr); network.dns.fallbackDomain = lines(dnsText.fallbackDomain); network.dns.hosts = mappings(dnsText.hosts, 'hosts') as HostMapping[]; network.dns.fallbackGeoipCode = network.dns.fallbackGeoipCode.trim().toUpperCase()
  return { dnsOverrideEnabled: network.dnsOverrideEnabled, dns: network.dns }
}
function saveDns(delay = 1000) { try { dnsSave.queue(dnsPayload(), delay) } catch (cause) { notify(errorMessage(cause), true) } }
function resetDns() { Object.assign(network.dns, structuredClone(defaultDns)); syncDnsText(); saveDns(0); notify('已恢复默认值，并自动保存') }
function behaviorPayload() { return { persistSelections: Boolean(manager.persistSelections), notifyAppUpdates: manager.notifyAppUpdates !== false, healthcheckUrl: manager.healthcheckUrl || '', healthcheckTimeout: Number(manager.healthcheckTimeout || 0) } }
function saveBehavior(delay: number | Event = 250) { behaviorSave.queue(behaviorPayload(), numericDelay(delay, 250)) }
function saveAppUpdatePreference() {
  const next = manager.notifyAppUpdates !== false
  setAppUpdateNoticeEnabled(next)
  behaviorSave.queue(behaviorPayload(), 120)
  if (next) void checkAppUpdateNotice().catch(() => undefined)
}
function saveEnvironment(delay: number | Event = 250) { envSave.queue({ ...proxyForm, port: Number(proxyForm.followMixedPort ? network.mixed.port : proxyForm.port) }, numericDelay(delay, 250)) }

function applyNetwork(value: NetworkSetting, fallbackPort: number) {
  const defaults = defaultNetwork()
  Object.assign(network.controller, defaults.controller, value.controller || {})
  for (const key of ['mixed', 'socks', 'http', 'redir', 'tproxy'] as const) Object.assign(network[key], defaults[key], value[key] || {})
  network.mixed.port ||= fallbackPort; network.allowLan = Boolean(value.allowLan); Object.assign(network.core, defaults.core, value.core || {}); Object.assign(network.tun, defaults.tun, value.tun || {}); tunRouteExcludeText.value = list(network.tun.routeExcludeAddress); network.dnsOverrideEnabled = value.dnsOverrideEnabled === true; Object.assign(network.dns, structuredClone(defaultDns), value.dns || {}); syncDnsText()
}
async function load() {
  loading.value = true
  secretVisible.value = false
  revealedSecret.value = ''
  try {
    const [sys, settings, net, proxy, update, iconData, geoData] = await Promise.all([
      api<SystemStatus>('/api/system/status').catch((cause): SystemStatus => ({ available: false, error: errorMessage(cause) })), api<ManagerSettings>('/api/settings'), api<NetworkSettingsResponse>('/api/network/settings').catch((cause): NetworkSettingsResponse => ({ error: errorMessage(cause), settings: null, tunCapability: { supported: false } })), api<ProxyEnvironmentResponse>('/api/system/proxy-environment').catch((cause): ProxyEnvironmentResponse => ({ ok: false, error: errorMessage(cause) })), api<AppUpdateInfo>('/api/app/update-info').catch((cause): AppUpdateInfo => ({ error: errorMessage(cause) })), api<AppIconsResponse>('/api/app/icons').catch((cause): AppIconsResponse => ({ ok: false, error: errorMessage(cause), selected: 'cat-orbit', options: [] })),
      api<GeoStatus>('/api/geo/status').catch((cause): GeoStatus => ({ error: errorMessage(cause), canUpdate: false })),
    ])
    networkOffline.value = net.offline === true
    system.value = sys; selectedCoreMode.value = sys.coreMode === 'external' ? 'external' : 'managed'; Object.assign(manager, settings); setAppUpdateNoticeEnabled(settings.notifyAppUpdates !== false); applyNetwork(net.settings || {}, Number(sys.managedMixedPort || 7890)); environment.value = proxy; appUpdate.value = update; icons.value = iconData; selectedIcon.value = iconData.selected || iconData.defaultId || 'cat-orbit'; geo.value = geoData; Object.assign(geoForm, { autoUpdate: geoData.settings?.autoUpdate === true, updateInterval: Number(geoData.settings?.updateInterval || 24) })
    const setting = proxy.management?.settings
    Object.assign(proxyForm, { enabled: setting?.enabled !== false, followMixedPort: setting?.followMixedPort !== false, port: Number(setting?.port || network.mixed.port || 7890), noProxy: setting?.noProxy || 'localhost,127.0.0.1,::1', targets: { environment: setting?.targets?.environment !== false, profile: setting?.targets?.profile !== false, bashrc: setting?.targets?.bashrc !== false } })
    tunSupported.value = net.tunCapability?.supported !== false; tunSupportText.value = net.tunCapability?.message || (tunSupported.value ? '当前 Mihomo 具备 TUN 所需权限，可直接启用' : !net.tunCapability?.tunDevice ? '当前系统没有 /dev/net/tun，暂不能启用 TUN' : '当前 Mihomo 不是 root 且没有 CAP_NET_ADMIN，暂不能启用 TUN')
    error.value = ''
  } catch (cause) { error.value = errorMessage(cause) }
  finally { loading.value = false }
}
async function enableControllerDetection() {
  if (manager.controllerAutoDetect !== false) await saveExternalController(true)
}
async function saveExternalController(automatic = false) {
  busy.value = 'controller'
  try {
    await api('/api/settings', jsonRequest('PUT', { controllerAutoDetect: manager.controllerAutoDetect !== false, ...(automatic ? {} : { controller: manager.controller, ...(clearExternalSecret.value ? { clearSecret: true } : externalSecret.value ? { secret: externalSecret.value } : {}) }) }))
    externalSecret.value = ''
    clearExternalSecret.value = false
    secretVisible.value = false
    revealedSecret.value = ''
    await refreshCoreHealth()
    const saved = await api<ManagerSettings>('/api/settings')
    Object.assign(manager, saved)
    notify(automatic ? '已启用自动检测' : '连接设置已保存')
  } catch (cause) { if (automatic) manager.controllerAutoDetect = false; notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}

async function toggleSecretVisibility() {
  if (secretVisible.value) {
    secretVisible.value = false
    revealedSecret.value = ''
    return
  }
  if (!manager.hasSecret || secretLoading.value) return
  secretLoading.value = true
  try {
    const result = await api<{ secret?: string }>('/api/settings/secret', jsonRequest('POST'))
    revealedSecret.value = result.secret || ''
    secretVisible.value = true
  } catch (cause) {
    notify(`读取 Secret 失败：${errorMessage(cause)}`, true)
  } finally {
    secretLoading.value = false
  }
}

async function switchCoreMode() {
  if (busy.value) return
  busy.value = 'core-mode'
  coreModeError.value = ''
  try {
    await api('/api/core/mode', jsonRequest('PUT', { mode: selectedCoreMode.value }))
    notify('切换成功')
  } catch (cause) {
    coreModeError.value = errorMessage(cause)
    notify(coreModeError.value, true)
  } finally {
    await load()
    await refreshCoreHealth().catch(() => undefined)
    busy.value = ''
  }
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
    const result = await checkAppUpdateNotice()
    appUpdate.value = result
    applyAppUpdateResult(result)
    if (!result.sourceConfigured) notify('当前构建未绑定公开 Release 仓库，请通过 fnOS 应用中心或手动 FPK 升级')
    else if (!result.updateAvailable) notify('当前已经是最新版本')
    else notify(`发现新版本 v${latestAppVersion.value}`)
  } catch (cause) { notify(errorMessage(cause), true) }
  finally { busy.value = '' }
}
function openAppUpdate() {
  const url = appUpdate.value.latest?.asset?.url || appUpdate.value.latest?.htmlUrl
  if (!url) return notify('未找到可用的更新下载地址', true)
  window.open(url, '_blank', 'noopener')
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
async function setCoreRunning(running: boolean) {
  if (busy.value || system.value.mode !== 'managed') return
  coreModeError.value = ''
  busy.value = running ? 'core-start' : 'core-stop'
  try {
    await api(running ? '/api/core/start' : '/api/core/stop', { method: 'POST' })
    notify(running ? '托管 Core 已启动' : '托管 Core 已停止，管理页面仍可使用')
  } catch (cause) { coreModeError.value = errorMessage(cause); notify(coreModeError.value, true) }
  finally {
    await load()
    await refreshCoreHealth().catch(() => undefined)
    busy.value = ''
  }
}

async function restartCore() {
  if (system.value.mode !== 'managed' || system.value.canRestartService !== true) return
  if (!confirm('确定要重启 Mihomo Core 吗？重启期间代理连接会短暂中断。')) return
  coreModeError.value = ''
  busy.value = 'core-restart'
  try {
    await api('/api/core/restart', { method: 'POST' })
    notify('Mihomo Core 已重启')
    await load()
  } catch (cause) { coreModeError.value = errorMessage(cause); notify(coreModeError.value, true) }
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
  closeTunProgressStream?.()
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
  if (!requestedSection || error.value) return
  await nextTick()
  document.getElementById(`${requestedSection}-settings`)?.scrollIntoView({ block: 'start' })
}
onMounted(initialize)
</script>

<template>
  <Teleport defer to="#page-title-meta">
    <span class="settings-save-hint">修改参数后，点击空白处会自动保存</span>
  </Teleport>

  <AsyncState :loading="loading" :error="error">
    <div class="settings-home"><div class="settings-accordion-list">
      <div v-for="category in categories" :id="category.key === 'tun' ? 'tun-settings' : undefined" :key="category.key" class="settings-accordion-item" :class="{ open: open === category.key }"><button class="settings-accordion-head" type="button" :aria-expanded="open === category.key" @click="toggleSection(category.key, $event)">
        <span class="settings-category-icon" :class="`icon-${category.key}`" aria-hidden="true">
          <svg v-if="category.key === 'core'" viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" rx="2" /><rect x="9" y="9" width="6" height="6" rx="1" /><path d="M9 3v3m6-3v3M9 18v3m6-3v3M3 9h3m-3 6h3m12-6h3m-3 6h3" /></svg>
          <svg v-else-if="category.key === 'network'" viewBox="0 0 24 24"><circle cx="6" cy="7" r="2.5" /><circle cx="18" cy="7" r="2.5" /><circle cx="12" cy="17" r="2.5" /><path d="M8.5 7h7M7.3 9.2l3.4 5.6m6-5.6-3.4 5.6" /></svg>
          <svg v-else-if="category.key === 'dns'" viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5" /><path d="M3.5 12h17M12 3.5c2.3 2.3 3.5 5.1 3.5 8.5s-1.2 6.2-3.5 8.5M12 3.5C9.7 5.8 8.5 8.6 8.5 12s1.2 6.2 3.5 8.5" /></svg>
          <svg v-else-if="category.key === 'tun'" class="tun-category-glyph" viewBox="0 0 24 24"><rect x="3" y="6" width="5" height="12" rx="2" /><rect x="16" y="6" width="5" height="12" rx="2" /><path d="M8 10h8m-2-2 2 2-2 2M16 14H8m2 2-2-2 2-2" /></svg>
          <svg v-else-if="category.key === 'advanced'" viewBox="0 0 24 24"><rect x="3" y="4.5" width="18" height="15" rx="2.5" /><path d="m7 9 3 3-3 3m6 0h4" /></svg>
          <svg v-else-if="category.key === 'behavior'" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.8 2.8-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.6v.2h-4V21a1.7 1.7 0 0 0-1-1.6 1.7 1.7 0 0 0-1.9.3l-.1.1L4.2 17l.1-.1a1.7 1.7 0 0 0 .3-1.9A1.7 1.7 0 0 0 3 14H2.8v-4H3a1.7 1.7 0 0 0 1.6-1 1.7 1.7 0 0 0-.3-1.9L4.2 7 7 4.2l.1.1A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-1.6v-.2h4V3a1.7 1.7 0 0 0 1 1.6 1.7 1.7 0 0 0 1.9-.3l.1-.1L19.8 7l-.1.1a1.7 1.7 0 0 0-.3 1.9 1.7 1.7 0 0 0 1.6 1h.2v4H21a1.7 1.7 0 0 0-1.6 1Z" /></svg>
          <svg v-else viewBox="0 0 24 24"><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /></svg>
        </span>
        <span class="settings-category-copy"><strong>{{ category.title }}</strong><small>{{ category.description }}</small></span><span class="settings-category-tail"><span v-if="category.key === 'update' && appUpdateAvailable" class="settings-update-notice" role="status"><span class="update-notice-dot" aria-hidden="true" />有新版本</span><span class="settings-accordion-chevron" aria-hidden="true"><svg viewBox="0 0 20 20"><path d="m5.5 7.5 4.5 4.5 4.5-4.5" /></svg></span></span>
      </button>
        <div v-if="open === category.key" class="settings-accordion-body">
          <div v-if="category.key === 'network'" class="settings-accordion-panel network-card"><p v-if="networkOffline" class="hint">Core 已停止，当前编辑的是托管启动配置；修改会自动保存，完成后请启动内核。</p><div class="port-list"><PortRow id="netController" v-model="network.controller" title="Controller API" description="Mihomo REST API · Manager 使用 127.0.0.1 连接" locked @change="saveNetwork('controller')" /><PortRow id="netMixed" v-model="network.mixed" title="混合代理端口" description="同时接受 HTTP 与 SOCKS5 代理" @change="saveNetwork('mixed')" /><PortRow id="netSocks" v-model="network.socks" title="SOCKS5 代理端口" description="单独提供 SOCKS5 入站" :disabled="network.mixed.enabled" @change="saveNetwork('socks')" /><PortRow id="netHttp" v-model="network.http" title="HTTP(S) 代理端口" description="单独提供 HTTP CONNECT/HTTP 代理" :disabled="network.mixed.enabled" @change="saveNetwork('http')" /><PortRow id="netRedir" v-model="network.redir" title="Redir 透明代理端口" description="Linux TCP REDIRECT 入站" :disabled="network.mixed.enabled" @change="saveNetwork('redir')" /><PortRow id="netTproxy" v-model="network.tproxy" title="TProxy 透明代理端口" description="Linux TPROXY TCP/UDP 入站" :disabled="network.mixed.enabled" @change="saveNetwork('tproxy')" /></div><div class="network-options"><SettingToggle v-model="network.allowLan" title="允许局域网连接" description="允许其他设备访问已启用的代理端口" @change="saveNetwork('allowLan')" /><SettingToggle v-model="network.core.ipv6" title="全局 IPv6" description="允许 Mihomo 接收和处理 IPv6 流量" @change="saveNetwork('ipv6')" /><SettingToggle v-model="network.core.unifiedDelay" title="统一延迟" description="使用统一 RTT 算法，使不同协议的测速更便于比较" @change="saveNetwork('unifiedDelay')" /></div><div class="dns-autosave-state" :class="netState" role="status" aria-live="polite">{{ netMessage }}</div></div>

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
                <select v-model="network.tun.stack" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('stack')"><option value="mixed">mixed（推荐）</option><option value="system">system</option><option value="gvisor">gVisor</option></select>
              </div>
              <div class="field">
                <div class="field-label-row"><label>MTU</label><HelpPopover label="MTU"><strong>单个网络数据包的最大传输尺寸</strong><span>默认值为 <b>1500</b>，与常见以太网环境一致，优先保证兼容性。</span><span>如果 VPN、PPPoE 或多层隧道下仍出现部分网站打不开或连接卡顿，可尝试调整为 <b>1400</b>。</span></HelpPopover></div>
                <input v-model.number="network.tun.mtu" type="number" min="1280" max="65535" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('mtu')">
              </div>
            </div>
            <div class="tun-option-grid"><SettingToggle v-model="network.tun.autoRoute" title="自动路由" description="自动把系统流量路由到 TUN" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('autoRoute')" /><SettingToggle v-model="network.tun.autoRedirect" title="Auto Redirect" description="Linux 自动配置 nftables/iptables TCP 重定向" :disabled="tunSwitching || !network.tun.enabled || !network.tun.autoRoute" @change="saveTunSettings('autoRedirect')" /><SettingToggle v-model="network.tun.autoDetectInterface" title="自动检测出口网卡" description="自动选择实际的外网出口接口" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('autoDetectInterface')" /><SettingToggle v-model="network.tun.dnsHijack" title="DNS 劫持" description="劫持 UDP/TCP 53 到 Mihomo DNS 模块" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('dnsHijack')" /><SettingToggle v-model="network.tun.strictRoute" title="严格路由" description="减少流量/DNS 泄漏；复杂网络可能影响其他虚拟网卡" :disabled="tunSwitching || !network.tun.enabled" @change="saveTunSettings('strictRoute')" /></div>
            <div class="field tun-route-exclude">
              <div class="field-label-row"><label>排除自定义网段</label><HelpPopover label="排除自定义网段"><strong>让指定目标网段绕过 TUN 自动路由</strong><span>仅在开启“自动路由”时生效，适合局域网、VPN 和其他虚拟网络。</span><span>仅支持 IPv4/IPv6 CIDR，每行填写一个。</span></HelpPopover></div>
              <textarea v-model="tunRouteExcludeText" placeholder="192.168.0.0/16&#10;10.0.0.0/8&#10;fc00::/7" :disabled="tunSwitching || !network.tun.enabled || !network.tun.autoRoute" @change="saveTunSettings('routeExcludeAddress', 80)" @input="saveTunSettings('routeExcludeAddress', 1000)" />
              <span class="field-note">每行一个 IPv4/IPv6 CIDR；留空表示不额外排除。</span>
            </div>
            <div v-if="network.tun.enabled && network.tun.dnsHijack && !network.dns.enable" class="tun-capability warn"><strong>DNS</strong><span>开启 DNS 劫持前建议先启用 Mihomo DNS。</span></div>
            <div class="tun-note"><strong>注意</strong><span>TUN 会修改 fnOS 的系统路由与 DNS 流向。默认关闭；配置不可用时可能影响 NAS 访问互联网。</span></div>
          </div>

          <div v-else-if="category.key === 'dns'" class="settings-accordion-panel dns-settings-panel" :class="{ 'dns-on': network.dnsOverrideEnabled }">
            <div class="dns-overview"><div><strong>DNS 覆写</strong><span>默认关闭；关闭时只保存 DNS 模板，不写入当前启动配置</span></div><label class="switch large"><input v-model="network.dnsOverrideEnabled" type="checkbox" @change="saveDns(120)"><span /></label></div>
            <div class="dns-tabs" role="tablist" aria-label="DNS 设置">
              <button v-for="(tab, index) in dnsTabs" :id="`dns-tab-${tab.key}`" :key="tab.key" type="button" role="tab" :aria-selected="dnsTab === tab.key" :aria-controls="`dns-panel-${tab.key}`" :tabindex="dnsTab === tab.key ? 0 : -1" @click="dnsTab = tab.key" @keydown="navigateDnsTab($event, index)">{{ tab.label }}</button>
            </div>
            <div class="dns-tab-panels">
              <div v-show="dnsTab === 'basic'" id="dns-panel-basic" class="dns-tab-panel" role="tabpanel" aria-labelledby="dns-tab-basic">
                <div class="dns-field-grid"><div class="field"><label>DNS 监听地址</label><input v-model="network.dns.listen" class="mono" @change="saveDns(80)"></div><div class="field"><label>增强模式</label><select v-model="network.dns.enhancedMode" @change="saveDns(180)"><option value="fake-ip">Fake IP</option><option value="redir-host">Redir Host</option></select></div><div class="field"><label>Fake IP IPv4 范围</label><input v-model="network.dns.fakeIpRange" class="mono" @change="saveDns(80)"></div><div class="field"><label>Fake IP IPv6 范围</label><input v-model="network.dns.fakeIpRange6" class="mono" @change="saveDns(80)"></div><div class="field"><label>Fake IP 过滤模式</label><select v-model="network.dns.fakeIpFilterMode" @change="saveDns(180)"><option value="blacklist">黑名单</option><option value="whitelist">白名单</option><option value="rule">规则模式</option></select></div></div>
                <div class="dns-toggle-grid"><SettingToggle v-model="network.dns.enable" title="启用 DNS" description="写入覆写配置时启用 Mihomo DNS" @change="saveDns(120)" /><SettingToggle v-model="network.dns.ipv6" title="IPv6 DNS 解析" description="是否返回 AAAA 记录；与全局 IPv6 开关不同" @change="saveDns(180)" /><SettingToggle v-model="network.dns.preferH3" title="优先使用 HTTP/3" description="DoH 优先尝试 HTTP/3" @change="saveDns(180)" /><SettingToggle v-model="network.dns.respectRules" title="DNS 遵循路由规则" description="需要配置代理节点 DNS，避免解析循环" @change="saveDns(180)" /><SettingToggle v-model="network.dns.useHosts" title="使用配置 Hosts" description="使用 Mihomo 配置中的 hosts 映射" @change="saveDns(180)" /><SettingToggle v-model="network.dns.useSystemHosts" title="使用系统 Hosts" description="读取 fnOS 的系统 hosts 文件" @change="saveDns(180)" /><SettingToggle v-model="network.dns.directNameserverFollowPolicy" title="直连 DNS 遵循策略" description="直连域名解析遵循 nameserver-policy" @change="saveDns(180)" /></div>
              </div>
              <div v-show="dnsTab === 'servers'" id="dns-panel-servers" class="dns-tab-panel dns-text-grid" role="tabpanel" aria-labelledby="dns-tab-servers"><div v-for="field in dnsServerFields" :key="field.key" class="field"><label>{{ field.label }}</label><textarea v-model="dnsText[field.key]" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div></div>
              <div v-show="dnsTab === 'fake-ip'" id="dns-panel-fake-ip" class="dns-tab-panel dns-text-grid" role="tabpanel" aria-labelledby="dns-tab-fake-ip"><div class="field"><label>Fake IP 过滤</label><textarea v-model="dnsText.fakeIpFilter" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div><div class="field"><label>域名服务器策略</label><textarea v-model="dnsText.nameserverPolicy" class="dns-list-input mono" placeholder="+.example.com = server1; server2" @change="saveDns(80)" @input="saveDns(1000)" /></div></div>
              <div v-show="dnsTab === 'fallback'" id="dns-panel-fallback" class="dns-tab-panel" role="tabpanel" aria-labelledby="dns-tab-fallback"><div class="dns-toggle-grid single"><SettingToggle v-model="network.dns.fallbackGeoip" title="启用 GeoIP 过滤" description="结果不属于指定国家时采用 fallback" @change="saveDns(180)" /></div><div class="dns-field-grid dns-fallback-fields"><div class="field full dns-country-code-row"><label>GeoIP 国家代码</label><input v-model="network.dns.fallbackGeoipCode" maxlength="2" @change="saveDns(80)"></div><div class="field"><label>污染结果 IP CIDR</label><textarea v-model="dnsText.fallbackIpCidr" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div><div class="field"><label>直接使用 fallback 的域名</label><textarea v-model="dnsText.fallbackDomain" class="dns-list-input mono" @change="saveDns(80)" @input="saveDns(1000)" /></div></div></div>
              <div v-show="dnsTab === 'hosts'" id="dns-panel-hosts" class="dns-tab-panel" role="tabpanel" aria-labelledby="dns-tab-hosts"><div class="field"><label>Hosts</label><textarea v-model="dnsText.hosts" class="dns-list-input mono" placeholder="example.com = 1.1.1.1; 2.2.2.2" @change="saveDns(80)" @input="saveDns(1000)" /></div></div>
            </div>
            <div class="dns-savebar"><div><div class="dns-autosave-state" :class="netState" role="status" aria-live="polite">{{ dnsStatus }}</div><div class="hint">开启后自动备份、校验并应用；Controller 无法恢复时由后端自动回滚。</div></div><div class="actions"><a class="ghost btn small" href="#config">查看原始配置</a><button class="ghost small" @click="resetDns">恢复默认值</button></div></div>
          </div>

          <div v-else-if="category.key === 'advanced'" class="settings-accordion-panel proxy-env-card"><div v-if="environment.error" class="local-warning">读取系统代理环境失败：{{ environment.error }}</div><div class="proxy-env-manage"><div v-if="!proxyAvailable" class="local-warning">Root Helper 不可用，当前只能查看环境变量，无法修改系统文件。</div><div class="proxy-env-manage-grid"><SettingToggle v-model="proxyForm.enabled" title="启用代理环境变量" description="关闭时仅移除本应用管理的配置块" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.followMixedPort" title="自动跟随 Mixed Port" description="端口变化时自动同步" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><div class="field proxy-env-port-field"><label>代理地址</label><div class="proxy-env-address"><span class="mono">127.0.0.1 :</span><input v-model.number="proxyPort" class="mono" type="number" min="1" max="65535" :disabled="!proxyAvailable || proxyForm.followMixedPort" @change="saveEnvironment"></div></div><div class="field proxy-env-no-proxy"><label>NO_PROXY</label><input v-model="proxyForm.noProxy" class="mono" :disabled="!proxyAvailable" @change="saveEnvironment"></div></div><div class="proxy-env-target-title">应用范围</div><div class="proxy-env-target-grid"><SettingToggle v-model="proxyForm.targets.environment" title="系统登录环境" description="/etc/environment" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.targets.profile" title="登录 Shell" description="/etc/profile" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /><SettingToggle v-model="proxyForm.targets.bashrc" title="Bash 交互环境" description="/etc/bash.bashrc" :disabled="!proxyAvailable" @change="saveEnvironment(120)" /></div><div class="hint proxy-env-manage-note">修改前由后端自动备份原文件；关闭后只移除 Clash for fnos 管理块。新值主要对新登录会话生效。</div></div><div class="proxy-env-divider"><span>当前检测结果</span></div><div class="proxy-env-files"><div v-for="file in environment.files || []" :key="file.path" class="proxy-env-source"><div class="proxy-env-source-head"><strong class="mono">{{ file.path || '--' }}</strong><span class="proxy-env-status" :class="file.variables?.length ? 'active' : ''">{{ !file.exists ? '不存在' : file.readable === false ? '不可读' : file.variables?.length ? '已检测到代理设置' : '未设置' }}</span></div><div v-if="file.error" class="proxy-env-error">{{ file.error }}</div><div v-else class="proxy-env-vars"><div v-for="variable in file.variables || []" :key="`${variable.key}-${variable.line}`" class="proxy-env-var"><span class="mono proxy-env-key">{{ variable.key }}</span><span class="mono proxy-env-value">{{ variable.value }}</span><span v-if="variable.line" class="proxy-env-line">L{{ variable.line }}</span></div><span v-if="!file.variables?.length" class="proxy-env-empty">未设置代理环境变量</span></div></div></div><div class="dns-autosave-state" :class="envState">{{ envMessage }}</div></div>

          <div v-else-if="category.key === 'core'" class="settings-accordion-panel update-panel" id="core-settings">
            <div class="core-tabs" role="tablist" aria-label="Mihomo Core 设置">
              <button v-for="(tab, index) in coreTabs" :id="`core-tab-${tab.key}`" :key="tab.key" type="button" role="tab" :aria-selected="coreTab === tab.key" :aria-controls="`core-panel-${tab.key}`" :tabindex="coreTab === tab.key ? 0 : -1" @click="coreTab = tab.key" @keydown="navigateCoreTab($event, index)">{{ tab.label }}</button>
            </div>
            <div v-show="coreTab === 'manage'" id="core-panel-manage" role="tabpanel" aria-labelledby="core-tab-manage">
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
                <button v-if="system.canRestartService !== true" class="ghost" :disabled="Boolean(busy) || system.mode !== 'managed' || system.available === false" :title="system.mode === 'external' ? '外部 Core 请通过原有服务启动' : '启动 Manager 托管的 Mihomo Core'" @click="setCoreRunning(true)">{{ busy === 'core-start' ? '正在启动…' : '启动内核' }}</button>
                <button v-else class="ghost" :disabled="Boolean(busy) || system.mode !== 'managed'" title="停止托管 Core，管理页面保持可用" @click="setCoreRunning(false)">{{ busy === 'core-stop' ? '正在停止…' : '停止内核' }}</button>
                <button class="ghost" :disabled="Boolean(busy) || system.mode !== 'managed' || system.canRestartService !== true" :title="system.mode === 'external' ? '外部 Core 请通过原有服务重启' : system.canRestartService !== true ? '当前没有可重启的托管 Core' : '重启 Manager 托管的 Mihomo Core'" @click="restartCore">{{ busy === 'core-restart' ? '正在重启…' : '重启内核' }}</button>
                <button class="ghost" :disabled="Boolean(busy)" @click="checkCoreUpdate">{{ busy === 'core-update' ? '检查中…' : '检查更新' }}</button>
              </div>
            </div>

            <div class="settings-accordion-subsection core-mode-section">
              <div class="core-mode-row">
                <div class="section-head"><div><h2 id="core-mode-label">Core 运行方式</h2><p>切换后保存选择，下次启动时沿用。</p></div></div>
                <div class="core-mode-controls">
                  <select id="core-mode" v-model="selectedCoreMode" aria-labelledby="core-mode-label" :disabled="Boolean(busy)"><option value="managed">Manager 托管</option><option value="external">外部 Mihomo</option></select>
                  <button :disabled="Boolean(busy) || system.available === false" @click="switchCoreMode">{{ busy === 'core-mode' ? '正在切换…' : '应用' }}</button>
                </div>
              </div>
            <p class="hint">托管：由应用管理内核启停。外部：连接本机已有 Mihomo，并停止托管内核。</p>
            <div v-if="coreModeError || system.bootstrap?.error" class="local-warning">{{ coreModeError || system.bootstrap?.error }}</div>
            <PortConflictHelp :managed="system.mode === 'managed'" @updated="load" :error="coreModeError || system.bootstrap?.error || ''" />
            <div class="core-startup-preference">
              <label class="core-check"><input v-model="manager.persistSelections" type="checkbox" @change="saveBehavior(120)"> 重启 Core 后恢复策略组选择</label>
              <p class="hint">恢复“代理节点”页面中仍然有效的手动策略组选择。</p>
              <div class="dns-autosave-state" :class="behaviorState">{{ behaviorMessage }}</div>
            </div>
            </div>
            </div>
            <div v-show="coreTab === 'connection'" id="core-panel-connection" role="tabpanel" aria-labelledby="core-tab-connection">
            <div class="settings-accordion-subsection core-connection-section">
              <div class="core-mode-row">
                <div class="section-head"><div><div class="connection-title"><h2>连接设置</h2><span v-if="system.coreMode !== 'external'" class="auto-detected">自动管理</span></div><p>连接当前 Core 的 Controller API</p></div></div>
                <div class="core-mode-controls">
                  <label v-if="system.coreMode === 'external'" class="core-check"><input v-model="manager.controllerAutoDetect" type="checkbox" :disabled="Boolean(busy)" @change="enableControllerDetection"> 自动检测</label>
                  <button class="ghost" :disabled="Boolean(busy)" @click="testController">{{ busy === 'test' ? '测试中…' : '测试连接' }}</button>
                </div>
              </div>
              <template v-if="system.coreMode === 'external' && manager.controllerAutoDetect === false">
                <div class="form-grid core-connection-fields">
                  <div class="field"><label for="external-controller">Controller 地址</label><input id="external-controller" v-model="manager.controller" placeholder="http://127.0.0.1:9090"></div>
                  <div class="field"><label for="external-secret">Secret</label><input id="external-secret" v-model="externalSecret" type="password" autocomplete="new-password" placeholder="留空保留已有 Secret" :disabled="clearExternalSecret"></div>
                </div>
                <div class="core-connection-footer"><label class="core-check"><input v-model="clearExternalSecret" type="checkbox"> 清除已保存的 Secret</label><button :disabled="Boolean(busy)" @click="saveExternalController()">{{ busy === 'controller' ? '保存中…' : '保存连接设置' }}</button></div>
                <p class="hint">填写 fnOS 主机可访问的地址，保存后再测试连接。</p>
              </template>
              <template v-else>
                <div class="core-connection-summary"><div class="core-controller-summary"><span>Controller</span><strong class="mono">{{ manager.controller || '--' }}</strong><a class="core-port-settings-link" href="#settings?section=network" @click.prevent="openSettingsSection('network')">端口设置</a></div><div><span>Secret</span><div class="core-secret-display"><strong class="mono">{{ manager.hasSecret ? secretVisible ? revealedSecret : '********' : '未设置' }}</strong><button v-if="manager.hasSecret" class="core-secret-toggle" type="button" :disabled="secretLoading" :aria-label="secretVisible ? '隐藏 Secret' : '显示 Secret'" :aria-pressed="secretVisible" :title="secretVisible ? '隐藏 Secret' : '显示 Secret'" @click="toggleSecretVisibility"><svg viewBox="0 0 24 24" aria-hidden="true"><template v-if="secretVisible"><path d="M3 3l18 18" /><path d="M10.6 10.6a2 2 0 0 0 2.8 2.8" /><path d="M9.9 4.2A10.5 10.5 0 0 1 12 4c6.5 0 10 8 10 8a18 18 0 0 1-2.1 3.2" /><path d="M6.6 6.6C3.6 8.5 2 12 2 12s3.5 8 10 8a9.7 9.7 0 0 0 4.1-.9" /></template><template v-else><path d="M2 12s3.5-8 10-8 10 8 10 8-3.5 8-10 8S2 12 2 12Z" /><circle cx="12" cy="12" r="3" /></template></svg></button></div></div></div>
                <p v-if="system.coreMode === 'external'" class="hint">自动读取外部 Core 配置；无法识别时，关闭自动检测并手动填写。</p>
              </template>
            </div>
            <div class="settings-accordion-subsection core-behavior-section">
              <div class="form-grid core-healthcheck-fields">
                <div class="field"><label for="core-healthcheck-url">延迟测试 URL</label><input id="core-healthcheck-url" v-model="manager.healthcheckUrl" @change="saveBehavior"></div>
                <div class="field"><label for="core-healthcheck-timeout">超时（毫秒）</label><input id="core-healthcheck-timeout" v-model.number="manager.healthcheckTimeout" type="number" @change="saveBehavior"></div>
              </div>
              <div class="dns-autosave-state" :class="behaviorState">{{ behaviorMessage }}</div>
            </div>
            </div>
            <div v-show="coreTab === 'geo'" id="core-panel-geo" role="tabpanel" aria-labelledby="core-tab-geo" class="geo-update-section">

              <div class="geo-update-summary">
                <div class="geo-update-title">
                  <h2>GEO 数据</h2>
                  <span v-if="geoMessage" class="geo-operation-state" :class="geoState" role="status" aria-live="polite"><span v-if="geoBusy" class="geo-spinner" /><span>{{ geoMessage }}</span></span>
                </div>
              </div>
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
            </div>
          </div>

          <div v-else-if="category.key === 'behavior'" class="settings-accordion-panel">
<div class="app-icon-settings"><div class="section-head"><div><h2>软件图标</h2></div></div><div v-if="icons.error" class="local-warning">读取图标设置失败：{{ icons.error }}</div><div class="app-icon-picker"><button v-for="icon in icons.options || []" :key="icon.id" type="button" class="app-icon-choice" :class="{ active: icon.id === selectedIcon }" :disabled="icons.ok === false || Boolean(busy)" :title="icon.name || icon.id" @click="chooseIcon(icon.id)"><img :src="icon.preview || `${APP_PREFIX}/icons/${icon.id}_256.png`" alt=""><span class="app-icon-choice-state">{{ icon.id === selectedIcon ? '✓' : '' }}</span></button></div></div>
          </div>

          <div v-else class="settings-accordion-panel update-panel">
            <div class="update-row app-update-row">
              <div class="update-row-copy">
                <div class="app-update-title-line" aria-label="Clash for fnOS 版本信息">
                  <h2>Clash for fnOS</h2>
                  <span class="update-meta">{{ platformLabel(appUpdate.platform) }}</span>
                  <strong class="app-current-version">v{{ String(appUpdate.currentVersion || '--').replace(/^v/, '') }}</strong>
                </div>
              </div>
              <div class="update-row-actions">
                <template v-if="appUpdate.updateAvailable">
                  <span class="app-update-latest-version">
                    <span>最新版本</span>
                    <strong>v{{ latestAppVersion }}</strong>
                  </span>
                  <button :disabled="busy === 'app-update'" @click="openAppUpdate">更新</button>
                </template>
                <button v-else class="ghost" :disabled="busy === 'app-update'" @click="checkAppUpdate">{{ busy === 'app-update' ? '检查中…' : '检查更新' }}</button>
              </div>
            </div>
            <label class="app-update-preference">
              <span class="app-update-preference-copy"><strong>更新提示</strong><small>启动时自动检查，有新版本时显示提示</small></span>
              <span class="switch"><input v-model="manager.notifyAppUpdates" type="checkbox" @change="saveAppUpdatePreference"><span /></span>
            </label>

            <p v-if="appUpdate.directRetry" class="hint">常规请求失败，已通过不使用 HTTP/HTTPS 代理的重试获取更新信息。</p>
          </div>
        </div>
      </div>
    </div></div>
  </AsyncState>
</template>

<style scoped>
.core-mode-section .core-mode-controls button { height:32px; min-height:32px; padding:0 13px; border-radius:8px; font-size:11px; line-height:30px; }
.connection-title { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }

.core-tabs { display: flex; gap: 4px; padding: 8px 20px; border-bottom: 1px solid var(--line); }
.core-tabs button { border: 1px solid var(--line); background: transparent; color: var(--muted); box-shadow: none; border-radius: 7px; padding: 6px 12px; font-size: 12px; line-height: 18px; transition: border-color .16s ease, background .16s ease, color .16s ease; }
.core-tabs button:hover { border-color: color-mix(in srgb, var(--accent) 42%, var(--line)); background: color-mix(in srgb, var(--accent) 6%, transparent); color: var(--text); }
.core-tabs button[aria-selected="true"] { border-color: color-mix(in srgb, var(--accent) 48%, var(--line)); background: color-mix(in srgb, var(--accent) 14%, var(--panel2)); color: var(--accent); }
.core-tabs button:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
#core-panel-connection > .core-connection-section { border-top: 0; }
#core-panel-geo { border-top: 0; }
#core-panel-geo .geo-update-summary { cursor: default; }
@media (max-width: 680px) { .core-tabs { padding: 8px 10px; } .core-tabs button { flex: 1; padding: 7px 6px; white-space: nowrap; } }

.core-connection-section .section-head { margin-bottom: 12px; }
.core-check { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; line-height: 1.5; }
.core-check input { width: 16px; height: 16px; margin: 0; flex: none; accent-color: var(--accent); }
.core-connection-summary { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.core-connection-summary > div { min-height: 46px; display: flex; align-items: center; gap: 12px; min-width: 0; padding: 10px 12px; border: 1px solid var(--line); border-radius: 10px; background: var(--control-bg); }
.core-connection-summary span { font-size: 12px; color: var(--muted, #68748b); }
.core-connection-summary strong { min-width: 0; overflow: hidden; color: var(--text); font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.core-controller-summary strong { flex: 1; }
.core-port-settings-link { flex: none; color: var(--accent); font-size: 11px; font-weight: 650; text-decoration: none; white-space: nowrap; }
.core-port-settings-link:hover { text-decoration: underline; }
#core-panel-connection > .core-behavior-section { border-top: 0; padding-top: 6px; }
.core-connection-footer { display: flex; align-items: center; justify-content: space-between; gap: 12px 24px; flex-wrap: wrap; margin-top: 12px; }
.core-startup-preference { margin-top: 14px; }
.core-startup-preference > .hint { margin: 5px 0 0; }
.core-startup-preference > .dns-autosave-state { margin-top: 4px; }
.core-healthcheck-fields { grid-template-columns: minmax(0, 1fr) 150px; }
.core-connection-section .hint, .core-mode-section .hint { margin: 10px 0 0; }
@media (max-width: 680px) {
  .core-connection-summary, .core-healthcheck-fields { grid-template-columns: minmax(0, 1fr); }
}
</style>
