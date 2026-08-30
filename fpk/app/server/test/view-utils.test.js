'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

test('frontend view helpers escape user-controlled content and format values', async () => {
  const { escapeHtml, formatBytes, formatRate, normalizeSubscriptionInfo } = await import('../public/lib/view-utils.js');
  assert.equal(escapeHtml('<img src=x onerror="alert(1)">'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
  assert.equal(formatBytes(1536), '1.5 KB');
  assert.equal(formatRate(1024), '1.0 KB/s');
  assert.deepEqual(normalizeSubscriptionInfo({ Upload: '10', Download: 20, Total: 100 }), { upload: 10, download: 20, total: 100, expire: 0 });
});

test('DNS settings use debounced auto-save without a manual save button', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  assert.doesNotMatch(source, /id=["']saveDnsSettings["']/);
  assert.match(source, /queueSettingsAutoSave\('dns'/);
  assert.match(source, /id="dnsAutoSaveState"/);
  assert.match(source, /autoSaveDns\(1000\)/);
});

test('DNS override defaults off while the DNS template defaults enabled', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  const server = fs.readFileSync(path.join(__dirname, '../server.js'), 'utf8');
  assert.match(server, /dnsOverrideEnabled: false/);
  assert.match(server, /dnsOverrideSettings: defaultDnsOverrideSettings\(\)/);
  assert.match(source, /id="dnsOverrideEnabled"/);
  assert.match(source, /optionSwitch\('dnsEnable','启用 DNS'/);
  assert.match(source, /dnsOverrideEnabled:qs\('#dnsOverrideEnabled'\)\.checked,dns:dnsPayload\(\)/);
});

test('sidebar uses consistent semantic SVG icons instead of text symbols', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  assert.match(source, /const NAV_ICON_SHAPES = \{/);
  assert.match(source, /dashboard: \[navIcon\('dashboard'\)/);
  assert.match(source, /proxies: \[navIcon\('proxies'\)/);
  assert.match(source, /profiles: \[navIcon\('profiles'\)/);
  assert.match(source, /config: \[navIcon\('config'\)/);
  assert.match(source, /rules: \[navIcon\('rules'\)/);
  assert.match(source, /connections: \[navIcon\('connections'\)/);
  assert.match(source, /logs: \[navIcon\('logs'\)/);
  assert.match(source, /settings: \[navIcon\('settings'\)/);
  assert.doesNotMatch(source, /dashboard: \['◫'/);
});

test('global IPv6 and unified delay are classified under network settings', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  const networkBlock = source.slice(source.indexOf('const networkBlock='), source.indexOf('const tunBlock='));
  const behaviorBlock = source.slice(source.indexOf('const behaviorBlock='), source.indexOf('const proxyMgmt='));
  const networkBindings = source.slice(source.indexOf("if(currentSettingsView==='network')"), source.indexOf("if(currentSettingsView==='tun')"));
  assert.match(networkBlock, /optionSwitch\('coreIpv6','全局 IPv6'/);
  assert.match(networkBlock, /optionSwitch\('coreUnifiedDelay','统一延迟'/);
  assert.doesNotMatch(behaviorBlock, /coreIpv6|coreUnifiedDelay/);
  assert.match(networkBindings, /core:\{ipv6:qs\('#coreIpv6'\)\.checked,unifiedDelay:qs\('#coreUnifiedDelay'\)\.checked\}/);
});

test('fallback GeoIP layout keeps field spacing without redundant default hint', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  assert.match(source, /class="dns-field-grid dns-fallback-fields"/);
  assert.doesNotMatch(source, /Clash Verge 默认 CN/);
});

test('subscription checkboxes use an aligned label layout', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  assert.match(source, /class="field profile-checkbox-field"/);
  assert.match(source, /class="profile-checkbox-label"/);
  assert.doesNotMatch(source, /type="checkbox"[^>]*style="width:auto"> 自动更新/);
});

test('DNS sticky save bar is clipped to the accordion bottom corners', () => {
  const styles = fs.readFileSync(path.join(__dirname, '../public/styles.css'), 'utf8');
  assert.match(styles, /\.dns-settings-panel\{[^}]*overflow:clip[^}]*border-radius:0 0 13px 13px/);
  assert.match(styles, /\.dns-savebar\{[^}]*border-radius:0 0 13px 13px/);
});

test('disabled TUN status uses a neutral color instead of the success color', () => {
  const source = fs.readFileSync(path.join(__dirname, '../public/app.js'), 'utf8');
  const styles = fs.readFileSync(path.join(__dirname, '../public/styles.css'), 'utf8');
  assert.match(source, /tun\.enabled\?'good-text':'muted-text'/);
  assert.match(styles, /\.muted-text\{color:var\(--muted\)\}/);
  assert.doesNotMatch(source, /tunSupported\?'good-text':'warn-text'\}\">\$\{tun\.enabled\?'已开启':'已关闭'/);
});
