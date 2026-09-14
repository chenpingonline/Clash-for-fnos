import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = ['DashboardPage.vue', 'ProxiesPage.vue', 'ProfilesPage.vue', 'ConfigPage.vue', 'RulesPage.vue', 'ConnectionsPage.vue', 'LogsPage.vue', 'SettingsPage.vue']
  .map(name => readFileSync(resolve(__dirname, `../pages/${name}`), 'utf8'))
  .concat(readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8'), readFileSync(resolve(__dirname, '../composables/useCoreHealth.ts'), 'utf8')).join('\n')

describe('backend API compatibility', () => {
  it.each([
    '/api/status', '/api/traffic-history', '/api/proxies', '/api/providers', '/api/profiles', '/api/exit-location', '/api/config/effective', '/api/rules', '/api/rule-providers', '/api/connections', '/api/logs/history', '/api/settings', '/api/settings/secret', '/api/network/settings', '/api/network/tun', '/api/network/tun/status', '/api/geo/status', '/api/geo/settings', '/api/geo/update', '/api/geo/download', '/api/system/proxy-environment', '/api/core/restart', '/api/core/check-update',
  ])('keeps the existing %s endpoint', endpoint => expect(source).toContain(endpoint))

  it('keeps hash navigation compatible with the fnOS iframe entry', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    expect(app).toContain(':href="`#${page.name}`"')
    expect(app).toContain("history.replaceState(history.state, '', `#${page}`)")
    expect(app).toContain("window.addEventListener('keydown', blockBrowserHistoryShortcut, true)")
  })

  it('refreshes the dashboard in place instead of remounting it', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    const dashboard = readFileSync(resolve(__dirname, '../pages/DashboardPage.vue'), 'utf8')
    expect(app).toContain('@click="refreshActivePage"')
    expect(app).toContain('activePageRef.value?.refreshPage')
    expect(app).toContain("pageRefreshing ? '刷新中…' : '刷新'")
    expect(dashboard).toContain('defineExpose({ refreshPage })')
    expect(dashboard).toContain('void loadDashboardDetails(true)')
    expect(dashboard).toContain('await refreshRuntime(true)')
    expect(dashboard).toContain('if (refreshTraffic) startDashboardStream()')
    expect(dashboard).toContain('...(refreshTraffic ? [loadTrafficHistory()] : [])')
    expect(dashboard).toContain('runtimeControl.value?.refreshState()')
    expect(dashboard).toContain('ref="runtimeControl"')
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    expect(dashboardControl).toContain('defineExpose({ refreshState: loadTun })')
    expect(dashboardControl).toContain('if (request !== tunLoadRequest) return')
  })

  it('advances the traffic chart for repeated zero-rate stream samples', () => {
    const dashboard = readFileSync(resolve(__dirname, '../pages/DashboardPage.vue'), 'utf8')
    const chart = readFileSync(resolve(__dirname, '../components/TrafficChart.vue'), 'utf8')
    expect(dashboard).toContain('trafficSampleTime.value = Date.now()')
    expect(dashboard).toContain(':sample-time="trafficSampleTime"')
    expect(chart).toContain('props.sampleTime')
    expect(chart).toContain('time: Number(sampleTime || Date.now())')
  })

  it('offers an explicit Core download flow for the all package', () => {
    const dashboard = readFileSync(resolve(__dirname, '../pages/DashboardPage.vue'), 'utf8')
    expect(dashboard).toContain("status.value?.bootstrap?.state === 'download-required'")
    expect(dashboard).toContain("openStatusStream<CoreBootstrap>('/api/core/bootstrap/status'")
    expect(dashboard).toContain("api<CoreDownloadInfo>('/api/core/download-info')")
    expect(dashboard).toContain("api<{ version?: string }>('/api/core/manual-install'")
    expect(dashboard).toContain("api<CoreBootstrap>('/api/core/bootstrap/cancel'")
    expect(dashboard).toContain("'停止下载'")
    expect(dashboard).toContain("'checking', 'downloading', 'canceling', 'installing', 'starting'")
    expect(dashboard).toContain('type="file" accept=".gz,application/gzip,application/x-gzip"')
    expect(dashboard).toContain("status.bootstrap?.state === 'downloading'")
    expect(dashboard).toContain("state === 'downloading'")
    expect(dashboard).toContain("return downloadAction.value ? '下载 Core' : '重新检测'")
    expect(dashboard).toContain('安装完成后自动启动和检测')
  })

  it('honors the update-notification preference and places notices in the requested status areas', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    const notice = readFileSync(resolve(__dirname, '../composables/useAppUpdateNotice.ts'), 'utf8')
    expect(notice).toContain("api<AppUpdateInfo>('/api/app/check-update'")
    expect(notice).toContain('settings.notifyAppUpdates !== false')
    expect(app).toContain('void checkAppUpdateSilently()')
    expect(app).toContain('class="sidebar-update-notice"')
    expect(settings).toContain("category.key === 'update' && appUpdateAvailable")
    expect(settings).toContain('v-model="manager.notifyAppUpdates"')
    expect(settings).toContain('saveAppUpdatePreference')
    expect(app).not.toContain('nav-update-indicator')
  })

  it('locks the TUN controls while a switch is pending', () => {
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(dashboardControl).toContain(':disabled="tunLoading || tunSaving')
    expect(dashboardControl).toContain(':checked="tunDisplayedEnabled"')
    expect(dashboardControl).toContain(':aria-busy="tunSaving"')
    expect(dashboardControl).toContain('class="dashboard-tun-progress"')
    expect(dashboardControl).toContain('class="dashboard-runtime-head"')
    expect(dashboardControl).toContain('<a class="dashboard-section-link" href="#settings"><span class="dashboard-section-title">运行控制</span><span class="dashboard-title-arrow"')
    expect(dashboardControl).toContain('<DashboardSettingsButton label="虚拟网卡(TUN)设置"')
    expect(dashboardControl).toContain('<TunSettingsModal')
    expect(dashboardControl).not.toContain('href="#settings?section=tun">详细设置</a>')
    expect(dashboardControl).toContain("openStatusStream<TunOperationStatus>('/api/network/tun/status'")
    expect(settings).toContain(':disabled="tunSwitching || netState')
    expect(settings).toContain("openStatusStream<TunOperationStatus>('/api/network/tun/status'")
    expect(settings).toContain('class="dashboard-tun-progress settings-tun-progress"')
  })

  it('opens dashboard system proxy settings and saves the supported environment options', () => {
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    const proxySettings = readFileSync(resolve(__dirname, '../components/SystemProxySettingsModal.vue'), 'utf8')
    expect(dashboardControl).toContain('<DashboardSettingsButton label="系统代理设置"')
    expect(dashboardControl).toContain('<SystemProxySettingsModal')
    expect(dashboardControl).toContain(':initial-management="management"')
    expect(proxySettings).toContain("api<ProxyEnvironmentResponse>('/api/system/proxy-environment'")
    expect(proxySettings).toContain('loading.value = !management.value')
    expect(proxySettings).toContain('if (props.initialManagement) applyManagement(props.initialManagement)')
    expect(proxySettings).toContain('followMixedPort: form.followMixedPort')
    expect(proxySettings).toContain('noProxy: bypassItems.value.join(\',\')')
    expect(proxySettings).toContain('targets: { ...form.targets }')
    expect(proxySettings).toContain('<template #header>')
    expect(proxySettings.indexOf('class="settings-modal-header-actions"')).toBeLessThan(proxySettings.indexOf('class="system-proxy-modal-scroll"'))
    expect(proxySettings).not.toContain('system-proxy-modal-actions')
    expect(proxySettings).not.toContain('<label>代理主机</label>')
    expect(proxySettings).not.toContain('<label>代理端口</label>')
  })

  it('uses the tag editor for proxy bypass settings and removes the duplicate proxy address field', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(settings).toContain('<label>代理绕过设置</label>')
    expect(settings).toContain('class="system-proxy-bypass-tag"')
    expect(settings).toContain('@keydown="proxyBypassKeydown"')
    expect(settings).toContain('updateProxyBypass')
    expect(settings).not.toContain('<label>代理地址</label>')
    expect(settings).not.toContain('<label>NO_PROXY</label>')
  })

  it('opens dashboard TUN settings and saves the complete TUN configuration transactionally', () => {
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    const tunSettings = readFileSync(resolve(__dirname, '../components/TunSettingsModal.vue'), 'utf8')
    expect(tunSettings).toContain("api<NetworkSettingsResponse>('/api/network/settings')")
    expect(tunSettings).toContain("'/api/network/settings/status'")
    expect(tunSettings).toContain("jsonRequest('PUT', { tun:")
    expect(tunSettings).toContain('routeExcludeAddress: [...form.routeExcludeAddress]')
    expect(tunSettings).toContain('title="Auto Redirect"')
    expect(tunSettings).toContain('title="DNS 劫持"')
    expect(tunSettings).toContain('<template #header>')
    expect(tunSettings.indexOf('class="settings-modal-header-actions"')).toBeLessThan(tunSettings.indexOf('class="tun-settings-modal-scroll"'))
    expect(tunSettings).not.toContain('tun-settings-modal-actions')
    expect(dashboardControl).toContain('if (result.tunCapability) tunCapability.value = result.tunCapability')
    expect(tunSettings).toContain('if (result.tunCapability) capability.value = result.tunCapability')
    expect(tunSettings).not.toContain('capability.value = result.tunCapability || { supported: false }')
  })

  it('uses compact dashboard title links for related pages', () => {
    const dashboard = readFileSync(resolve(__dirname, '../pages/DashboardPage.vue'), 'utf8')
    expect(dashboard).toContain('class="dashboard-section-link" href="#proxies"><span class="dashboard-section-title">当前连接</span><span class="dashboard-title-arrow"')
    expect(dashboard).toContain('class="dashboard-section-link" href="#profiles"><span class="dashboard-section-title">')
    expect(dashboard).not.toContain('打开代理节点')
  })

  it('keeps profile job progress in the fixed title line', () => {
    const profiles = readFileSync(resolve(__dirname, '../pages/ProfilesPage.vue'), 'utf8')
    expect(profiles).toContain('class="profile-title-line"')
    expect(profiles.indexOf('class="profile-operation-status"')).toBeLessThan(profiles.indexOf('class="profile-meta"'))
  })

  it('keeps dashboard subscription progress inside the heading row', () => {
    const dashboard = readFileSync(resolve(__dirname, '../pages/DashboardPage.vue'), 'utf8')
    const headingStart = dashboard.indexOf('<div class="dashboard-subscription-heading-main">')
    const headingEnd = dashboard.indexOf('</div>', dashboard.indexOf('</div>', headingStart) + 6)
    const progress = dashboard.indexOf('class="dashboard-subscription-progress"')
    expect(headingStart).toBeGreaterThan(-1)
    expect(progress).toBeGreaterThan(headingStart)
    expect(progress).toBeLessThan(headingEnd)
  })

  it('places the effective rule count in the page title and starts the card with the rule table', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    const rules = readFileSync(resolve(__dirname, '../pages/RulesPage.vue'), 'utf8')
    expect(app).toContain('id="page-title-meta"')
    expect(rules).toContain('to="#page-title-meta"')
    expect(rules).toContain('class="rule-count"')
    expect(rules).not.toContain('class="rules-toolbar"')
    expect(rules).not.toContain('<h2>生效规则</h2>')
  })

  it('shows GEO in its own tab and places transient progress in the title', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    const summary = settings.indexOf('class="geo-update-summary"')
    const status = settings.indexOf('class="geo-operation-state"')
    const body = settings.indexOf('class="geo-update-body"')
    expect(settings).toContain('id="core-panel-geo" role="tabpanel"')
    expect(settings).not.toContain('<details class="geo-update-section"')
    expect(status).toBeGreaterThan(summary)
    expect(status).toBeLessThan(body)
    expect(settings).toContain("setGeoOperation('success'")
    expect(settings).toContain("setGeoOperation('error'")
    expect(settings).toContain('}, 3000)')
  })

  it('presents DNS categories as keyboard-accessible tabs', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(settings).toContain('class="dns-tabs" role="tablist"')
    expect(settings).toContain('role="tabpanel" aria-labelledby="dns-tab-basic"')
    expect(settings).toContain('@keydown="navigateDnsTab($event, index)"')
    expect(settings).not.toContain('<details class="dns-group"')
  })

  it('only enables the Core restart action for a running managed Core', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(settings).toContain("system.value.mode !== 'managed' || system.value.canRestartService !== true")
    expect(settings).toContain("api('/api/core/restart', { method: 'POST' })")
    expect(settings).toContain("system.mode !== 'managed' || system.canRestartService !== true")
    expect(settings).toContain('外部 Core 请通过原有服务重启')
    expect(settings).toContain('正在重启…')
  })

  it('updates the displayed Controller after a network port save', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(settings).toContain('if (result.controller) manager.controller = result.controller')
  })
})
