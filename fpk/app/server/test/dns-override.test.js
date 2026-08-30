'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const {
  defaultDnsOverrideSettings,
  normalizeStoredDnsOverride,
  resolveDnsOverrideUpdate
} = require('../lib/dns-override');

test('DNS override defaults off while its DNS template defaults enabled', () => {
  const defaults = defaultDnsOverrideSettings();
  const result = resolveDnsOverrideUpdate(false, defaults, {});
  assert.equal(result.enabled, false);
  assert.equal(result.settings.enable, true);
  assert.equal(result.shouldApply, false);
});

test('saving a DNS template while override is off does not request config application', () => {
  const result = resolveDnsOverrideUpdate(false, defaultDnsOverrideSettings(), {
    dnsOverrideEnabled: false,
    dns: { enable: true }
  });
  assert.equal(result.enabled, false);
  assert.equal(result.settings.enable, true);
  assert.equal(result.shouldApply, false);
});

test('enabling DNS override applies the saved DNS template', () => {
  const result = resolveDnsOverrideUpdate(false, { ...defaultDnsOverrideSettings(), enable: true }, {
    dnsOverrideEnabled: true
  });
  assert.equal(result.enabled, true);
  assert.equal(result.settings.enable, true);
  assert.equal(result.shouldApply, true);
});

test('invalid persisted DNS override data falls back to safe defaults', () => {
  const result = normalizeStoredDnsOverride({ enable: true, nameserver: [] });
  assert.equal(result.enable, true);
  assert.ok(result.nameserver.length > 0);
});
