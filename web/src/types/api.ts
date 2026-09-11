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
  delivery?: 'online' | 'bundled' | 'external'
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
  canRestartService?: boolean
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
  'redir-port'?: number
  'tproxy-port'?: number
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

export interface ProxyProvider {
  type?: string
  vehicleType?: string
  updatedAt?: string
  proxies?: ProxyNode[]
}

export interface ProxyProvidersResponse { providers?: Record<string, ProxyProvider> }

export interface DelayResponse { delay?: number }

export interface TrafficSample {
  time: number
  up: number
  down: number
}

export interface TrafficHistoryResponse {
  samples?: TrafficSample[]
}

export interface ProfilesResponse { items?: ProfileItem[] }

export interface ExitLocationResponse {
  ip?: string
  country?: string
  countryCode?: string
  region?: string
  city?: string
  timezone?: string
  utcOffset?: string
  via?: 'mixed' | 'http' | 'socks5' | 'direct' | string
  updatedAt?: number
}

export interface ProfileJob {
  jobId?: string
  profileId?: string
  operation?: 'update' | 'update-activate' | 'activate'
  state?: 'pending' | 'running' | 'done' | 'failed'
  stage?: string
  message?: string
  error?: string
  result?: { target?: string; unchanged?: boolean; durationMs?: number; stages?: Record<string, number>; lastDownload?: ProfileItem['lastDownload'] }
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
  unchanged?: boolean
  conditional?: boolean
  attempts?: Array<{ label?: string; skipped?: boolean; status?: number; error?: string }>
}

export interface LocalProcess {
  pid?: number
  exe?: string
  args?: string[]
  containerized?: boolean
}

export interface LocalRuntime {
  mode?: 'managed' | 'external' | 'auto' | string
  running?: boolean
  pid?: number
  binaryPath?: string
  configPath?: string
  binaryVersion?: string
  message?: string
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
  runtime?: LocalRuntime
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

export interface RuleItem {
  type?: string
  index?: number
  payload?: string
  proxy?: string
  size?: number
}

export interface RulesResponse {
  rules?: RuleItem[]
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
  routeExcludeAddress?: string[]
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
  offline?: boolean
  error?: string
  settings?: NetworkSetting | null
  tunCapability?: { supported?: boolean; tunDevice?: boolean; permission?: boolean; reason?: string; message?: string }
  activation?: string
  proxyEnvironment?: ProxyEnvironmentResponse
}
export interface ManagerSettings {
  controllerAutoDetect?: boolean
  controller?: string
  hasSecret?: boolean
  healthcheckUrl?: string
  healthcheckTimeout?: number
  persistSelections?: boolean
  notifyAppUpdates: boolean
}
export interface AppUpdateInfo {
  directRetry?: boolean
  appName?: string; currentVersion?: string; platform?: string; sourceConfigured?: boolean; releaseRepo?: string; updateAvailable?: boolean; error?: string
  latest?: { tag?: string; name?: string; publishedAt?: string; htmlUrl?: string; asset?: { name?: string; url?: string; size?: number } | null }
}

export interface GeoAsset {
  key: 'geoip' | 'geosite' | 'mmdb' | 'asn' | string
  label?: string
  fileName?: string
  present?: boolean
  size?: number
  updatedAt?: number
  source?: string
  status?: 'ready' | 'missing' | string
}

export interface GeoStatus {
  ok?: boolean
  mode?: 'managed' | 'external' | string
  readOnly?: boolean
  canUpdate?: boolean
  message?: string
  configPath?: string
  homeDir?: string
  settings?: { autoUpdate?: boolean; updateInterval?: number }
  assets?: GeoAsset[]
  error?: string
}
