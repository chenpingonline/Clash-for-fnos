import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = ['DashboardPage.vue', 'ProxiesPage.vue', 'ProfilesPage.vue', 'ConfigPage.vue', 'RulesPage.vue', 'ConnectionsPage.vue', 'LogsPage.vue', 'SettingsPage.vue']
  .map(name => readFileSync(resolve(__dirname, `../pages/${name}`), 'utf8')).concat(readFileSync(resolve(__dirname, '../composables/useCoreHealth.ts'), 'utf8')).join('\n')

describe('backend API compatibility', () => {
  it.each([
    '/api/status', '/api/proxies', '/api/profiles', '/api/config/effective', '/api/rule-providers', '/api/connections', '/api/logs/history', '/api/settings', '/api/network/settings', '/api/system/proxy-environment', '/api/core/check-update',
  ])('keeps the existing %s endpoint', endpoint => expect(source).toContain(endpoint))

  it('keeps hash navigation compatible with the fnOS iframe entry', () => {
    const app = readFileSync(resolve(__dirname, '../App.vue'), 'utf8')
    expect(app).toContain(':href="`#${page.name}`"')
  })
})
