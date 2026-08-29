'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const {
  managedProxyEnvBlock,
  normalizeProxyEnvSettings,
  parseProxyEnvFile,
  proxyEnvFromObject,
  withManagedProxyEnvBlock
} = require('../lib/proxy-environment');

test('proxy credentials are redacted in process and file views', () => {
  assert.deepEqual(proxyEnvFromObject({ HTTP_PROXY: 'http://user:secret@proxy.local:8080' }), [
    { key: 'HTTP_PROXY', value: 'http://user:***@proxy.local:8080/' }
  ]);
  assert.equal(parseProxyEnvFile("export HTTPS_PROXY='http://user:secret@proxy.local:8080'")[0].value, 'http://user:***@proxy.local:8080/');
});

test('managed proxy block can be applied, replaced, and removed idempotently', () => {
  const original = 'PATH=/usr/bin\n';
  const first = withManagedProxyEnvBlock(original, managedProxyEnvBlock(7890, 'localhost', true));
  const replaced = withManagedProxyEnvBlock(first, managedProxyEnvBlock(7891, 'localhost', true));
  assert.match(replaced, /127\.0\.0\.1:7891/);
  assert.doesNotMatch(replaced, /127\.0\.0\.1:7890/);
  assert.equal(withManagedProxyEnvBlock(replaced, ''), original);
});

test('proxy settings reject dangerous and ineffective input', () => {
  assert.equal(normalizeProxyEnvSettings({ port: 7890 }).port, 7890);
  assert.throws(() => normalizeProxyEnvSettings({ port: 0 }), /1-65535/);
  assert.throws(() => normalizeProxyEnvSettings({ noProxy: 'localhost\nHTTP_PROXY=x' }), /格式无效/);
  assert.throws(() => normalizeProxyEnvSettings({ enabled: true, targets: { environment: false, profile: false, bashrc: false } }), /至少选择一个/);
});
