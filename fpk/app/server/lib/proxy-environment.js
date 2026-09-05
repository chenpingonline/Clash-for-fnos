'use strict';

const PROXY_ENV_BEGIN = '# >>> Clash for fnos proxy >>>';
const PROXY_ENV_END = '# <<< Clash for fnos proxy <<<';
const PROXY_ENV_KEYS = ['http_proxy', 'https_proxy', 'all_proxy', 'no_proxy', 'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY'];
const PROXY_ENV_KEY_SET = new Set(PROXY_ENV_KEYS);
const DEFAULT_PROXY_ENV_SETTINGS = Object.freeze({
  enabled: true,
  followMixedPort: true,
  port: 7890,
  noProxy: 'localhost,127.0.0.1,::1',
  targets: { environment: true, profile: true, bashrc: true }
});

/** @param {unknown} key @param {unknown} input */
function redactProxyEnvValue(key, input) {
  const value = String(input ?? '').trim();
  if (!value) return '';
  if (/no_proxy/i.test(String(key || ''))) return value;
  try {
    const url = new URL(value);
    if (url.password) url.password = '***';
    return url.toString();
  } catch (_) {
    return value.replace(/:\/\/([^\s:@/]+):([^\s@/]+)@/g, '://$1:***@');
  }
}

/** @param {Record<string, unknown> | NodeJS.ProcessEnv | null | undefined} env */
function proxyEnvFromObject(env) {
  return PROXY_ENV_KEYS
    .filter(key => Object.prototype.hasOwnProperty.call(env || {}, key) && String(env?.[key] ?? '').trim())
    .map(key => ({ key, value: redactProxyEnvValue(key, env?.[key]) }));
}

/** @param {unknown} raw */
function parseProxyEnvFile(raw) {
  const variables = [];
  const lines = String(raw || '').split(/\r?\n/);
  for (let index = 0; index < lines.length; index += 1) {
    let line = lines[index].trim();
    if (!line || line.startsWith('#')) continue;
    if (line.startsWith('export ')) line = line.slice(7).trim();
    const equals = line.indexOf('=');
    if (equals <= 0) continue;
    const key = line.slice(0, equals).trim();
    if (!PROXY_ENV_KEY_SET.has(key)) continue;
    let value = line.slice(equals + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) value = value.slice(1, -1);
    variables.push({ key, value: redactProxyEnvValue(key, value), line: index + 1 });
  }
  return variables;
}

/** @typedef {{enabled: boolean, followMixedPort: boolean, port: number, noProxy: string, targets: {environment: boolean, profile: boolean, bashrc: boolean}}} ProxyEnvSettings */

/** @param {Record<string, any>} [input] @param {ProxyEnvSettings} [base] @returns {ProxyEnvSettings} */
function normalizeProxyEnvSettings(input = {}, base = DEFAULT_PROXY_ENV_SETTINGS) {
  const targetInput = input.targets && typeof input.targets === 'object' ? input.targets : {};
  const baseTargets = base.targets || DEFAULT_PROXY_ENV_SETTINGS.targets;
  const settings = {
    enabled: input.enabled === undefined ? Boolean(base.enabled) : Boolean(input.enabled),
    followMixedPort: input.followMixedPort === undefined ? Boolean(base.followMixedPort) : Boolean(input.followMixedPort),
    port: Number(input.port === undefined ? base.port : input.port),
    noProxy: String(input.noProxy === undefined ? base.noProxy : input.noProxy).trim(),
    targets: {
      environment: targetInput.environment === undefined ? Boolean(baseTargets.environment) : Boolean(targetInput.environment),
      profile: targetInput.profile === undefined ? Boolean(baseTargets.profile) : Boolean(targetInput.profile),
      bashrc: targetInput.bashrc === undefined ? Boolean(baseTargets.bashrc) : Boolean(targetInput.bashrc)
    }
  };
  if (!Number.isInteger(settings.port) || settings.port < 1 || settings.port > 65535) throw Object.assign(new Error('代理端口必须是 1-65535'), { statusCode: 400 });
  if (!settings.noProxy) settings.noProxy = DEFAULT_PROXY_ENV_SETTINGS.noProxy;
  if (settings.noProxy.length > 4096 || /[\r\n\0]/.test(settings.noProxy)) throw Object.assign(new Error('NO_PROXY 格式无效'), { statusCode: 400 });
  if (settings.enabled && !Object.values(settings.targets).some(Boolean)) throw Object.assign(new Error('启用代理环境变量时至少选择一个写入位置'), { statusCode: 400 });
  return settings;
}

/** @param {unknown} raw */
function stripManagedProxyEnvBlock(raw) {
  const lines = String(raw || '').match(/[^\n]*\n|[^\n]+$/g) || [];
  const kept = [];
  let managed = false;
  for (const line of lines) {
    const marker = line.trim();
    if (marker === PROXY_ENV_BEGIN) {
      if (managed) throw Object.assign(new Error('检测到嵌套的代理环境变量管理块，请先手动修复'), { statusCode: 409 });
      managed = true;
    } else if (marker === PROXY_ENV_END) {
      if (!managed) throw Object.assign(new Error('检测到孤立的代理环境变量结束标记，请先手动修复'), { statusCode: 409 });
      managed = false;
    } else if (!managed) kept.push(line);
  }
  if (managed) throw Object.assign(new Error('检测到未闭合的 Clash for fnos 代理环境变量管理块，请先手动修复'), { statusCode: 409 });
  return kept.join('');
}

/** @param {unknown} value @param {boolean} shell */
function proxyEnvQuoted(value, shell) {
  let output = String(value ?? '').replace(/\\/g, '\\\\').replace(/"/g, '\\"');
  if (shell) output = output.replace(/\$/g, '\\$').replace(/`/g, '\\`');
  return `"${output}"`;
}

/** @param {number} port @param {string} noProxy @param {boolean} shell */
function managedProxyEnvBlock(port, noProxy, shell) {
  const httpUrl = `http://127.0.0.1:${port}`;
  const prefix = shell ? 'export ' : '';
  const values = [
    ['http_proxy', httpUrl], ['https_proxy', httpUrl],
    ['HTTP_PROXY', httpUrl], ['HTTPS_PROXY', httpUrl],
    ['no_proxy', noProxy], ['NO_PROXY', noProxy]
  ];
  return [PROXY_ENV_BEGIN, ...values.map(([key, value]) => `${prefix}${key}=${proxyEnvQuoted(value, shell)}`), PROXY_ENV_END].join('\n');
}

/** @param {unknown} raw @param {string} block */
function withManagedProxyEnvBlock(raw, block) {
  const clean = stripManagedProxyEnvBlock(raw);
  if (!block) return clean;
  return `${clean}${clean && !clean.endsWith('\n') ? '\n' : ''}${block}\n`;
}

module.exports = {
  DEFAULT_PROXY_ENV_SETTINGS,
  PROXY_ENV_BEGIN,
  PROXY_ENV_END,
  PROXY_ENV_KEYS,
  managedProxyEnvBlock,
  normalizeProxyEnvSettings,
  parseProxyEnvFile,
  proxyEnvFromObject,
  redactProxyEnvValue,
  stripManagedProxyEnvBlock,
  withManagedProxyEnvBlock
};
