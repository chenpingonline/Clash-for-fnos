export type CoreMode = 'auto' | 'external' | 'managed'
export type RuntimeMode = 'rule' | 'global' | 'direct'

export interface ApiErrorPayload {
  error?: string
  message?: string
}

export interface CoreBootstrap {
  state?: string
  mode?: string | null
  coreMode?: CoreMode
  delivery?: 'online' | 'bundled'
  message?: string
  error?: string
  progress?: number
}

export interface CoreAvailabilityItem {
  installed?: boolean
  running?: boolean
  binaryPath?: string | null
  configPath?: string | null
}

export interface SystemStatus {
  available?: boolean
  privileged?: boolean
  mode?: 'external' | 'managed'
  coreMode?: CoreMode
  currentVersion?: string
  controllerVersion?: { version?: string }
  binaryPath?: string
  configPath?: string
  managedMixedPort?: number
  bootstrap?: CoreBootstrap
  coreAvailability?: {
    external?: CoreAvailabilityItem
    managed?: CoreAvailabilityItem
  }
  error?: string
}

export interface RuntimeConfig {
  mode?: RuntimeMode
  'mixed-port'?: number
  port?: number
  'socks-port'?: number
  'allow-lan'?: boolean
  ipv6?: boolean
  tun?: { enable?: boolean }
}

export interface StatusResponse {
  online?: boolean
  version?: { version?: string }
  configs?: RuntimeConfig
  connections?: {
    count?: number
    uploadTotal?: number
    downloadTotal?: number
    memory?: number
  }
}

export interface CoreHealth extends StatusResponse {
  online: boolean
  system?: SystemStatus
  bootstrap?: CoreBootstrap
  error?: string
}

export interface ProxyEnvironmentManagement {
  active?: boolean
  suspendedReason?: string | null
  mixedPort?: number
  settings?: {
    enabled?: boolean
    followMixedPort?: boolean
    port?: number
    noProxy?: string
    targets?: { environment?: boolean; profile?: boolean; bashrc?: boolean }
  }
}

export interface ProxyVariable {
  key?: string
  value?: string
  line?: number
}

export interface ProxyEnvironmentFile {
  path?: string
  exists?: boolean
  readable?: boolean
  variables?: ProxyVariable[]
  error?: string
}

export interface ProxyEnvironmentResponse {
  ok?: boolean
  error?: string
  files?: ProxyEnvironmentFile[]
  managerEnvironment?: ProxyVariable[]
  helperEnvironment?: ProxyVariable[]
  mihomoEnvironment?: { pid?: number | null; variables?: ProxyVariable[] }
  management?: ProxyEnvironmentManagement | null
  operation?: { changed?: string[] }
}

export interface ProxyNode {
  type?: string
  now?: string
  all?: string[]
  history?: Array<{ delay?: number }>
}

export interface ProxiesResponse {
  proxies?: Record<string, ProxyNode>
  groupOrder?: string[]
  groupOrderSource?: 'startup' | 'managed' | string
}

export interface DelayResponse { delay?: number }

export interface ProfilesResponse { items?: ProfileItem[] }

export interface ProfileJob {
  jobId?: string
  state?: 'pending' | 'running' | 'done' | 'failed'
  message?: string
  error?: string
  result?: { target?: string }
}

export interface ProfileItem {
  id: string
  name: string
  type: 'remote' | 'local' | string
  current?: boolean
  url?: string
  sourcePath?: string
  updatedAt?: number
  lastError?: string
  intervalMinutes?: number
  autoUpdate?: boolean
  autoApply?: boolean
  subscriptionInfo?: unknown
  lastDownload?: DownloadInfo
}

export interface DownloadInfo {
  method?: string
  status?: number
  label?: string
  durationMs?: number
  attempts?: Array<{ label?: string; skipped?: boolean; status?: number; error?: string }>
}

export interface LocalProcess {
  pid?: number
  exe?: string
  args?: string[]
  containerized?: boolean
}

export interface LocalConfigCandidate {
  token?: string
  path?: string
  source?: string
  namespace?: string
  readable?: boolean
  permissionDenied?: boolean
  exists?: boolean
  size?: number
  mtime?: number
}

export interface LocalDiscoveryResponse {
  error?: string
  authorizedPaths?: string[]
  processes?: LocalProcess[]
  candidates?: LocalConfigCandidate[]
}

export interface ConnectionItem {
  id: string
  rule?: string
  rulePayload?: string
  chains?: string[]
  upload?: number
  download?: number
  metadata?: {
    host?: string
    destinationIP?: string
    destinationPort?: string | number
    process?: string
  }
}

export interface ConnectionsResponse {
  connections?: ConnectionItem[]
  uploadTotal?: number
  downloadTotal?: number
}

export interface RuleProvider {
  behavior?: string
  vehicleType?: string
  type?: string
  ruleCount?: number
  updatedAt?: string
}

export interface LogItem {
  time?: string | number
  level?: string
  type?: string
  message?: string
  payload?: string
}

export interface AppIconOption {
  id: string
  name?: string
  description?: string
  preview?: string
}

export interface AppIconsResponse {
  ok?: boolean
  selected?: string
  defaultId?: string
  requiresWindowReload?: boolean
  error?: string
  options?: AppIconOption[]
}

export interface PortSetting { enabled?: boolean; port?: number }
export interface TunSetting {
  enabled?: boolean
  stack?: 'mixed' | 'system' | 'gvisor'
  mtu?: number
  autoRoute?: boolean
  autoRedirect?: boolean
  autoDetectInterface?: boolean
  dnsHijack?: boolean
  strictRoute?: boolean
}
export interface DnsMapping { matcher: string; servers: string[] }
export interface HostMapping { host: string; values: string[] }
export interface DnsSetting {
  enable?: boolean; listen?: string; enhancedMode?: string; fakeIpRange?: string; fakeIpRange6?: string
  fakeIpFilterMode?: string; ipv6?: boolean; preferH3?: boolean; respectRules?: boolean; useHosts?: boolean
  useSystemHosts?: boolean; directNameserverFollowPolicy?: boolean; defaultNameserver?: string[]; nameserver?: string[]
  fallback?: string[]; proxyServerNameserver?: string[]; directNameserver?: string[]; fakeIpFilter?: string[]
  nameserverPolicy?: DnsMapping[]; fallbackGeoip?: boolean; fallbackGeoipCode?: string; fallbackIpCidr?: string[]
  fallbackDomain?: string[]; hosts?: HostMapping[]
}
export interface NetworkSetting {
  controller?: { host?: string; port?: number }
  mixed?: PortSetting; socks?: PortSetting; http?: PortSetting; redir?: PortSetting; tproxy?: PortSetting
  allowLan?: boolean; dnsEnabled?: boolean; dnsOverrideEnabled?: boolean; dns?: DnsSetting
  core?: { ipv6?: boolean; unifiedDelay?: boolean }; tun?: TunSetting
}
export interface NetworkSettingsResponse {
  error?: string
  settings?: NetworkSetting | null
  tunCapability?: { supported?: boolean; tunDevice?: boolean; permission?: boolean; reason?: string; message?: string }
}
export interface ManagerSettings {
  controller?: string
  hasSecret?: boolean
  healthcheckUrl?: string
  healthcheckTimeout?: number
  persistSelections?: boolean
  applyManagedConfigOnStart?: boolean
}
export interface AppUpdateInfo {
  appName?: string; currentVersion?: string; platform?: string; sourceConfigured?: boolean; releaseRepo?: string; error?: string
}
