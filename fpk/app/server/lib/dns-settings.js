'use strict';

/** @typedef {{matcher:string, servers:string[]}} DnsPolicy */
/** @typedef {{host:string, values:string[]}} HostMapping */
/**
 * @typedef {Object} DnsSettings
 * @property {boolean} enable
 * @property {string} listen
 * @property {'fake-ip'|'redir-host'} enhancedMode
 * @property {string} fakeIpRange
 * @property {string} fakeIpRange6
 * @property {'blacklist'|'whitelist'|'rule'} fakeIpFilterMode
 * @property {boolean} ipv6
 * @property {boolean} preferH3
 * @property {boolean} respectRules
 * @property {boolean} useHosts
 * @property {boolean} useSystemHosts
 * @property {boolean} directNameserverFollowPolicy
 * @property {string[]} defaultNameserver
 * @property {string[]} nameserver
 * @property {string[]} fallback
 * @property {string[]} proxyServerNameserver
 * @property {string[]} directNameserver
 * @property {string[]} fakeIpFilter
 * @property {DnsPolicy[]} nameserverPolicy
 * @property {boolean} fallbackGeoip
 * @property {string} fallbackGeoipCode
 * @property {string[]} fallbackIpCidr
 * @property {string[]} fallbackDomain
 * @property {HostMapping[]} hosts
 */

/** @type {Readonly<DnsSettings>} */
const DEFAULT_DNS_SETTINGS = Object.freeze({
  enable: true,
  listen: '127.0.0.1:1053',
  enhancedMode: 'fake-ip',
  fakeIpRange: '198.18.0.1/16',
  fakeIpRange6: 'fdfe:dcba:9876::1/64',
  fakeIpFilterMode: 'blacklist',
  ipv6: true,
  preferH3: false,
  respectRules: false,
  useHosts: false,
  useSystemHosts: false,
  directNameserverFollowPolicy: false,
  defaultNameserver: ['system', '223.6.6.6', '8.8.8.8', '2400:3200::1', '2001:4860:4860::8888'],
  nameserver: ['8.8.8.8', 'https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query'],
  fallback: [],
  proxyServerNameserver: ['https://doh.pub/dns-query', 'https://dns.alidns.com/dns-query', 'tls://223.5.5.5'],
  directNameserver: [],
  fakeIpFilter: ['*.lan', '*.local', '*.arpa', 'time.*.com', 'ntp.*.com', '+.market.xiaomi.com', 'localhost.ptlogin2.qq.com', '*.msftncsi.com', 'www.msftconnecttest.com'],
  nameserverPolicy: [],
  fallbackGeoip: true,
  fallbackGeoipCode: 'CN',
  fallbackIpCidr: ['240.0.0.0/4', '0.0.0.0/32'],
  fallbackDomain: ['+.google.com', '+.facebook.com', '+.youtube.com'],
  hosts: []
});

/** @param {unknown} value */
function scalarText(value) {
  let text = String(value ?? '').trim().replace(/\s+#.*$/, '').trim();
  if ((text.startsWith('"') && text.endsWith('"')) || (text.startsWith("'") && text.endsWith("'"))) {
    if (text.startsWith('"')) {
      try { return String(JSON.parse(text)); } catch (_) {}
    }
    return text.slice(1, -1).replace(/''/g, "'");
  }
  return text;
}

/** @param {unknown} value @param {boolean} fallback */
function boolValue(value, fallback) {
  const text = scalarText(value).toLowerCase();
  if (['true', 'yes', 'on', '1'].includes(text)) return true;
  if (['false', 'no', 'off', '0'].includes(text)) return false;
  return fallback;
}

/** @param {string} input */
function splitInlineList(input) {
  const text = String(input || '').trim();
  if (!text.startsWith('[') || !text.endsWith(']')) return text ? [scalarText(text)] : [];
  return text.slice(1, -1).split(',').map(scalarText).filter(Boolean);
}

/**
 * Return direct child YAML sections beneath a mapping header.
 * @param {string} blockText
 */
function childSections(blockText) {
  const lines = String(blockText || '').split(/\r?\n/);
  const candidates = lines.slice(1).filter(line => line.trim() && !/^\s*#/.test(line));
  const indents = candidates.map(line => line.match(/^(\s*)/)?.[1].length || 0).filter(n => n > 0);
  const indent = indents.length ? Math.min(...indents) : 2;
  /** @type {Map<string, {key:string, tail:string, text:string, lines:string[]}>} */
  const sections = new Map();
  for (let i = 1; i < lines.length; i += 1) {
    const match = lines[i].match(new RegExp(`^\\s{${indent}}([^:#][^:]*?)\\s*:\\s*(.*)$`));
    if (!match) continue;
    const key = scalarText(match[1]);
    const start = i;
    while (i + 1 < lines.length) {
      const next = lines[i + 1];
      const nextIndent = next.match(/^(\s*)/)?.[1].length || 0;
      if (next.trim() && !/^\s*#/.test(next) && nextIndent <= indent) break;
      i += 1;
    }
    const sectionLines = lines.slice(start, i + 1);
    sections.set(key, { key, tail: match[2].trim(), text: sectionLines.join('\n'), lines: sectionLines });
  }
  return sections;
}

/** @param {{tail:string, lines:string[]}|undefined} section */
function sequenceValue(section) {
  if (!section) return [];
  if (section.tail) return splitInlineList(section.tail);
  return section.lines.slice(1).map(line => line.match(/^\s*-\s*(.*)$/)?.[1]).filter(v => v !== undefined).map(scalarText).filter(Boolean);
}

/** @param {{tail:string, text:string}|undefined} section */
function mappingValue(section) {
  if (!section || section.tail) return [];
  const nested = childSections(`root:\n${section.text.split(/\r?\n/).slice(1).map(line => line.replace(/^\s{2}/, '')).join('\n')}`);
  return [...nested.values()].map(entry => ({ key: entry.key, values: sequenceValue(entry).length ? sequenceValue(entry) : (entry.tail ? [scalarText(entry.tail)] : []) }));
}

/** @param {string} blockText */
function parseDnsSettingsBlock(blockText) {
  const sections = childSections(blockText);
  /** @param {string} key @param {string} fallback */
  const scalar = (key, fallback) => scalarText(sections.get(key)?.tail ?? fallback);
  /** @param {string} key @param {boolean} fallback */
  const bool = (key, fallback) => sections.has(key) ? boolValue(sections.get(key)?.tail, fallback) : fallback;
  /** @param {string} key @param {readonly string[]} fallback */
  const sequence = (key, fallback) => sections.has(key) ? sequenceValue(sections.get(key)) : [...fallback];
  const fallbackFilter = sections.get('fallback-filter');
  const fallbackSections = fallbackFilter
    ? childSections(`root:\n${fallbackFilter.text.split(/\r?\n/).slice(1).map(line => line.replace(/^\s{2}/, '')).join('\n')}`)
    : new Map();
  /** @param {string} key @param {string} fallback */
  const fallbackScalar = (key, fallback) => scalarText(fallbackSections.get(key)?.tail ?? fallback);
  /** @param {string} key @param {boolean} fallback */
  const fallbackBool = (key, fallback) => fallbackSections.has(key) ? boolValue(fallbackSections.get(key)?.tail, fallback) : fallback;
  /** @param {string} key @param {readonly string[]} fallback */
  const fallbackSequence = (key, fallback) => fallbackSections.has(key) ? sequenceValue(fallbackSections.get(key)) : [...fallback];
  const policies = mappingValue(sections.get('nameserver-policy')).map(item => ({ matcher: item.key, servers: item.values }));
  const hosts = mappingValue(sections.get('hosts')).map(item => ({ host: item.key, values: item.values }));
  const mode = scalar('enhanced-mode', DEFAULT_DNS_SETTINGS.enhancedMode);
  const filterMode = scalar('fake-ip-filter-mode', DEFAULT_DNS_SETTINGS.fakeIpFilterMode);
  return {
    enable: bool('enable', DEFAULT_DNS_SETTINGS.enable),
    listen: scalar('listen', DEFAULT_DNS_SETTINGS.listen),
    enhancedMode: ['fake-ip', 'redir-host'].includes(mode) ? mode : DEFAULT_DNS_SETTINGS.enhancedMode,
    fakeIpRange: scalar('fake-ip-range', DEFAULT_DNS_SETTINGS.fakeIpRange),
    fakeIpRange6: scalar('fake-ip-range6', DEFAULT_DNS_SETTINGS.fakeIpRange6),
    fakeIpFilterMode: ['blacklist', 'whitelist', 'rule'].includes(filterMode) ? filterMode : DEFAULT_DNS_SETTINGS.fakeIpFilterMode,
    ipv6: bool('ipv6', DEFAULT_DNS_SETTINGS.ipv6),
    preferH3: bool('prefer-h3', DEFAULT_DNS_SETTINGS.preferH3),
    respectRules: bool('respect-rules', DEFAULT_DNS_SETTINGS.respectRules),
    useHosts: bool('use-hosts', DEFAULT_DNS_SETTINGS.useHosts),
    useSystemHosts: bool('use-system-hosts', DEFAULT_DNS_SETTINGS.useSystemHosts),
    directNameserverFollowPolicy: bool('direct-nameserver-follow-policy', DEFAULT_DNS_SETTINGS.directNameserverFollowPolicy),
    defaultNameserver: sequence('default-nameserver', DEFAULT_DNS_SETTINGS.defaultNameserver),
    nameserver: sequence('nameserver', DEFAULT_DNS_SETTINGS.nameserver),
    fallback: sequence('fallback', DEFAULT_DNS_SETTINGS.fallback),
    proxyServerNameserver: sequence('proxy-server-nameserver', DEFAULT_DNS_SETTINGS.proxyServerNameserver),
    directNameserver: sequence('direct-nameserver', DEFAULT_DNS_SETTINGS.directNameserver),
    fakeIpFilter: sequence('fake-ip-filter', DEFAULT_DNS_SETTINGS.fakeIpFilter),
    nameserverPolicy: policies,
    fallbackGeoip: fallbackBool('geoip', DEFAULT_DNS_SETTINGS.fallbackGeoip),
    fallbackGeoipCode: fallbackScalar('geoip-code', DEFAULT_DNS_SETTINGS.fallbackGeoipCode).toUpperCase(),
    fallbackIpCidr: fallbackSequence('ipcidr', DEFAULT_DNS_SETTINGS.fallbackIpCidr),
    fallbackDomain: fallbackSequence('domain', DEFAULT_DNS_SETTINGS.fallbackDomain),
    hosts
  };
}

/** @param {unknown} input @param {string} label @param {number} max */
function safeText(input, label, max = 512) {
  const text = String(input ?? '').trim();
  if (!text || text.length > max || /[\r\n\0]/.test(text)) throw Object.assign(new Error(`${label}格式无效`), { statusCode: 400 });
  return text;
}

/** @param {unknown} input @param {string} label */
function safeList(input, label) {
  if (!Array.isArray(input) || input.length > 128) throw Object.assign(new Error(`${label}格式无效`), { statusCode: 400 });
  return [...new Set(input.map(value => safeText(value, label)).filter(Boolean))];
}

/** @param {unknown} input @param {string} label @param {'matcher'|'host'} keyName */
function safeMappings(input, label, keyName) {
  if (!Array.isArray(input) || input.length > 128) throw Object.assign(new Error(`${label}格式无效`), { statusCode: 400 });
  return input.map(item => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) throw Object.assign(new Error(`${label}格式无效`), { statusCode: 400 });
    const record = /** @type {Record<string, unknown>} */ (item);
    return { [keyName]: safeText(record[keyName], label), [keyName === 'matcher' ? 'servers' : 'values']: safeList(record[keyName === 'matcher' ? 'servers' : 'values'], label) };
  });
}

/** @param {unknown} input @param {DnsSettings} current */
function normalizeDnsSettings(input, current = /** @type {DnsSettings} */ (DEFAULT_DNS_SETTINGS)) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw Object.assign(new Error('DNS 设置格式无效'), { statusCode: 400 });
  const src = /** @type {Record<string, unknown>} */ (input);
  const enhancedMode = String(src.enhancedMode ?? current.enhancedMode);
  const fakeIpFilterMode = String(src.fakeIpFilterMode ?? current.fakeIpFilterMode);
  if (!['fake-ip', 'redir-host'].includes(enhancedMode)) throw Object.assign(new Error('DNS 增强模式无效'), { statusCode: 400 });
  if (!['blacklist', 'whitelist', 'rule'].includes(fakeIpFilterMode)) throw Object.assign(new Error('Fake IP 过滤模式无效'), { statusCode: 400 });
  const listen = safeText(src.listen ?? current.listen, 'DNS 监听地址', 256);
  const listenMatch = listen.match(/^(?::(\d{1,5})|[^\s:]+:(\d{1,5})|\[[0-9a-fA-F:]+]:(\d{1,5}))$/);
  const listenPort = Number(listenMatch?.[1] || listenMatch?.[2] || listenMatch?.[3]);
  if (!listenMatch || !Number.isInteger(listenPort) || listenPort < 1 || listenPort > 65535) throw Object.assign(new Error('DNS 监听地址应为 127.0.0.1:1053、[::1]:1053 或 :53'), { statusCode: 400 });
  const geoipCode = safeText(src.fallbackGeoipCode ?? current.fallbackGeoipCode, 'GeoIP 国家代码', 2).toUpperCase();
  if (!/^[A-Z]{2}$/.test(geoipCode)) throw Object.assign(new Error('GeoIP 国家代码必须是两个英文字母'), { statusCode: 400 });
  const normalized = {
    enable: src.enable === undefined ? current.enable : Boolean(src.enable), listen,
    enhancedMode: /** @type {'fake-ip'|'redir-host'} */ (enhancedMode),
    fakeIpRange: safeText(src.fakeIpRange ?? current.fakeIpRange, 'Fake IP 范围', 128),
    fakeIpRange6: safeText(src.fakeIpRange6 ?? current.fakeIpRange6, 'Fake IP IPv6 范围', 128),
    fakeIpFilterMode: /** @type {'blacklist'|'whitelist'|'rule'} */ (fakeIpFilterMode),
    ipv6: src.ipv6 === undefined ? current.ipv6 : Boolean(src.ipv6),
    preferH3: src.preferH3 === undefined ? current.preferH3 : Boolean(src.preferH3),
    respectRules: src.respectRules === undefined ? current.respectRules : Boolean(src.respectRules),
    useHosts: src.useHosts === undefined ? current.useHosts : Boolean(src.useHosts),
    useSystemHosts: src.useSystemHosts === undefined ? current.useSystemHosts : Boolean(src.useSystemHosts),
    directNameserverFollowPolicy: src.directNameserverFollowPolicy === undefined ? current.directNameserverFollowPolicy : Boolean(src.directNameserverFollowPolicy),
    defaultNameserver: safeList(src.defaultNameserver ?? current.defaultNameserver, '默认域名服务器'),
    nameserver: safeList(src.nameserver ?? current.nameserver, '域名服务器'),
    fallback: safeList(src.fallback ?? current.fallback, '回退服务器'),
    proxyServerNameserver: safeList(src.proxyServerNameserver ?? current.proxyServerNameserver, '代理节点 DNS'),
    directNameserver: safeList(src.directNameserver ?? current.directNameserver, '直连域名服务器'),
    fakeIpFilter: safeList(src.fakeIpFilter ?? current.fakeIpFilter, 'Fake IP 过滤'),
    nameserverPolicy: /** @type {DnsPolicy[]} */ (safeMappings(src.nameserverPolicy ?? current.nameserverPolicy, '域名服务器策略', 'matcher')),
    fallbackGeoip: src.fallbackGeoip === undefined ? current.fallbackGeoip : Boolean(src.fallbackGeoip), fallbackGeoipCode: geoipCode,
    fallbackIpCidr: safeList(src.fallbackIpCidr ?? current.fallbackIpCidr, '回退 IP CIDR'),
    fallbackDomain: safeList(src.fallbackDomain ?? current.fallbackDomain, '回退域名'),
    hosts: /** @type {HostMapping[]} */ (safeMappings(src.hosts ?? current.hosts, 'Hosts 设置', 'host'))
  };
  if (normalized.enable && normalized.nameserver.length === 0) throw Object.assign(new Error('启用 DNS 时至少需要一个域名服务器'), { statusCode: 400 });
  if (normalized.respectRules && normalized.proxyServerNameserver.length === 0) throw Object.assign(new Error('遵循路由规则时必须配置代理节点 DNS，避免解析循环'), { statusCode: 400 });
  return normalized;
}

/** @param {string} value */
function yamlString(value) { return JSON.stringify(String(value)); }
/** @param {string[]} lines @param {string} key @param {string[]} values @param {number} indent */
function appendSequence(lines, key, values, indent = 2) {
  if (!values.length) return;
  const pad = ' '.repeat(indent);
  lines.push(`${pad}${key}:`);
  for (const value of values) lines.push(`${pad}  - ${yamlString(value)}`);
}

const KNOWN_DNS_KEYS = new Set([
  'enable', 'listen', 'enhanced-mode', 'fake-ip-range', 'fake-ip-range6', 'fake-ip-filter-mode', 'ipv6', 'prefer-h3', 'respect-rules',
  'use-hosts', 'use-system-hosts', 'direct-nameserver-follow-policy', 'default-nameserver', 'nameserver', 'fallback',
  'proxy-server-nameserver', 'direct-nameserver', 'fake-ip-filter', 'nameserver-policy', 'fallback-filter'
]);

/** @param {DnsSettings} settings @param {string} existingBlock */
function renderDnsSettingsBlock(settings, existingBlock = '') {
  const lines = ['dns:'];
  lines.push(`  enable: ${settings.enable}`);
  lines.push(`  listen: ${yamlString(settings.listen)}`);
  lines.push(`  enhanced-mode: ${settings.enhancedMode}`);
  lines.push(`  fake-ip-range: ${yamlString(settings.fakeIpRange)}`);
  lines.push(`  fake-ip-range6: ${yamlString(settings.fakeIpRange6)}`);
  lines.push(`  fake-ip-filter-mode: ${settings.fakeIpFilterMode}`);
  lines.push(`  ipv6: ${settings.ipv6}`);
  lines.push(`  prefer-h3: ${settings.preferH3}`);
  lines.push(`  respect-rules: ${settings.respectRules}`);
  lines.push(`  use-hosts: ${settings.useHosts}`);
  lines.push(`  use-system-hosts: ${settings.useSystemHosts}`);
  lines.push(`  direct-nameserver-follow-policy: ${settings.directNameserverFollowPolicy}`);
  appendSequence(lines, 'default-nameserver', settings.defaultNameserver);
  appendSequence(lines, 'nameserver', settings.nameserver);
  appendSequence(lines, 'fallback', settings.fallback);
  appendSequence(lines, 'proxy-server-nameserver', settings.proxyServerNameserver);
  appendSequence(lines, 'direct-nameserver', settings.directNameserver);
  appendSequence(lines, 'fake-ip-filter', settings.fakeIpFilter);
  if (settings.nameserverPolicy.length) {
    lines.push('  nameserver-policy:');
    for (const policy of settings.nameserverPolicy) {
      lines.push(`    ${yamlString(policy.matcher)}:`);
      for (const server of policy.servers) lines.push(`      - ${yamlString(server)}`);
    }
  }
  lines.push('  fallback-filter:');
  lines.push(`    geoip: ${settings.fallbackGeoip}`);
  lines.push(`    geoip-code: ${settings.fallbackGeoipCode}`);
  appendSequence(lines, 'ipcidr', settings.fallbackIpCidr, 4);
  appendSequence(lines, 'domain', settings.fallbackDomain, 4);
  const unknown = [...childSections(existingBlock).values()].filter(section => !KNOWN_DNS_KEYS.has(section.key)).map(section => section.text);
  if (unknown.length) lines.push(...unknown);
  return lines.join('\n');
}

/** @param {string} blockText */
function parseHostsBlock(blockText) {
  return [...childSections(blockText).values()].map(entry => ({
    host: entry.key,
    values: sequenceValue(entry).length ? sequenceValue(entry) : (entry.tail ? [scalarText(entry.tail)] : [])
  }));
}

/** @param {HostMapping[]} hosts @param {string} existingBlock */
function renderHostsBlock(hosts, existingBlock = '') {
  if (!hosts.length) {
    const existing = String(existingBlock || '').trim();
    if (existing && parseHostsBlock(existing).length === 0 && !/^hosts\s*:\s*(?:\{\s*\}|null|~)?\s*$/i.test(existing)) return existingBlock;
    return 'hosts: {}';
  }
  const lines = ['hosts:'];
  for (const mapping of hosts) {
    lines.push(`  ${yamlString(mapping.host)}:`);
    for (const value of mapping.values) lines.push(`    - ${yamlString(value)}`);
  }
  return lines.join('\n');
}

module.exports = { DEFAULT_DNS_SETTINGS, normalizeDnsSettings, parseDnsSettingsBlock, parseHostsBlock, renderDnsSettingsBlock, renderHostsBlock };
