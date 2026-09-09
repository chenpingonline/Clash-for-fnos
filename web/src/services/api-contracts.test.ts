import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = ['DashboardPage.vue', 'ProxiesPage.vue', 'ProfilesPage.vue', 'ConfigPage.vue', 'RulesPage.vue', 'ConnectionsPage.vue', 'LogsPage.vue', 'SettingsPage.vue']
  .map(name => readFileSync(resolve(__dirname, `../pages/${name}`), 'utf8'))
  .concat(readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8'), readFileSync(resolve(__dirname, '../composables/useCoreHealth.ts'), 'utf8')).join('\n')

describe('backend API compatibility', () => {
  it.each([
    '/api/status', '/api/traffic-history', '/api/proxies', '/api/providers', '/api/profiles', '/api/exit-location', '/api/config/effective', '/api/rules', '/api/rule-providers', '/api/connections', '/api/logs/history', '/api/settings', '/api/network/settings', '/api/network/tun', '/api/network/tun/status', '/api/geo/status', '/api/geo/settings', '/api/geo/update', '/api/geo/download', '/api/system/proxy-environment', '/api/core/check-update',
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
    expect(dashboard).toContain('defineExpose({ refreshPage })')
    expect(dashboard).toContain('void loadDashboardDetails()')
  })

  it('checks for an app update once on startup and marks the settings navigation item', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    expect(app).toContain("api<AppUpdateInfo>('/api/app/check-update'")
    expect(app).toContain('void checkAppUpdateSilently()')
    expect(app).toContain("page.name === 'settings' && appUpdateAvailable")
    expect(app).toContain('有新版本')
  })

  it('locks the TUN controls while a switch is pending', () => {
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(dashboardControl).toContain(':disabled="tunLoading || tunSaving')
    expect(dashboardControl).toContain(':checked="tunDisplayedEnabled"')
    expect(dashboardControl).toContain(':aria-busy="tunSaving"')
    expect(dashboardControl).toContain('class="dashboard-tun-progress"')
    expect(dashboardControl).toContain('class="dashboard-runtime-head"')
    expect(dashboardControl).toContain('<a class="dashboard-settings-link" href="#settings?section=tun">打开详细设置</a>')
    expect(dashboardControl).not.toContain('<a v-else class="dashboard-settings-link"')
    expect(dashboardControl).toContain("setTimeout(pollTunProgress, 120)")
    expect(settings).toContain(':disabled="tunSwitching || netState')
    expect(settings).toContain("api<TunOperationStatus>('/api/network/tun/status')")
    expect(settings).toContain('class="dashboard-tun-progress settings-tun-progress"')
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

  it('keeps GEO details collapsed by default and places transient progress in the title', () => {
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    const summary = settings.indexOf('class="geo-update-summary"')
    const status = settings.indexOf('class="geo-operation-state"')
    const body = settings.indexOf('class="geo-update-body"')
    expect(settings).toContain('<details class="geo-update-section">')
    expect(settings).not.toContain('<details class="geo-update-section" open')
    expect(status).toBeGreaterThan(summary)
    expect(status).toBeLessThan(body)
    expect(settings).toContain("setGeoOperation('success'")
    expect(settings).toContain("setGeoOperation('error'")
    expect(settings).toContain('}, 3000)')
  })
})
