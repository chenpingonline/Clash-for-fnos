'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const {
  DEFAULT_DNS_SETTINGS,
  normalizeDnsSettings,
  parseDnsSettingsBlock,
  parseHostsBlock,
  renderDnsSettingsBlock,
  renderHostsBlock
} = require('../lib/dns-settings');

const DNS_BLOCK = `dns:
  enable: true
  listen: 127.0.0.1:1053
  enhanced-mode: fake-ip
  fake-ip-range: 198.18.0.1/16
  fake-ip-range6: fdfe:dcba:9876::1/64
  fake-ip-filter-mode: blacklist
  ipv6: true
  prefer-h3: true
  respect-rules: true
  use-hosts: true
  use-system-hosts: false
  direct-nameserver-follow-policy: true
  default-nameserver:
    - 223.5.5.5
  nameserver:
    - https://doh.pub/dns-query
  fallback:
    - https://1.1.1.1/dns-query
  proxy-server-nameserver:
    - tls://223.5.5.5
  direct-nameserver:
    - system
  fake-ip-filter:
    - '*.lan'
  nameserver-policy:
    '+.example.com':
      - https://dns.alidns.com/dns-query
  fallback-filter:
    geoip: true
    geoip-code: CN
    ipcidr:
      - 240.0.0.0/4
    domain:
      - '+.google.com'
  cache-algorithm: arc`;

test('parses categorized Mihomo DNS settings', () => {
  const parsed = parseDnsSettingsBlock(DNS_BLOCK);
  assert.equal(parsed.enable, true);
  assert.equal(parsed.listen, '127.0.0.1:1053');
  assert.equal(parsed.fakeIpRange6, 'fdfe:dcba:9876::1/64');
  assert.equal(parsed.ipv6, true);
  assert.equal(parsed.useSystemHosts, false);
  assert.deepEqual(parsed.nameserver, ['https://doh.pub/dns-query']);
  assert.deepEqual(parsed.nameserverPolicy, [{ matcher: '+.example.com', servers: ['https://dns.alidns.com/dns-query'] }]);
  assert.deepEqual(parsed.fallbackIpCidr, ['240.0.0.0/4']);
  assert.deepEqual(parsed.fallbackDomain, ['+.google.com']);
});

test('uses Clash Verge Rev DNS and fallback-filter defaults with a NAS-safe listener', () => {
  assert.equal(DEFAULT_DNS_SETTINGS.enable, true);
  assert.equal(DEFAULT_DNS_SETTINGS.listen, '127.0.0.1:1053');
  assert.equal(DEFAULT_DNS_SETTINGS.fakeIpRange6, 'fdfe:dcba:9876::1/64');
  assert.equal(DEFAULT_DNS_SETTINGS.ipv6, true);
  assert.deepEqual(DEFAULT_DNS_SETTINGS.fallback, []);
  assert.equal(DEFAULT_DNS_SETTINGS.fallbackGeoip, true);
  assert.equal(DEFAULT_DNS_SETTINGS.fallbackGeoipCode, 'CN');
  assert.deepEqual(DEFAULT_DNS_SETTINGS.fallbackIpCidr, ['240.0.0.0/4', '0.0.0.0/32']);
  assert.deepEqual(DEFAULT_DNS_SETTINGS.fallbackDomain, ['+.google.com', '+.facebook.com', '+.youtube.com']);
  const rendered = renderDnsSettingsBlock(DEFAULT_DNS_SETTINGS);
  assert.match(rendered, /\n  enable: true\n/);
  assert.match(rendered, /\n  ipv6: true\n/);
  assert.match(rendered, /\n    geoip: true\n/);
});

test('preserves explicit disabled DNS switches instead of replacing them with defaults', () => {
  const parsed = parseDnsSettingsBlock(`dns:
  enable: false
  ipv6: false
  fallback-filter:
    geoip: false`);
  assert.equal(parsed.enable, false);
  assert.equal(parsed.ipv6, false);
  assert.equal(parsed.fallbackGeoip, false);
});

test('fills missing DNS fields from Clash Verge defaults but preserves explicit empty lists', () => {
  const missing = parseDnsSettingsBlock(`dns:
  enable: true
  fallback-filter:
    geoip: true`);
  assert.deepEqual(missing.defaultNameserver, DEFAULT_DNS_SETTINGS.defaultNameserver);
  assert.deepEqual(missing.nameserver, DEFAULT_DNS_SETTINGS.nameserver);
  assert.deepEqual(missing.fallbackIpCidr, ['240.0.0.0/4', '0.0.0.0/32']);
  assert.deepEqual(missing.fallbackDomain, ['+.google.com', '+.facebook.com', '+.youtube.com']);

  const explicitEmpty = parseDnsSettingsBlock(`dns:
  enable: true
  default-nameserver: []
  nameserver: []
  fallback-filter:
    ipcidr: []
    domain: []`);
  assert.deepEqual(explicitEmpty.defaultNameserver, []);
  assert.deepEqual(explicitEmpty.nameserver, []);
  assert.deepEqual(explicitEmpty.fallbackIpCidr, []);
  assert.deepEqual(explicitEmpty.fallbackDomain, []);
});

test('renders known values while preserving unknown DNS keys', () => {
  const parsed = parseDnsSettingsBlock(DNS_BLOCK);
  const rendered = renderDnsSettingsBlock(normalizeDnsSettings(parsed, parsed), DNS_BLOCK);
  assert.match(rendered, /cache-algorithm: arc/);
  assert.match(rendered, /proxy-server-nameserver:\n\s+- "tls:\/\/223\.5\.5\.5"/);
  assert.deepEqual(parseDnsSettingsBlock(rendered).nameserverPolicy, parsed.nameserverPolicy);
});

test('reads and writes top-level hosts mappings', () => {
  const hosts = parseHostsBlock(`hosts:
  '*.clash.test': 127.0.0.1
  alpha.clash.test:
    - '::1'
    - 1.1.1.1`);
  assert.deepEqual(hosts, [
    { host: '*.clash.test', values: ['127.0.0.1'] },
    { host: 'alpha.clash.test', values: ['::1', '1.1.1.1'] }
  ]);
  assert.deepEqual(parseHostsBlock(renderHostsBlock(hosts)), hosts);
  assert.equal(renderHostsBlock([]), 'hosts: {}');
  assert.equal(renderHostsBlock([], "hosts: {'legacy.test': 1.1.1.1}"), "hosts: {'legacy.test': 1.1.1.1}");
});

test('normalization preserves omitted switches and rejects unsafe combinations', () => {
  const current = { ...DEFAULT_DNS_SETTINGS, enable: true, ipv6: true, useSystemHosts: true };
  const normalized = normalizeDnsSettings({ listen: '127.0.0.1:1053' }, current);
  assert.equal(normalized.enable, true);
  assert.equal(normalized.ipv6, true);
  assert.equal(normalized.useSystemHosts, true);
  assert.throws(() => normalizeDnsSettings({ ...DEFAULT_DNS_SETTINGS, enable: true, nameserver: [] }), /至少需要一个域名服务器/);
  assert.throws(() => normalizeDnsSettings({ ...DEFAULT_DNS_SETTINGS, respectRules: true, proxyServerNameserver: [] }), /必须配置代理节点 DNS/);
  assert.throws(() => normalizeDnsSettings({ ...DEFAULT_DNS_SETTINGS, listen: '127.0.0.1:70000' }), /监听地址/);
  assert.throws(() => normalizeDnsSettings({ ...DEFAULT_DNS_SETTINGS, nameserver: ['ok\nmalicious: true'] }), /格式无效/);
});
