'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const fsp = fs.promises;
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const env = require('../lib/proxy-environment');

async function fixture(t) {
  const dir = await fsp.mkdtemp(path.join(os.tmpdir(), 'clash-proxy-test-'));
  t.after(() => fsp.rm(dir, { recursive: true, force: true }));
  const targets = ['environment', 'profile', 'bashrc'].map(key => ({ key, path: path.join(dir, key), shell: key !== 'environment' }));
  const original = 'HTTP_PROXY="http://existing:8080"\n# preserve  \n\n\n';
  for (const target of targets) await fsp.writeFile(target.path, original);
  const settingsFile = path.join(dir, 'proxy-environment.json');
  await fsp.writeFile(settingsFile, JSON.stringify(env.normalizeProxyEnvSettings({ enabled: false })));
  const io = Object.create(fsp);
  const source = fs.readFileSync(require.resolve('../privileged-helper.js'), 'utf8');
  const context = vm.createContext({
    ...env, fsp: io, fs, path, process, PROXY_ENV_TARGETS: targets,
    ETC_DIR: dir, PROXY_ENV_BACKUP_DIR: path.join(dir, 'backups'), PROXY_ENV_SETTINGS_FILE: settingsFile,
    safeStamp: () => String(Date.now()),
    networkStatus: async () => ({ settings: { mixed: { enabled: true, port: 7890 } } }),
    readProxyEnvFile: async () => ({}), detectPrimaryMihomo: async () => null, processProxyEnvironment: async () => ({}),
  });
  vm.runInContext(source.slice(source.indexOf('async function readProxyEnvSettings()'), source.indexOf('\nfunction optionValue')), context);
  return { context, io, targets, original, settingsFile };
}

test('disable removes only managed blocks and automatic sync cannot re-enable them', async t => {
  const { context, targets, original, settingsFile } = await fixture(t);
  await context.updateSystemProxyEnvironment({ enabled: true });
  assert.match(await fsp.readFile(targets[0].path, 'utf8'), /127\.0\.0\.1:7890/);
  await Promise.all([context.syncSystemProxyEnvironment(), context.updateSystemProxyEnvironment({ enabled: false }), context.syncSystemProxyEnvironment()]);
  for (const target of targets) assert.equal(await fsp.readFile(target.path, 'utf8'), original);
  assert.equal(JSON.parse(await fsp.readFile(settingsFile, 'utf8')).enabled, false);
});

test('failed disable rolls back files and keeps the saved enabled state', async t => {
  const { context, targets, io, settingsFile } = await fixture(t);
  await context.updateSystemProxyEnvironment({ enabled: true });
  const before = await Promise.all(targets.map(x => fsp.readFile(x.path, 'utf8')));
  let failed = false;
  io.rename = async (from, to) => {
    if (to === targets[1].path && !failed) { failed = true; throw new Error('simulated write failure'); }
    return fsp.rename(from, to);
  };
  await assert.rejects(context.updateSystemProxyEnvironment({ enabled: false }), /simulated write failure/);
  for (let i = 0; i < targets.length; i++) assert.equal(await fsp.readFile(targets[i].path, 'utf8'), before[i]);
  assert.equal(JSON.parse(await fsp.readFile(settingsFile, 'utf8')).enabled, true);
  await context.updateSystemProxyEnvironment({ enabled: false });
  assert.equal(JSON.parse(await fsp.readFile(settingsFile, 'utf8')).enabled, false);
});

function uiFixture({ enabled = false, online = true, failure = false } = {}) {
  const toggle = {}, status = {};
  const buttons = ['rule', 'global', 'direct'].map(mode => ({ dataset: { mode }, classList: { toggle() {} } }));
  const calls = [];
  const source = fs.readFileSync(require.resolve('../public/app.js'), 'utf8');
  const context = vm.createContext({
    qs: selector => selector === '#systemProxyEnabled' ? toggle : status,
    document: { querySelectorAll: () => buttons }, MODE_LABELS: { rule: '规则', global: '全局', direct: '直连' }, toast() {},
    api: async (url, options) => {
      calls.push({ url, body: JSON.parse(options.body) });
      if (failure) throw new Error('write failed');
      return { management: { settings: { enabled: JSON.parse(options.body).enabled }, active: JSON.parse(options.body).enabled } };
    },
  });
  vm.runInContext(source.slice(source.indexOf('function bindSystemProxyControls('), source.indexOf('async function renderDashboard(')), context);
  context.bindSystemProxyControls({ mode: 'rule' }, { management: { settings: { enabled }, active: enabled } }, online, () => true);
  return { toggle, buttons, calls };
}

test('dashboard modes require enabled proxy and disable only updates proxy environment', async () => {
  const { toggle, buttons, calls } = uiFixture();
  assert.ok(buttons.every(b => b.disabled));
  toggle.checked = true;
  await toggle.onchange();
  assert.ok(buttons.every(b => !b.disabled));
  toggle.checked = false;
  await toggle.onchange();
  assert.ok(buttons.every(b => b.disabled));
  assert.deepEqual(calls, [
    { url: '/api/system/proxy-environment', body: { enabled: true } },
    { url: '/api/system/proxy-environment', body: { enabled: false } },
  ]);
});

test('failed toggle restores displayed state and offline core still allows disabling', async () => {
  const { toggle, buttons } = uiFixture({ enabled: true, online: false, failure: true });
  assert.equal(toggle.disabled, false);
  assert.ok(buttons.every(b => b.disabled));
  toggle.checked = false;
  await toggle.onchange();
  assert.equal(toggle.checked, true);
  assert.equal(toggle.disabled, false);
});
