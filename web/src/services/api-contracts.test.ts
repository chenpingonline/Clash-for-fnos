import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = ['DashboardPage.vue', 'ProxiesPage.vue', 'ProfilesPage.vue', 'ConfigPage.vue', 'RulesPage.vue', 'ConnectionsPage.vue', 'LogsPage.vue', 'SettingsPage.vue']
  .map(name => readFileSync(resolve(__dirname, `../pages/${name}`), 'utf8'))
  .concat(readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8'), readFileSync(resolve(__dirname, '../composables/useCoreHealth.ts'), 'utf8')).join('\n')

describe('backend API compatibility', () => {
  it.each([
    '/api/status', '/api/traffic-history', '/api/proxies', '/api/profiles', '/api/exit-location', '/api/config/effective', '/api/rules', '/api/rule-providers', '/api/connections', '/api/logs/history', '/api/settings', '/api/network/settings', '/api/network/tun', '/api/network/tun/status', '/api/system/proxy-environment', '/api/core/check-update',
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

  it('locks the TUN controls while a switch is pending', () => {
    const dashboardControl = readFileSync(resolve(__dirname, '../components/SystemProxyCard.vue'), 'utf8')
    const settings = readFileSync(resolve(__dirname, '../pages/SettingsPage.vue'), 'utf8')
    expect(dashboardControl).toContain(':disabled="tunLoading || tunSaving')
    expect(dashboardControl).toContain(':checked="tunDisplayedEnabled"')
    expect(dashboardControl).toContain(':aria-busy="tunSaving"')
    expect(dashboardControl).toContain('class="dashboard-tun-progress"')
    expect(dashboardControl).toContain("setTimeout(pollTunProgress, 120)")
    expect(settings).toContain(':disabled="tunSwitching || netState')
  })
})
