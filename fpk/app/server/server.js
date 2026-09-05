'use strict';

const http = require('http');
const https = require('https');
const tls = require('tls');
const fs = require('fs');
const fsp = fs.promises;
const path = require('path');
const crypto = require('crypto');
const os = require('os');
const {
  compareVersions,
  detectMihomoDownloadTarget: resolveMihomoDownloadTarget,
  mihomoReleaseAssetNames
} = require('./lib/version');
const { parseProxyGroupOrder } = require('./lib/yaml-proxy-groups');
const { PROXY_ENV_KEYS, proxyEnvFromObject, redactProxyEnvValue } = require('./lib/proxy-environment');
const { defaultDnsOverrideSettings, normalizeStoredDnsOverride, resolveDnsOverrideUpdate } = require('./lib/dns-override');

const APP_NAME = 'clash-for-fnos';
const APP_VERSION = require('./package.json').version;
const APP_RELEASE_REPO = String(process.env.CLASH_FOR_FNOS_RELEASE_REPO || 'chenpingonline/Clash-for-fnos').trim();
const GATEWAY_PREFIX = (process.env.GATEWAY_PREFIX || `/app/${APP_NAME}`).replace(/\/$/, '');
const SOCKET_PATH = process.env.SOCKET_PATH || '/tmp/clash-for-fnos.sock';
const PRIV_SOCKET_PATH = process.env.PRIV_SOCKET_PATH || path.join(process.env.TRIM_PKGVAR || '/tmp', 'clash-for-fnos-priv.sock');
const ETC_DIR = process.env.TRIM_PKGETC || path.join(__dirname, '.data', 'etc');
const VAR_DIR = process.env.TRIM_PKGVAR || path.join(__dirname, '.data', 'var');
const PUBLIC_DIR = path.join(__dirname, 'public');
const SETTINGS_FILE = path.join(ETC_DIR, 'settings.json');
const PROFILES_FILE = path.join(ETC_DIR, 'profiles.json');
const SELECTED_FILE = path.join(ETC_DIR, 'selected.json');
const MANAGED_CONFIG_FILE = path.join(ETC_DIR, 'config.yaml');
const CONFIG_META_FILE = path.join(ETC_DIR, 'config-meta.json');
const PROFILE_DIR = path.join(ETC_DIR, 'profiles');
const BACKUP_DIR = path.join(ETC_DIR, 'backups');
const CORE_STAGE_DIR = path.join(VAR_DIR, 'core-stage');
const APP_LOG = path.join(VAR_DIR, 'clash-for-fnos.log');
const MIHOMO_LOG_FILE = path.join(VAR_DIR, 'mihomo.log');
const MAX_MIHOMO_LOG_BYTES = 1024 * 1024;
const MIHOMO_LOG_TRIM_TARGET = 768 * 1024;
const MAX_BODY = 12 * 1024 * 1024;
const MAX_REMOTE_CONFIG = 12 * 1024 * 1024;
const MAX_CORE_ASSET = 80 * 1024 * 1024;


function parseFnOSPathList(raw) {
  const out = [];
  const seen = new Set();
  for (const part of String(raw || '').split(':')) {
    const value = part.trim();
    if (!value || !path.isAbsolute(value) || value.includes('\0')) continue;
    const normalized = path.normalize(value);
    if (seen.has(normalized)) continue;
    seen.add(normalized);
    out.push(normalized);
  }
  return out;
}

const AUTHORIZED_PATHS_FILE = path.join(ETC_DIR, 'authorized-paths.txt');
const AUTHORIZED_REAL_ROOTS = new Map();
async function currentAuthorizedPaths() {
  try {
    const raw = await fsp.readFile(AUTHORIZED_PATHS_FILE, 'utf8');
    return parseFnOSPathList(raw);
  } catch (_) {
    return parseFnOSPathList(process.env.TRIM_DATA_ACCESSIBLE_PATHS || '');
  }
}

const SYSTEM_MIHOMO_CONFIG_PATHS = new Set([
  '/etc/mihomo/config.yaml', '/etc/clash/config.yaml',
  '/usr/local/etc/mihomo/config.yaml', '/usr/local/etc/clash/config.yaml',
  '/opt/mihomo/config.yaml', '/var/lib/mihomo/config.yaml',
  '/root/.config/mihomo/config.yaml', '/root/.config/clash/config.yaml'
].map(x => path.normalize(x)));
let detectedMihomoConfigPaths = new Set();

function pathWithinRoot(candidate, root) {
  const rel = path.relative(root, candidate);
  return rel === '' || (!rel.startsWith('..' + path.sep) && rel !== '..' && !path.isAbsolute(rel));
}

async function realRoot(root) {
  if (AUTHORIZED_REAL_ROOTS.has(root)) return AUTHORIZED_REAL_ROOTS.get(root);
  let resolved = path.resolve(root);
  try { resolved = await fsp.realpath(root); } catch (_) {}
  AUTHORIZED_REAL_ROOTS.set(root, resolved);
  return resolved;
}

async function isAuthorizedUserPath(input) {
  const normalized = path.normalize(String(input || ''));
  if (!path.isAbsolute(normalized)) return false;
  for (const root of await currentAuthorizedPaths()) {
    if (!pathWithinRoot(normalized, root)) continue;
    let realCandidate = path.resolve(normalized);
    try { realCandidate = await fsp.realpath(normalized); } catch (_) {}
    const resolvedRoot = await realRoot(root);
    if (pathWithinRoot(realCandidate, resolvedRoot)) return true;
  }
  return false;
}

function isKnownSystemMihomoConfig(input) {
  const normalized = path.normalize(String(input || ''));
  return SYSTEM_MIHOMO_CONFIG_PATHS.has(normalized) || detectedMihomoConfigPaths.has(normalized);
}

async function authorizedPathStatus() {
  const items = [];
  const roots = await currentAuthorizedPaths();
  for (const root of roots) {
    let exists = false, readable = false, writable = false, realPath = null, error = null;
    try {
      const st = await fsp.stat(root);
      exists = st.isDirectory();
      if (exists) {
        realPath = await fsp.realpath(root).catch(() => path.resolve(root));
        readable = await fsp.access(root, fs.constants.R_OK).then(() => true).catch(() => false);
        writable = await fsp.access(root, fs.constants.W_OK).then(() => true).catch(() => false);
      }
    } catch (err) { error = err.message || String(err); }
    items.push({ path: root, realPath, exists, readable, writable, error });
  }
  return {
    enabled: roots.length > 0,
    paths: items,
    count: items.length,
    processUid: typeof process.getuid === 'function' ? process.getuid() : null,
    processGid: typeof process.getgid === 'function' ? process.getgid() : null,
    runningAsRoot: typeof process.getuid === 'function' ? process.getuid() === 0 : false
  };
}

async function scanAuthorizedYamlFiles(root, maxDepth = 3, maxFiles = 60, maxEntries = 2500) {
  const found = [];
  let visited = 0;
  async function walk(dir, depth) {
    if (depth > maxDepth || found.length >= maxFiles || visited >= maxEntries) return;
    let entries;
    try { entries = await fsp.readdir(dir, { withFileTypes: true }); } catch (_) { return; }
    for (const entry of entries) {
      if (found.length >= maxFiles || visited++ >= maxEntries) break;
      if (entry.name === '.git' || entry.name === 'node_modules' || entry.name === '@eaDir') continue;
      const full = path.join(dir, entry.name);
      if (entry.isSymbolicLink()) continue;
      if (entry.isFile() && /\.ya?ml$/i.test(entry.name)) found.push(full);
      else if (entry.isDirectory()) await walk(full, depth + 1);
    }
  }
  await walk(root, 0);
  return found;
}

const defaults = {
  controller: 'http://127.0.0.1:9090',
  secret: '',
  controllerAutoDetect: true,
  persistSelections: true,
  applyManagedConfigOnStart: true,
  healthcheckUrl: 'https://www.gstatic.com/generate_204',
  healthcheckTimeout: 5000,
  dnsOverrideEnabled: false,
  dnsOverrideSettings: defaultDnsOverrideSettings()
};

let settings = { ...defaults };
let profilesState = { current: null, items: [] };
let selectedState = {};
let localConfigScanCache = new Map();
let mihomoLogWriteQueue = Promise.resolve();
let mihomoLogCollectorAbort = null;
const mihomoLogClients = new Set();
const profileApplyJobs = new Map();
const activeProfileApplyJobs = new Map();

function publicProfileApplyJob(job) {
  return {
    jobId: job.id,
    profileId: job.profileId,
    state: job.state,
    stage: job.stage,
    message: job.message,
    error: job.error || null,
    result: job.result || null,
    createdAt: job.createdAt,
    updatedAt: job.updatedAt
  };
}

function updateProfileApplyJob(job, patch = {}) {
  Object.assign(job, patch, { updatedAt: Date.now() });
}

function startProfileApplyJob(item) {
  const existingId = activeProfileApplyJobs.get(item.id);
  const existing = existingId ? profileApplyJobs.get(existingId) : null;
  if (existing && existing.state === 'running') return publicProfileApplyJob(existing);

  const id = crypto.randomBytes(10).toString('hex');
  const now = Date.now();
  const job = {
    id, profileId: item.id, state: 'running', stage: 'queued',
    message: '准备应用配置…', error: null, result: null,
    createdAt: now, updatedAt: now
  };
  profileApplyJobs.set(id, job);
  activeProfileApplyJobs.set(item.id, id);

  setImmediate(async () => {
    try {
      const system = await activateProfile(item, true, {
        applyTimeoutMs: 120000,
        onStage(stage, message) { updateProfileApplyJob(job, { stage, message }); }
      });
      updateProfileApplyJob(job, {
        state: 'done', stage: 'done', message: '配置已应用',
        result: { target: system?.target || null, backup: system?.backup || null, validation: system?.validation || null }
      });
    } catch (err) {
      updateProfileApplyJob(job, { state: 'failed', stage: 'failed', message: '应用失败', error: err?.message || String(err) });
      item.lastError = err?.message || String(err);
      await writeJson(PROFILES_FILE, profilesState).catch(() => {});
    } finally {
      if (activeProfileApplyJobs.get(item.id) === id) activeProfileApplyJobs.delete(item.id);
      const timer = setTimeout(() => profileApplyJobs.delete(id), 10 * 60 * 1000);
      timer.unref?.();
    }
  });
  return publicProfileApplyJob(job);
}

async function ensureDirs() {
  for (const d of [ETC_DIR, VAR_DIR, PROFILE_DIR, BACKUP_DIR, CORE_STAGE_DIR]) await fsp.mkdir(d, { recursive: true });
}

async function log(message) {
  const line = `${new Date().toISOString()} ${message}\n`;
  try { await fsp.appendFile(APP_LOG, line); } catch (_) {}
}

function normalizeMihomoLogLevel(input) {
  const value = String(input || 'info').trim().toLowerCase();
  if (value === 'warn') return 'warning';
  return ['debug', 'info', 'warning', 'error'].includes(value) ? value : 'info';
}

function mihomoLogLevelRank(input) {
  return ({ debug: 10, info: 20, warning: 30, error: 40 })[normalizeMihomoLogLevel(input)] || 20;
}

function mihomoLogMatchesLevel(recordLevel, selectedLevel) {
  return mihomoLogLevelRank(recordLevel) >= mihomoLogLevelRank(selectedLevel);
}

function normalizeMihomoLogRecord(rawLine) {
  const line = String(rawLine || '').trim();
  if (!line) return null;
  let raw;
  try { raw = JSON.parse(line); } catch (_) { raw = { payload: line }; }
  if (!raw || typeof raw !== 'object') raw = { payload: String(raw ?? '') };
  return {
    time: raw.time || new Date().toISOString(),
    level: normalizeMihomoLogLevel(raw.level || raw.type || 'info'),
    message: String(raw.message ?? raw.payload ?? line)
  };
}

async function appendMihomoLogRecord(record) {
  const line = `${JSON.stringify(record)}\n`;
  mihomoLogWriteQueue = mihomoLogWriteQueue.then(async () => {
    await fsp.appendFile(MIHOMO_LOG_FILE, line, { mode: 0o600 });
    const stat = await fsp.stat(MIHOMO_LOG_FILE).catch(() => null);
    if (!stat || stat.size <= MAX_MIHOMO_LOG_BYTES) return;

    const keepBytes = Math.min(MIHOMO_LOG_TRIM_TARGET, stat.size);
    const fd = await fsp.open(MIHOMO_LOG_FILE, 'r');
    try {
      const buffer = Buffer.alloc(keepBytes);
      await fd.read(buffer, 0, keepBytes, Math.max(0, stat.size - keepBytes));
      const firstNewline = buffer.indexOf(0x0a);
      const retained = firstNewline >= 0 ? buffer.subarray(firstNewline + 1) : buffer;
      await writeAtomic(MIHOMO_LOG_FILE, retained);
    } finally {
      await fd.close().catch(() => {});
    }
  }).catch(() => {});
  return mihomoLogWriteQueue;
}

async function readMihomoLogHistory(level = 'info', limit = 800) {
  await mihomoLogWriteQueue.catch(() => {});
  let raw = '';
  try { raw = await fsp.readFile(MIHOMO_LOG_FILE, 'utf8'); } catch (_) {}
  const selected = normalizeMihomoLogLevel(level);
  const items = [];
  for (const line of raw.split(/\r?\n/)) {
    if (!line.trim()) continue;
    try {
      const record = JSON.parse(line);
      if (record && mihomoLogMatchesLevel(record.level, selected)) items.push(record);
    } catch (_) {}
  }
  const safeLimit = Math.max(1, Math.min(2000, Number(limit) || 800));
  const stat = await fsp.stat(MIHOMO_LOG_FILE).catch(() => ({ size: 0 }));
  return { items: items.slice(-safeLimit), size: Number(stat.size || 0), maxBytes: MAX_MIHOMO_LOG_BYTES };
}

async function clearMihomoLogHistory() {
  mihomoLogWriteQueue = mihomoLogWriteQueue.then(() => fsp.writeFile(MIHOMO_LOG_FILE, '', { mode: 0o600 })).catch(() => {});
  await mihomoLogWriteQueue;
}

function broadcastMihomoLog(record) {
  const payload = `data: ${JSON.stringify(record)}\n\n`;
  for (const client of [...mihomoLogClients]) {
    if (!mihomoLogMatchesLevel(record.level, client.level)) continue;
    try { client.res.write(payload); } catch (_) { mihomoLogClients.delete(client); }
  }
}

function streamPersistedMihomoLogs(req, res, level = 'info') {
  const client = { res, level: normalizeMihomoLogLevel(level), keepalive: null };
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    'Connection': 'keep-alive',
    'X-Accel-Buffering': 'no'
  });
  res.write(': connected\n\n');
  mihomoLogClients.add(client);
  client.keepalive = setInterval(() => {
    try { res.write(': keepalive\n\n'); } catch (_) {}
  }, 20000);
  client.keepalive.unref?.();
  const close = () => {
    mihomoLogClients.delete(client);
    if (client.keepalive) clearInterval(client.keepalive);
    try { res.end(); } catch (_) {}
  };
  req.once('close', close);
  req.once('aborted', close);
}

function sleepWithSignal(ms, signal) {
  return new Promise(resolve => {
    if (signal?.aborted) return resolve();
    const timer = setTimeout(done, ms);
    timer.unref?.();
    function done() {
      signal?.removeEventListener?.('abort', done);
      clearTimeout(timer);
      resolve();
    }
    signal?.addEventListener?.('abort', done, { once: true });
  });
}

async function collectMihomoLogs(signal) {
  while (!signal.aborted) {
    try {
      const controller = normalizeController(settings.controller);
      const upstream = await fetch(`${controller}/logs?level=debug&format=structured`, {
        headers: authHeaders(),
        signal
      });
      if (upstream.ok) {
        const reader = upstream.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';
        while (!signal.aborted) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let idx;
          while ((idx = buffer.indexOf('\n')) >= 0) {
            const line = buffer.slice(0, idx).trim();
            buffer = buffer.slice(idx + 1);
            const record = normalizeMihomoLogRecord(line);
            if (!record) continue;
            await appendMihomoLogRecord(record);
            broadcastMihomoLog(record);
          }
        }
      }
    } catch (err) {
      if (signal.aborted) break;
    }
    await sleepWithSignal(1500, signal);
  }
}

function restartMihomoLogCollector() {
  if (mihomoLogCollectorAbort) mihomoLogCollectorAbort.abort();
  const ac = new AbortController();
  mihomoLogCollectorAbort = ac;
  collectMihomoLogs(ac.signal).catch(() => {});
}

function stopMihomoLogCollector() {
  if (mihomoLogCollectorAbort) mihomoLogCollectorAbort.abort();
  mihomoLogCollectorAbort = null;
  for (const client of [...mihomoLogClients]) {
    if (client.keepalive) clearInterval(client.keepalive);
    try { client.res.end(); } catch (_) {}
  }
  mihomoLogClients.clear();
}

async function readJson(file, fallback) {
  try { return JSON.parse(await fsp.readFile(file, 'utf8')); } catch (_) { return fallback; }
}

async function writeAtomic(file, content) {
  const tmp = `${file}.${process.pid}.${Date.now()}.tmp`;
  await fsp.writeFile(tmp, content, { mode: 0o600 });
  await fsp.rename(tmp, file);
}

async function writeJson(file, value) {
  await writeAtomic(file, JSON.stringify(value, null, 2));
}

async function managedProxyGroupOrder() {
  try {
    const raw = await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8');
    return parseProxyGroupOrder(raw);
  } catch (_) { return []; }
}

async function getProxyGroupOrder() {
  try {
    const startup = await privilegedRequest('/config/proxy-group-order', null, { method: 'GET', timeoutMs: 10000 });
    if (Array.isArray(startup?.order) && startup.order.length) {
      return { order: startup.order, source: 'startup', path: startup.configPath || null };
    }
  } catch (_) {}
  const managed = await managedProxyGroupOrder();
  if (managed.length) return { order: managed, source: 'managed', path: MANAGED_CONFIG_FILE };
  return { order: [], source: 'api', path: null };
}

async function orderedProxiesPayload() {
  const data = (await mihomoFetch('/proxies')).data || {};
  const meta = await getProxyGroupOrder();
  return { ...data, groupOrder: meta.order, groupOrderSource: meta.source, groupOrderPath: meta.path };
}

function sanitizeSettingsForClient() {
  return {
    controller: settings.controller,
    hasSecret: Boolean(settings.secret),
    controllerAutoDetect: settings.controllerAutoDetect !== false,
    persistSelections: settings.persistSelections !== false,
    applyManagedConfigOnStart: settings.applyManagedConfigOnStart !== false,
    healthcheckUrl: settings.healthcheckUrl,
    healthcheckTimeout: settings.healthcheckTimeout
  };
}

function normalizeController(input) {
  const value = String(input || '').trim().replace(/\/$/, '');
  const u = new URL(value);
  if (!['http:', 'https:'].includes(u.protocol)) throw new Error('控制器地址只支持 http/https');
  return u.toString().replace(/\/$/, '');
}

function authHeaders(extra = {}) {
  /** @type {Record<string, string>} */
  const headers = { ...extra };
  if (settings.secret) headers.Authorization = `Bearer ${settings.secret}`;
  return headers;
}

async function readResponse(resp) {
  const text = await resp.text();
  if (!text) return null;
  try { return JSON.parse(text); } catch (_) { return text; }
}

async function mihomoFetch(apiPath, options = {}, timeoutMs = 12000) {
  const controller = normalizeController(settings.controller);
  const url = `${controller}${apiPath.startsWith('/') ? apiPath : `/${apiPath}`}`;
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(new Error('请求 Mihomo 超时')), timeoutMs);
  try {
    const resp = await fetch(url, {
      ...options,
      signal: ac.signal,
      headers: authHeaders(options.headers || {})
    });
    const data = await readResponse(resp);
    if (!resp.ok) {
      const message = data && typeof data === 'object' ? (data.message || JSON.stringify(data)) : (data || resp.statusText);
      throw Object.assign(new Error(`Mihomo ${resp.status}: ${message}`), { statusCode: resp.status });
    }
    return { status: resp.status, data, headers: resp.headers };
  } finally {
    clearTimeout(timer);
  }
}


let ruleProviderUpdateQueue = Promise.resolve();
function enqueueRuleProviderUpdate(task) {
  const run = ruleProviderUpdateQueue.then(task, task);
  ruleProviderUpdateQueue = run.catch(() => {});
  return run;
}

async function patchRuntimeMode(mode) {
  await mihomoFetch('/configs', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ mode })
  }, 8000);
}

async function restoreRuntimeMode(mode) {
  let lastError = null;
  for (let i = 0; i < 2; i++) {
    try {
      await patchRuntimeMode(mode);
      return;
    } catch (err) {
      lastError = err;
      if (i === 0) await new Promise(resolve => setTimeout(resolve, 300));
    }
  }
  throw lastError || new Error('恢复 Mihomo 运行模式失败');
}

async function updateRuleProviderWithDirectFallback(name) {
  return enqueueRuleProviderUpdate(async () => {
    const providerPath = `/providers/rules/${encodeURIComponent(name)}`;
    try {
      await mihomoFetch(providerPath, { method: 'PUT' }, 30000);
      return { ok: true, method: 'normal' };
    } catch (primaryError) {
      await log(`[Rule Provider] ${name} 常规更新失败：${primaryError.message}；准备直连兜底`);

      let previousMode = null;
      let switched = false;
      let directError = null;
      let directSucceeded = false;
      let restoreError = null;

      try {
        const runtime = (await mihomoFetch('/configs', {}, 8000)).data || {};
        previousMode = String(runtime.mode || 'rule').toLowerCase();
        if (previousMode !== 'direct') {
          await patchRuntimeMode('direct');
          switched = true;
          await log(`[Rule Provider] ${name} 临时切换 Mihomo mode=direct 进行最后一次尝试`);
        }
        await mihomoFetch(providerPath, { method: 'PUT' }, 30000);
        directSucceeded = true;
      } catch (err) {
        directError = err;
      } finally {
        if (switched && previousMode) {
          try {
            await restoreRuntimeMode(previousMode);
            await log(`[Rule Provider] ${name} 已恢复 Mihomo mode=${previousMode}`);
          } catch (err) {
            restoreError = err;
            await log(`[Rule Provider] ${name} 恢复 Mihomo mode=${previousMode} 失败：${err.message}`);
          }
        }
      }

      if (restoreError) {
        throw Object.assign(new Error(directSucceeded
          ? `Rule Provider ${name} 已通过直连更新，但恢复原运行模式失败：${restoreError.message}`
          : `Rule Provider ${name} 更新失败，且恢复原运行模式失败：${restoreError.message}`), { statusCode: 500 });
      }
      if (directSucceeded) {
        await log(`[Rule Provider] ${name} 直连兜底更新成功`);
        return { ok: true, method: 'direct-fallback', initialError: primaryError.message };
      }

      throw Object.assign(new Error(`Rule Provider ${name} 更新失败：常规尝试：${primaryError.message}；直连兜底：${directError?.message || '失败'}`), {
        statusCode: directError?.statusCode || primaryError?.statusCode || 502
      });
    }
  });
}


function privilegedRequest(apiPath, payload = null, options = {}) {
  const method = options.method || (payload === null ? 'GET' : 'POST');
  const timeoutMs = Math.max(1000, Number(options.timeoutMs || 90000));
  const body = payload === null ? null : JSON.stringify(payload);
  return new Promise((resolve, reject) => {
    const req = http.request({
      socketPath: PRIV_SOCKET_PATH,
      path: apiPath,
      method,
      headers: body ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) } : {},
      timeout: timeoutMs
    }, res => {
      const chunks = [];
      let size = 0;
      res.on('data', chunk => {
        size += chunk.length;
        if (size > MAX_BODY + 2 * 1024 * 1024) {
          req.destroy(new Error('特权助手响应过大'));
          return;
        }
        chunks.push(chunk);
      });
      res.on('end', () => {
        const raw = Buffer.concat(chunks).toString('utf8');
        let data;
        try { data = raw ? JSON.parse(raw) : {}; } catch (_) { data = { error: raw || `HTTP ${res.statusCode}` }; }
        if ((res.statusCode || 500) < 200 || (res.statusCode || 500) >= 300) {
          reject(Object.assign(new Error(data?.error || `特权操作失败: HTTP ${res.statusCode}`), { statusCode: res.statusCode }));
          return;
        }
        resolve(data);
      });
    });
    req.on('timeout', () => req.destroy(new Error('特权操作超时')));
    req.on('error', err => reject(Object.assign(new Error(`特权助手不可用: ${err.message}`), { cause: err })));
    if (body) req.write(body);
    req.end();
  });
}

async function privilegedStatus() {
  try { return await privilegedRequest('/status', null, { method: 'GET', timeoutMs: 10000 }); }
  catch (err) { return { privileged: false, available: false, error: err.message }; }
}


let controllerSettingsSyncAt = 0;
async function syncControllerSettings(force = false) {
  const now = Date.now();
  if (!force && now - controllerSettingsSyncAt < 3000) return null;
  controllerSettingsSyncAt = now;
  const sys = await privilegedStatus();
  let changed = false;

  if (sys?.mode === 'managed' && sys.managedController) {
    const controller = normalizeController(sys.managedController);
    if (settings.controller !== controller) { settings.controller = controller; changed = true; }
    if (sys.managedSecret !== undefined && settings.secret !== String(sys.managedSecret || '')) {
      settings.secret = String(sys.managedSecret || ''); changed = true;
    }
  } else if (sys?.mode === 'external' && settings.controllerAutoDetect !== false && sys.detectedController) {
    const controller = normalizeController(sys.detectedController);
    if (settings.controller !== controller) { settings.controller = controller; changed = true; }
    if (sys.detectedSecretPresent === true && settings.secret !== String(sys.detectedSecret || '')) {
      settings.secret = String(sys.detectedSecret || ''); changed = true;
    }
  }

  if (changed) {
    await writeJson(SETTINGS_FILE, settings);
    restartMihomoLogCollector();
  }
  return sys;
}

async function applyPayload(payload, timeoutMs = 30000) {
  if (!payload || !String(payload).trim()) throw new Error('配置内容为空');
  return mihomoFetch('/configs?force=true', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: '', payload: String(payload) })
  }, Math.max(5000, Number(timeoutMs || 30000)));
}

async function waitForControllerOnline(timeoutMs = 30000) {
  const deadline = Date.now() + Math.max(3000, Number(timeoutMs || 30000));
  let lastError = null;
  while (Date.now() < deadline) {
    try {
      const version = (await mihomoFetch('/version', {}, 3500)).data;
      if (version) return version;
    } catch (err) { lastError = err; }
    await new Promise(resolve => setTimeout(resolve, 500));
  }
  throw new Error(`Mihomo Controller 未在限定时间内恢复${lastError ? `：${lastError.message}` : ''}`);
}


async function configPathAllowed(input) {
  const value = String(input || '').trim();
  if (!value || value.length > 4096 || value.includes('\0')) throw Object.assign(new Error('配置路径无效'), { statusCode: 400 });
  if (!path.isAbsolute(value)) throw Object.assign(new Error('请输入 NAS 上的绝对路径'), { statusCode: 400 });
  const normalized = path.normalize(value);
  if (!/\.(ya?ml)$/i.test(normalized)) throw Object.assign(new Error('只允许 YAML/YML 配置文件'), { statusCode: 400 });
  if (/^(\/proc|\/sys|\/dev)(\/|$)/.test(normalized)) throw Object.assign(new Error('不能手动读取该系统路径'), { statusCode: 400 });
  if (isKnownSystemMihomoConfig(normalized) || await isAuthorizedUserPath(normalized)) return normalized;
  throw Object.assign(new Error('该文件不在 fnOS 已授权目录中。请先在“系统设置 → 应用 → Clash for fnos → 访问权限”授权所在文件夹'), { statusCode: 403 });
}

async function fileReadable(file) {
  try { await fsp.access(file, fs.constants.R_OK); return true; } catch (_) { return false; }
}

async function processInfo(pid) {
  try {
    const cmd = await fsp.readFile(`/proc/${pid}/cmdline`);
    const args = cmd.toString('utf8').split('\0').filter(Boolean);
    if (!args.length) return null;
    const transient = args.slice(1).some(arg => ['-t', '--test', '-v', '--version', '-h', '--help'].includes(String(arg).toLowerCase()));
    if (transient) return null;
    let exe = '';
    let cwd = '';
    try { exe = await fsp.readlink(`/proc/${pid}/exe`); } catch (_) {}
    try { cwd = await fsp.readlink(`/proc/${pid}/cwd`); } catch (_) {}
    const base = path.basename(exe || args[0] || '').toLowerCase();
    const argv0 = path.basename(args[0] || '').toLowerCase();
    const joined = `${base} ${argv0}`;
    if (!/(^|[^a-z])(mihomo|clash-meta)([^a-z]|$)/i.test(joined)) return null;
    if (/clash-for-fnos/i.test(joined)) return null;
    let uid = null;
    try {
      const status = await fsp.readFile(`/proc/${pid}/status`, 'utf8');
      const match = status.match(/^Uid:\s+(\d+)/m);
      if (match) uid = Number(match[1]);
    } catch (_) {}
    let containerized = false;
    try {
      const cg = await fsp.readFile(`/proc/${pid}/cgroup`, 'utf8');
      containerized = /(docker|containerd|kubepods|lxc)/i.test(cg);
    } catch (_) {}
    return { pid: Number(pid), exe: exe || args[0], cwd, args, uid, containerized };
  } catch (_) { return null; }
}

function sanitizeProcessArgs(args) {
  const out = [];
  let redactNext = false;
  for (const raw of args || []) {
    const arg = String(raw);
    if (redactNext) { out.push('******'); redactNext = false; continue; }
    if (/^(--?|\/)(secret|password|token)(=|$)/i.test(arg) || /secret=/i.test(arg)) {
      const eq = arg.indexOf('=');
      if (eq >= 0) out.push(`${arg.slice(0, eq + 1)}******`);
      else { out.push(arg); redactNext = true; }
      continue;
    }
    out.push(arg);
  }
  return out;
}

function optionValue(args, names) {
  for (let i = 1; i < args.length; i++) {
    const arg = args[i];
    for (const name of names) {
      if (arg === name && i + 1 < args.length) return args[i + 1];
      if (arg.startsWith(`${name}=`)) return arg.slice(name.length + 1);
    }
  }
  return null;
}

function resolveProcessArg(value, proc, baseDir = null) {
  if (!value) return null;
  if (path.isAbsolute(value)) return path.normalize(value);
  return path.resolve(baseDir || proc.cwd || '/', value);
}

async function passwdHomeForUid(uid) {
  if (!Number.isInteger(uid)) return null;
  try {
    const passwd = await fsp.readFile('/etc/passwd', 'utf8');
    for (const line of passwd.split('\n')) {
      const parts = line.split(':');
      if (parts.length >= 6 && Number(parts[2]) === uid) return parts[5] || null;
    }
  } catch (_) {}
  return uid === 0 ? '/root' : null;
}

async function inspectCandidate(displayPath, actualPath, source, proc = null, namespace = 'host', accessScope = 'system') {
  const result = {
    token: '', path: displayPath, source, namespace, accessScope,
    exists: false, readable: false, size: 0, mtime: null, realPath: null, viaHelper: false,
    process: proc ? { pid: proc.pid, exe: proc.exe, args: sanitizeProcessArgs(proc.args), containerized: proc.containerized } : null
  };

  // System Mihomo configs can intentionally be root-owned. Inspect them through
  // the restricted root helper instead of treating package-user EACCES as a
  // missing permission. The helper independently validates that the requested
  // path is the active/managed config or one of the supported system paths.
  if (accessScope === 'system' && namespace === 'host') {
    try {
      const info = await privilegedRequest('/config/inspect-path', { path: displayPath }, { timeoutMs: 10000 });
      result.exists = Boolean(info?.exists);
      result.readable = Boolean(info?.readable);
      result.permissionDenied = Boolean(info?.permissionDenied);
      result.size = Number(info?.size || 0);
      result.mtime = info?.mtime ?? null;
      result.realPath = info?.realPath ? path.normalize(String(info.realPath)) : null;
      result.viaHelper = result.exists && result.readable;
      if (result.exists) {
        const identity = `${result.realPath || path.normalize(displayPath)}|${info?.dev || ''}|${info?.ino || ''}|${result.mtime || ''}`;
        const token = crypto.createHash('sha256').update(identity).digest('hex').slice(0, 24);
        result.token = token;
        localConfigScanCache.set(token, { ...result, actualPath: displayPath });
      }
      return result;
    } catch (_) {
      // Not every scanned @app* YAML is a Mihomo system config. Fall through
      // to package-user inspection for non-privileged candidates.
    }
  }

  try {
    const st = await fsp.stat(actualPath);
    if (!st.isFile()) return result;
    result.exists = true;
    result.size = st.size;
    result.mtime = st.mtimeMs;
    result.realPath = await fsp.realpath(actualPath).catch(() => path.resolve(actualPath));
    result.readable = await fileReadable(actualPath);
    const token = crypto.createHash('sha256').update(`${result.realPath || actualPath}|${st.dev}|${st.ino}|${st.mtimeMs}`).digest('hex').slice(0, 24);
    result.token = token;
    localConfigScanCache.set(token, { ...result, actualPath });
  } catch (err) {
    if (err && (err.code === 'EACCES' || err.code === 'EPERM')) result.permissionDenied = true;
  }
  return result;
}

async function scanNamedDirs(root, maxDepth = 2) {
  const found = [];
  async function walk(dir, depth) {
    if (depth > maxDepth || found.length >= 30) return;
    let entries;
    try { entries = await fsp.readdir(dir, { withFileTypes: true }); } catch (_) { return; }
    for (const e of entries) {
      if (found.length >= 30) break;
      const full = path.join(dir, e.name);
      if (e.isFile() && /^config\.ya?ml$/i.test(e.name)) found.push(full);
      else if (e.isDirectory()) {
        const interesting = /(mihomo|clash)/i.test(e.name);
        if (interesting || depth > 0) await walk(full, depth + 1);
      }
    }
  }
  await walk(root, 0);
  return found;
}

async function discoverLocalConfigs() {
  localConfigScanCache = new Map();
  detectedMihomoConfigPaths = new Set();
  const processes = [];
  const candidateSpecs = [];
  let procEntries = [];
  try { procEntries = (await fsp.readdir('/proc')).filter(x => /^\d+$/.test(x)); } catch (_) {}
  for (const pid of procEntries) {
    const proc = await processInfo(pid);
    if (!proc) continue;
    processes.push(proc);
    const dirArg = optionValue(proc.args, ['-d', '--dir', '--home-dir']);
    const fileArg = optionValue(proc.args, ['-f', '--config', '--config-file', '-config']);
    const configDir = resolveProcessArg(dirArg, proc);
    const processPaths = [];
    if (fileArg) {
      processPaths.push(resolveProcessArg(fileArg, proc, configDir));
      if (configDir && !path.isAbsolute(fileArg)) processPaths.push(path.resolve(configDir, fileArg));
    } else if (configDir) processPaths.push(path.join(configDir, 'config.yaml'));
    const home = await passwdHomeForUid(proc.uid);
    if (!fileArg && !configDir && home) processPaths.push(path.join(home, '.config/mihomo/config.yaml'));
    for (const displayPath of [...new Set(processPaths.filter(Boolean))]) {
      detectedMihomoConfigPaths.add(path.normalize(displayPath));
      candidateSpecs.push({ displayPath, actualPath: displayPath, accessScope: 'system', source: fileArg ? '运行进程 -f/--config' : dirArg ? '运行进程 -d/--dir' : '运行用户默认目录', proc, namespace: 'host' });
      if (path.isAbsolute(displayPath)) {
        candidateSpecs.push({ displayPath, actualPath: `/proc/${proc.pid}/root${displayPath}`, accessScope: 'system', source: `${fileArg ? '运行进程配置' : '运行目录配置'}（进程根目录）`, proc, namespace: 'process-root' });
      }
    }
  }

  const common = [
    '/etc/mihomo/config.yaml', '/etc/clash/config.yaml',
    '/usr/local/etc/mihomo/config.yaml', '/usr/local/etc/clash/config.yaml',
    '/opt/mihomo/config.yaml', '/var/lib/mihomo/config.yaml',
    '/root/.config/mihomo/config.yaml', '/root/.config/clash/config.yaml'
  ];
  common.forEach(p => candidateSpecs.push({ displayPath: p, actualPath: p, accessScope: 'system', source: '常见路径', proc: null, namespace: 'host' }));

  let vols = [];
  try { vols = (await fsp.readdir('/', { withFileTypes: true })).filter(e => e.isDirectory() && /^vol\d+$/i.test(e.name)).map(e => `/${e.name}`); } catch (_) {}
  for (const vol of vols.slice(0, 8)) {
    for (const sub of ['@appdata', '@appconf', '@appcenter']) {
      for (const file of await scanNamedDirs(path.join(vol, sub), 2)) candidateSpecs.push({ displayPath: file, actualPath: file, accessScope: 'system', source: `fnOS ${sub}`, proc: null, namespace: 'host' });
    }
  }

  const authorizedRoots = await currentAuthorizedPaths();
  for (const root of authorizedRoots) {
    for (const file of await scanAuthorizedYamlFiles(root)) {
      candidateSpecs.push({ displayPath: file, actualPath: file, accessScope: 'authorized', source: '用户授权目录', proc: null, namespace: 'host' });
    }
  }

  const seen = new Set();
  const candidates = [];
  for (const spec of candidateSpecs) {
    const key = `${spec.actualPath}|${spec.namespace}`;
    if (seen.has(key)) continue;
    seen.add(key);
    const candidate = await inspectCandidate(spec.displayPath, spec.actualPath, spec.source, spec.proc, spec.namespace, spec.accessScope || 'system');
    if (candidate.exists || candidate.permissionDenied || spec.proc) candidates.push(candidate);
  }
  // Deduplicate by the underlying file, not by where it was discovered.
  // A config discovered from both "-d/--dir" and /proc/<pid>/root must appear
  // only once. When realpath is unavailable, the normalized display path is
  // still enough to collapse duplicate scan sources for the same host path.
  const canonical = new Map();
  const score = x => (x.process ? 100 : 0) + (x.readable ? 20 : 0) + (x.exists ? 10 : 0) + (x.namespace === 'host' ? 2 : 0) + (x.viaHelper ? 1 : 0);
  for (const candidate of candidates) {
    const key = path.normalize(String(candidate.realPath || candidate.path || ''));
    const previous = canonical.get(key);
    if (!previous || score(candidate) > score(previous)) canonical.set(key, candidate);
  }
  const deduped = [...canonical.values()];
  deduped.sort((a, b) => score(b) - score(a) || String(a.path).localeCompare(String(b.path)));
  return {
    processes: processes.map(p => ({ pid: p.pid, exe: p.exe, cwd: p.cwd, args: sanitizeProcessArgs(p.args), containerized: p.containerized })),
    candidates: deduped,
    authorizedPaths: authorizedRoots,
    scannedAt: Date.now()
  };
}

async function checkManualLocalConfig(input) {
  const file = await configPathAllowed(input);
  const accessScope = await isAuthorizedUserPath(file) ? 'authorized' : 'system';
  const candidate = await inspectCandidate(file, file, accessScope === 'authorized' ? '用户授权路径' : '系统 Mihomo 路径', null, 'host', accessScope);
  if (!candidate.exists) throw Object.assign(new Error('没有找到这个配置文件'), { statusCode: 404 });
  return candidate;
}

async function readCandidateContent(token) {
  const candidate = localConfigScanCache.get(String(token || ''));
  if (!candidate) throw Object.assign(new Error('配置扫描结果已失效，请重新扫描'), { statusCode: 409 });
  if (candidate.accessScope === 'authorized' && !(await isAuthorizedUserPath(candidate.actualPath))) {
    throw Object.assign(new Error('该文件已不在 fnOS 授权目录中，请重新授权并扫描'), { statusCode: 403 });
  }

  if (candidate.accessScope === 'system' && candidate.namespace === 'host') {
    let helperData;
    let helperError;
    try {
      helperData = await privilegedRequest('/config/read-path', { path: candidate.actualPath }, { timeoutMs: 15000 });
    } catch (err) {
      helperError = err;
    }
    if (helperError && candidate.viaHelper) throw helperError;
    if (helperData) {
      const raw = String(helperData?.content || '');
      const validationError = !raw.trim()
        ? new Error('配置文件为空')
        : raw.includes('\0')
          ? new Error('配置文件不是文本 YAML')
          : Buffer.byteLength(raw) > MAX_REMOTE_CONFIG
            ? Object.assign(new Error('配置文件过大'), { statusCode: 413 })
            : null;
      if (!validationError) return { raw, candidate };
      if (candidate.viaHelper) throw validationError;
    }
  }

  let st;
  try { st = await fsp.stat(candidate.actualPath); } catch (err) {
    if (err.code === 'EACCES' || err.code === 'EPERM') throw Object.assign(new Error('Clash for fnos 没有权限读取该配置，请调整文件读取权限'), { statusCode: 403 });
    throw Object.assign(new Error('配置文件已经不存在，请重新扫描'), { statusCode: 404 });
  }
  if (!st.isFile()) throw Object.assign(new Error('目标不是文件'), { statusCode: 400 });
  if (st.size <= 0) throw Object.assign(new Error('配置文件为空'), { statusCode: 400 });
  if (st.size > MAX_REMOTE_CONFIG) throw Object.assign(new Error('配置文件过大'), { statusCode: 413 });
  let raw;
  try {
    raw = await fsp.readFile(candidate.actualPath, 'utf8');
  } catch (err) {
    if (err.code === 'EACCES' || err.code === 'EPERM') throw Object.assign(new Error('Clash for fnos 没有权限读取该配置，请调整文件读取权限'), { statusCode: 403 });
    throw err;
  }
  if (!raw.trim()) throw new Error('配置文件为空');
  if (raw.includes('\0')) throw new Error('配置文件不是文本 YAML');
  return { raw, candidate };
}

async function importLocalCandidate(token, options = {}) {
  const { raw, candidate } = await readCandidateContent(token);
  const apply = Boolean(options.apply);
  if (apply) await applyPayload(raw);
  await backupManagedConfig();
  await writeAtomic(MANAGED_CONFIG_FILE, raw);
  const now = Date.now();
  await writeJson(CONFIG_META_FILE, {
    source: 'nas-local', path: candidate.path, namespace: candidate.namespace,
    importedAt: now, appliedAt: apply ? now : null, active: apply
  });
  let item = profilesState.items.find(x => (x.type || '') === 'local' && x.sourcePath === candidate.path);
  if (!item) {
    item = {
      id: crypto.randomBytes(6).toString('hex'),
      name: String(options.name || path.basename(path.dirname(candidate.path)) || '本机配置'),
      url: '', type: 'local', sourcePath: candidate.path,
      autoUpdate: false, autoApply: false, intervalMinutes: 0, updatedAt: now, lastError: null
    };
    profilesState.items.push(item);
  } else {
    item.name = String(options.name || item.name || '本机配置');
    item.updatedAt = now;
    item.lastError = null;
  }
  await writeAtomic(path.join(PROFILE_DIR, `${item.id}.yaml`), raw);
  if (apply) profilesState.current = item.id;
  await writeJson(PROFILES_FILE, profilesState);
  if (apply) setTimeout(() => restoreSelections().catch(() => {}), 1200);
  await log(`已从本机配置导入 ${candidate.path}${apply ? ' 并应用' : ''}`);
  return { item: publicProfile(item), applied: apply, sourcePath: candidate.path };
}

async function backupManagedConfig() {
  try {
    const old = await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8');
    const stamp = new Date().toISOString().replace(/[:.]/g, '-');
    const file = path.join(BACKUP_DIR, `${stamp}.yaml`);
    await fsp.writeFile(file, old, { mode: 0o600 });
    const files = (await fsp.readdir(BACKUP_DIR)).filter(f => f.endsWith('.yaml')).sort().reverse();
    for (const stale of files.slice(20)) await fsp.unlink(path.join(BACKUP_DIR, stale)).catch(() => {});
  } catch (_) {}
}

async function saveAndApplyManagedConfig(payload) {
  await applyPayload(payload);
  await backupManagedConfig();
  await writeAtomic(MANAGED_CONFIG_FILE, String(payload));
  const meta = await readJson(CONFIG_META_FILE, {});
  await writeJson(CONFIG_META_FILE, { ...meta, active: true, appliedAt: Date.now() });
  setTimeout(() => restoreSelections().catch(() => {}), 1200);
}


async function saveApplyAndSyncStartupConfig(payload, meta = {}, options = {}) {
  const onStage = typeof options.onStage === 'function' ? options.onStage : () => {};
  onStage('validating', '校验并写入启动配置…');
  const sync = await privilegedRequest('/config/sync', { content: String(payload) }, { timeoutMs: 60000 });
  const effective = String(sync.effectiveContent || payload);
  let activation = null;
  let reloadWarning = null;
  try {
    onStage('activating', '正在让 Mihomo 使用新配置…');
    activation = await privilegedRequest('/config/activate', { txId: sync.txId }, { timeoutMs: 60000 });
    if (activation?.method === 'hot-reload') {
      onStage('reloading', 'Mihomo 正在热加载配置与 Provider…');
      try {
        await applyPayload(effective, Number(options.applyTimeoutMs || 120000));
      } catch (err) {
        // A controller-side timeout can be caused by slow Provider initialization.
        // Keep the already validated startup file and verify whether the Core is still healthy
        // instead of immediately restoring the old file.
        reloadWarning = err?.message || String(err);
      }
    } else {
      onStage('restarting', activation?.method === 'managed-restart' ? '正在重启托管 Mihomo Core…' : '正在重启 Mihomo 服务…');
    }

    onStage('confirming', '正在确认 Mihomo Controller…');
    await waitForControllerOnline(Number(options.confirmTimeoutMs || 30000));
  } catch (err) {
    onStage('rollback', '新配置启动失败，正在恢复旧配置…');
    const rollback = await privilegedRequest('/config/rollback', { txId: sync.txId }, { timeoutMs: 60000 }).catch(() => null);
    const suffix = rollback?.restartError ? `；回滚后重启失败：${rollback.restartError}` : '';
    throw new Error(`新配置未能正常运行，已尝试回滚：${err.message}${suffix}`);
  }

  onStage('saving', '保存配置状态…');
  await backupManagedConfig();
  // Keep the profile snapshot clean. Local Controller/port/TUN overrides are
  // injected only into the real startup config and runtime.
  await writeAtomic(MANAGED_CONFIG_FILE, String(payload));
  const previous = await readJson(CONFIG_META_FILE, {});
  await writeJson(CONFIG_META_FILE, {
    ...previous,
    ...meta,
    active: true,
    appliedAt: Date.now(),
    startupSyncedAt: Date.now(),
    startupConfigPath: sync.target,
    startupBackupPath: sync.backup,
    activationMethod: activation?.method || 'unknown',
    reloadWarning: reloadWarning || null
  });
  await privilegedRequest('/config/commit', { txId: sync.txId }, { timeoutMs: 10000 }).catch(() => {});
  setTimeout(() => restoreSelections().catch(() => {}), 1200);
  await log(`已应用启动配置：${sync.target}，方式：${activation?.method || 'unknown'}${reloadWarning ? `，热加载提示：${reloadWarning}` : ''}`);
  return { target: sync.target, backup: sync.backup, validation: sync.validation, activation, reloadWarning };
}

function parseSubscriptionUserInfo(value) {
  if (!value) return null;
  const result = {};
  for (const part of String(value).split(';')) {
    const idx = part.indexOf('=');
    if (idx < 0) continue;
    const key = part.slice(0, idx).trim().toLowerCase();
    const raw = part.slice(idx + 1).trim();
    if (!['upload', 'download', 'total', 'expire'].includes(key)) continue;
    const num = Number(raw);
    if (Number.isFinite(num) && num >= 0) result[key] = num;
  }
  if (!Object.keys(result).length) return null;
  result.fetchedAt = Date.now();
  return result;
}

const DEFAULT_SUBSCRIPTION_UA = 'mihomo';

function defaultSubscriptionUserAgent() {
  // Remote subscriptions use a fixed Mihomo User-Agent.
  return DEFAULT_SUBSCRIPTION_UA;
}


function headerObject(headers) {
  const out = {};
  for (const [key, value] of Object.entries(headers || {})) {
    if (value !== undefined && value !== null && value !== '') out[key] = String(value);
  }
  return out;
}

function basicAuthFromUrl(u) {
  if (!u.username) return null;
  const user = decodeURIComponent(u.username || '');
  const pass = decodeURIComponent(u.password || '');
  return `Basic ${Buffer.from(`${user}:${pass}`).toString('base64')}`;
}

function cleanRequestUrl(input) {
  const u = new URL(input);
  if (!['http:', 'https:'].includes(u.protocol)) throw new Error('订阅地址只支持 http/https');
  const authorization = basicAuthFromUrl(u);
  u.username = '';
  u.password = '';
  return { url: u, authorization };
}

function collectNodeResponse(resp, maxBytes = MAX_REMOTE_CONFIG, asBuffer = false) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let total = 0;
    resp.on('data', chunk => {
      total += chunk.length;
      if (total > maxBytes) {
        resp.destroy(new Error('订阅文件过大'));
        return;
      }
      chunks.push(chunk);
    });
    resp.on('end', () => resolve({
      status: resp.statusCode || 0,
      statusText: resp.statusMessage || '',
      headers: resp.headers || {},
      body: asBuffer ? Buffer.concat(chunks) : Buffer.concat(chunks).toString('utf8')
    }));
    resp.on('error', reject);
  });
}

function proxyAuthorization(proxy) {
  if (!proxy.username) return null;
  const user = decodeURIComponent(proxy.username || '');
  const pass = decodeURIComponent(proxy.password || '');
  return `Basic ${Buffer.from(`${user}:${pass}`).toString('base64')}`;
}

function nodeRequestOnce(targetInput, options = {}) {
  const { url: target, authorization } = cleanRequestUrl(targetInput);
  const headers = headerObject(options.headers || {});
  if (authorization) headers.Authorization = authorization;
  const timeoutMs = Math.max(1000, Number(options.timeoutMs || 20000));
  const proxyUrl = options.proxyUrl ? new URL(options.proxyUrl) : null;

  return new Promise((resolve, reject) => {
    let settled = false;
    const done = (err, value) => {
      if (settled) return;
      settled = true;
      if (err) reject(err); else resolve(value);
    };
    const timeoutError = () => Object.assign(new Error(`请求超时（${Math.round(timeoutMs / 1000)} 秒）`), { code: 'ETIMEDOUT' });

    if (!proxyUrl) {
      const transport = target.protocol === 'https:' ? https : http;
      const req = transport.request({
        protocol: target.protocol,
        hostname: target.hostname,
        port: target.port || undefined,
        method: 'GET',
        path: `${target.pathname}${target.search}`,
        headers: { Host: target.host, ...headers },
        timeout: timeoutMs
      }, async resp => {
        try { done(null, await collectNodeResponse(resp, options.maxBytes || MAX_REMOTE_CONFIG, Boolean(options.binary))); } catch (err) { done(err); }
      });
      req.on('timeout', () => req.destroy(timeoutError()));
      req.on('error', err => done(err));
      req.end();
      return;
    }

    if (!['http:', 'https:'].includes(proxyUrl.protocol)) {
      done(Object.assign(new Error(`暂不支持 ${proxyUrl.protocol} 系统代理`), { code: 'UNSUPPORTED_PROXY' }));
      return;
    }

    const proxyHeaders = {};
    const proxyAuth = proxyAuthorization(proxyUrl);
    if (proxyAuth) proxyHeaders['Proxy-Authorization'] = proxyAuth;
    const proxyTransport = proxyUrl.protocol === 'https:' ? https : http;

    if (target.protocol === 'http:') {
      const req = proxyTransport.request({
        protocol: proxyUrl.protocol,
        hostname: proxyUrl.hostname,
        port: proxyUrl.port || (proxyUrl.protocol === 'https:' ? 443 : 80),
        method: 'GET',
        path: target.href,
        headers: { Host: target.host, ...headers, ...proxyHeaders },
        timeout: timeoutMs
      }, async resp => {
        try { done(null, await collectNodeResponse(resp, options.maxBytes || MAX_REMOTE_CONFIG, Boolean(options.binary))); } catch (err) { done(err); }
      });
      req.on('timeout', () => req.destroy(timeoutError()));
      req.on('error', err => done(err));
      req.end();
      return;
    }

    const connectReq = proxyTransport.request({
      protocol: proxyUrl.protocol,
      hostname: proxyUrl.hostname,
      port: proxyUrl.port || (proxyUrl.protocol === 'https:' ? 443 : 80),
      method: 'CONNECT',
      path: `${target.hostname}:${target.port || 443}`,
      headers: { Host: `${target.hostname}:${target.port || 443}`, ...proxyHeaders },
      timeout: timeoutMs
    });
    connectReq.on('connect', (resp, socket) => {
      if ((resp.statusCode || 0) !== 200) {
        socket.destroy();
        done(Object.assign(new Error(`代理 CONNECT 失败: HTTP ${resp.statusCode || 0}`), { status: resp.statusCode || 0 }));
        return;
      }
      const tlsSocket = tls.connect({
        socket,
        servername: target.hostname,
        ALPNProtocols: ['http/1.1']
      });
      tlsSocket.setTimeout(timeoutMs, () => tlsSocket.destroy(timeoutError()));
      tlsSocket.once('error', err => done(err));
      tlsSocket.once('secureConnect', () => {
        const tunnelAgent = new https.Agent({ keepAlive: false, maxSockets: 1 });
        tunnelAgent.createConnection = (_opts, callback) => {
          if (typeof callback === 'function') process.nextTick(callback, null, tlsSocket);
          return tlsSocket;
        };
        const req = https.request({
          protocol: 'https:',
          hostname: target.hostname,
          port: target.port || 443,
          method: 'GET',
          path: `${target.pathname}${target.search}`,
          headers: { Host: target.host, ...headers },
          agent: tunnelAgent,
          timeout: timeoutMs
        }, async response => {
          try { done(null, await collectNodeResponse(response, options.maxBytes || MAX_REMOTE_CONFIG, Boolean(options.binary))); } catch (err) { done(err); }
          finally { tunnelAgent.destroy(); }
        });
        req.on('timeout', () => req.destroy(timeoutError()));
        req.on('error', err => { tunnelAgent.destroy(); done(err); });
        req.end();
      });
    });
    connectReq.on('timeout', () => connectReq.destroy(timeoutError()));
    connectReq.on('error', err => done(err));
    connectReq.end();
  });
}

async function nodeRequestFollowingRedirects(urlString, options = {}) {
  let current = String(urlString);
  const maxRedirects = Math.max(0, Number(options.maxRedirects ?? 10));
  for (let i = 0; i <= maxRedirects; i++) {
    const resp = await nodeRequestOnce(current, options);
    if ([301, 302, 303, 307, 308].includes(resp.status) && resp.headers.location) {
      if (i === maxRedirects) throw new Error(`订阅重定向超过 ${maxRedirects} 次`);
      current = new URL(resp.headers.location, current).toString();
      continue;
    }
    return { ...resp, finalUrl: current, redirects: i };
  }
  throw new Error('订阅重定向失败');
}

function systemProxyFor(urlString) {
  const u = new URL(urlString);
  const env = process.env;
  const candidates = u.protocol === 'https:'
    ? [env.HTTPS_PROXY, env.https_proxy, env.HTTP_PROXY, env.http_proxy, env.ALL_PROXY, env.all_proxy]
    : [env.HTTP_PROXY, env.http_proxy, env.ALL_PROXY, env.all_proxy];
  return candidates.find(Boolean) || null;
}

async function currentMihomoHttpProxy() {
  try {
    const data = (await mihomoFetch('/configs', {}, 8000)).data || {};
    const mixed = Number(data['mixed-port'] ?? data.mixedPort ?? 0);
    if (Number.isInteger(mixed) && mixed > 0 && mixed <= 65535) return `http://127.0.0.1:${mixed}`;
    const port = Number(data.port ?? 0);
    if (Number.isInteger(port) && port > 0 && port <= 65535) return `http://127.0.0.1:${port}`;
  } catch (err) {
    await log(`订阅更新：读取 Mihomo mixed-port 失败: ${err.message}`);
  }
  return null;
}

function compactAttemptError(attempt) {
  if (attempt.skipped) return `${attempt.label}：跳过（${attempt.error}）`;
  if (attempt.status) return `${attempt.label}：HTTP ${attempt.status}`;
  return `${attempt.label}：${attempt.error || '失败'}`;
}

function safeProxyUrl(value) {
  if (!value) return null;
  try {
    const u = new URL(value);
    if (u.username || u.password) { u.username = '***'; u.password = '***'; }
    return u.toString().replace(/\/$/, '');
  } catch (_) { return String(value).replace(/:\/\/[^@]+@/, '://***@'); }
}

async function fetchRemoteText(urlString, options = {}) {
  const u = new URL(urlString);
  if (!['http:', 'https:'].includes(u.protocol)) throw new Error('订阅地址只支持 http/https');
  const userAgent = String(options.userAgent || '').trim() || defaultSubscriptionUserAgent();
  const timeoutMs = Math.max(5000, Number(options.timeoutMs || 20000));
  const attempts = [];
  const mihomoProxy = await currentMihomoHttpProxy();
  const systemProxy = systemProxyFor(urlString);
  const plans = [
    { key: 'direct', label: '直连', proxyUrl: null },
    mihomoProxy
      ? { key: 'mihomo', label: 'Mihomo 代理', proxyUrl: mihomoProxy }
      : { key: 'mihomo', label: 'Mihomo 代理', skipped: true, error: '未检测到 mixed-port/HTTP port' },
    systemProxy && systemProxy !== mihomoProxy
      ? { key: 'system', label: '系统代理', proxyUrl: systemProxy }
      : { key: 'system', label: '系统代理', skipped: true, error: systemProxy ? '与 Mihomo 代理相同' : '未配置 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY' }
  ];

  for (const plan of plans) {
    if (plan.skipped) {
      attempts.push({ key: plan.key, label: plan.label, skipped: true, error: plan.error });
      await log(`订阅更新 [${plan.label}] 跳过：${plan.error}`);
      continue;
    }
    const started = Date.now();
    await log(`订阅更新 [${plan.label}] 开始，UA=${userAgent}${plan.proxyUrl ? `，proxy=${safeProxyUrl(plan.proxyUrl)}` : ''}`);
    try {
      const resp = await nodeRequestFollowingRedirects(urlString, {
        proxyUrl: plan.proxyUrl,
        timeoutMs,
        maxRedirects: 10,
        headers: { 'User-Agent': userAgent }
      });
      const durationMs = Date.now() - started;
      const attempt = { key: plan.key, label: plan.label, status: resp.status, durationMs, redirects: resp.redirects };
      attempts.push(attempt);
      if (resp.status < 200 || resp.status >= 300) {
        attempt.server = String(resp.headers.server || '');
        attempt.error = `HTTP ${resp.status}`;
        await log(`订阅更新 [${plan.label}] 失败：HTTP ${resp.status}，${durationMs} ms${attempt.server ? `，server=${attempt.server}` : ''}`);
        continue;
      }
      if (!resp.body.trim()) {
        attempt.error = '订阅返回为空';
        await log(`订阅更新 [${plan.label}] 失败：返回为空，${durationMs} ms`);
        continue;
      }
      await log(`订阅更新 [${plan.label}] 成功：HTTP ${resp.status}，${durationMs} ms，redirect=${resp.redirects}`);
      return {
        content: resp.body,
        subscriptionInfo: parseSubscriptionUserInfo(resp.headers['subscription-userinfo']),
        profileWebPageUrl: resp.headers['profile-web-page-url'] || null,
        downloadInfo: {
          method: plan.key,
          label: plan.label,
          status: resp.status,
          durationMs,
          redirects: resp.redirects,
          userAgent,
          proxyUrl: safeProxyUrl(plan.proxyUrl),
          updatedAt: Date.now(),
          attempts
        }
      };
    } catch (err) {
      const durationMs = Date.now() - started;
      const attempt = { key: plan.key, label: plan.label, durationMs, error: err.message || String(err) };
      attempts.push(attempt);
      await log(`订阅更新 [${plan.label}] 失败：${attempt.error}，${durationMs} ms`);
    }
  }
  const ordered = ['direct', 'mihomo', 'system'].map(k => attempts.find(x => x.key === k)).filter(Boolean);
  throw Object.assign(new Error(`订阅下载失败：${ordered.map(compactAttemptError).join('；')}`), {
    attempts: ordered,
    downloadInfo: { method: 'failed', label: '全部失败', status: 0, durationMs: ordered.reduce((n, x) => n + Number(x.durationMs || 0), 0), userAgent, updatedAt: Date.now(), attempts: ordered }
  });
}


async function requestWithNetworkFallback(urlString, options = {}) {
  const mihomoProxy = await currentMihomoHttpProxy();
  const systemProxy = systemProxyFor(urlString);
  const plans = [
    { key: 'direct', label: '直连', proxyUrl: null },
    mihomoProxy ? { key: 'mihomo', label: 'Mihomo 代理', proxyUrl: mihomoProxy } : null,
    systemProxy && systemProxy !== mihomoProxy ? { key: 'system', label: '系统代理', proxyUrl: systemProxy } : null
  ].filter(Boolean);
  const errors = [];
  for (const plan of plans) {
    try {
      const resp = await nodeRequestFollowingRedirects(urlString, {
        proxyUrl: plan.proxyUrl,
        timeoutMs: Number(options.timeoutMs || 30000),
        maxRedirects: 10,
        maxBytes: Number(options.maxBytes || MAX_REMOTE_CONFIG),
        binary: Boolean(options.binary),
        headers: options.headers || {}
      });
      if (resp.status >= 200 && resp.status < 300) return { ...resp, networkMethod: plan.key, networkLabel: plan.label, proxyUrl: safeProxyUrl(plan.proxyUrl) };
      errors.push(`${plan.label}: HTTP ${resp.status}`);
    } catch (err) { errors.push(`${plan.label}: ${err.message}`); }
  }
  throw new Error(`网络请求失败：${errors.join('；')}`);
}

function detectMihomoDownloadTarget() {
  const platform = process.platform;
  const machine = String(typeof os.machine === 'function' ? os.machine() : process.arch).trim().toLowerCase();
  return resolveMihomoDownloadTarget(platform, machine, process.arch);
}

function selectOfficialCoreAsset(release, tag) {
  const target = detectMihomoDownloadTarget();
  const assetNames = mihomoReleaseAssetNames(target, tag);
  const asset = Array.isArray(release.assets) ? assetNames.map(name => release.assets.find(x => x.name === name)).find(Boolean) : null;
  const assetName = String(asset?.name || assetNames[0]);
  if (!asset?.browser_download_url) throw new Error(`官方 Release 中没有找到适用于 ${target.machine} 的 ${assetName}`);
  const digest = String(asset.digest || '');
  const sha256 = digest.startsWith('sha256:') ? digest.slice(7).toLowerCase() : null;
  return { target, assetName, asset, sha256 };
}

function normalizeReleaseRepo(value) {
  const raw = String(value || '').trim();
  if (!raw) return '';
  if (/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(raw)) return raw;
  try {
    const u = new URL(raw);
    if (u.hostname !== 'github.com') return '';
    const parts = u.pathname.split('/').filter(Boolean);
    if (parts.length >= 2) return `${parts[0]}/${parts[1].replace(/\.git$/i, '')}`;
  } catch (_) {}
  return '';
}

function appPlatformName() {
  const raw = String(process.env.TRIM_SYS_ARCH || process.arch || '').toLowerCase();
  return /arm|aarch/.test(raw) ? 'arm' : 'x86';
}

function selectAppReleaseAsset(release, platform) {
  const assets = Array.isArray(release?.assets) ? release.assets : [];
  const fpk = assets.filter(x => /\.fpk$/i.test(String(x?.name || '')));
  const exact = fpk.find(x => platform === 'arm'
    ? /(^|[_-])(arm|arm64|aarch64)([_-]|\.|$)/i.test(String(x.name || ''))
    : /(^|[_-])(x86|x86_64|amd64)([_-]|\.|$)/i.test(String(x.name || '')));
  const asset = exact || (fpk.length === 1 ? fpk[0] : null);
  return asset ? { name: asset.name, url: asset.browser_download_url || null, size: asset.size || null } : null;
}

async function appUpdateStatus(checkRemote = false) {
  const repo = normalizeReleaseRepo(APP_RELEASE_REPO);
  const result = {
    appName: 'Clash for fnOS',
    currentVersion: APP_VERSION,
    platform: appPlatformName(),
    sourceConfigured: Boolean(repo),
    releaseRepo: repo || null,
    updateAvailable: null,
    latest: null
  };
  if (!checkRemote || !repo) return result;
  const resp = await requestWithNetworkFallback(`https://api.github.com/repos/${repo}/releases/latest`, {
    timeoutMs: 15000,
    maxBytes: 4 * 1024 * 1024,
    headers: { 'User-Agent': `Clash-for-fnos/v${APP_VERSION}`, 'Accept': 'application/vnd.github+json' }
  });
  let release;
  try { release = JSON.parse(String(resp.body)); } catch (_) { throw new Error('Clash for fnOS Release 响应不是有效 JSON'); }
  const tag = String(release.tag_name || '').trim();
  if (!tag) throw new Error('Clash for fnOS Release 未提供版本号');
  result.latest = {
    tag,
    name: release.name || tag,
    publishedAt: release.published_at || null,
    htmlUrl: release.html_url || null,
    asset: selectAppReleaseAsset(release, result.platform)
  };
  result.updateAvailable = compareVersions(tag, APP_VERSION) > 0;
  return result;
}

async function latestMihomoRelease() {
  const resp = await requestWithNetworkFallback('https://api.github.com/repos/MetaCubeX/mihomo/releases/latest', {
    timeoutMs: 30000,
    maxBytes: 4 * 1024 * 1024,
    headers: { 'User-Agent': `Clash-for-fnos/v${APP_VERSION}`, 'Accept': 'application/vnd.github+json' }
  });
  let release;
  try { release = JSON.parse(String(resp.body)); } catch (_) { throw new Error('GitHub Release 响应不是有效 JSON'); }
  const tag = String(release.tag_name || '');
  if (!/^v\d+\.\d+\.\d+$/.test(tag)) throw new Error('无法识别 Mihomo 最新版本');
  const selected = selectOfficialCoreAsset(release, tag);
  return {
    tag,
    name: release.name || tag,
    publishedAt: release.published_at || null,
    htmlUrl: release.html_url || null,
    target: selected.target,
    asset: { name: selected.asset.name, url: selected.asset.browser_download_url, size: Number(selected.asset.size || 0), sha256: selected.sha256 }
  };
}

async function coreStatus(includeLatest = false) {
  let priv;
  try { priv = await syncControllerSettings(true); } catch (_) { priv = await privilegedStatus(); }
  let version = null;
  try { version = (await mihomoFetch('/version')).data; } catch (_) {}
  const currentVersion = version?.version || String(priv.binaryVersion || '').match(/v?\d+\.\d+\.\d+/)?.[0] || priv.bootstrap?.targetVersion || null;
  const { managedSecret: _hiddenManagedSecret, detectedSecret: _hiddenDetectedSecret, ...safePriv } = priv || {};
  const safeBootstrap = safePriv.bootstrap ? { ...safePriv.bootstrap, secret: undefined } : safePriv.bootstrap;
  const result = { ...safePriv, bootstrap: safeBootstrap, managerVersion: APP_VERSION, currentVersion, controllerVersion: version || null };
  if (includeLatest) {
    const latest = await latestMihomoRelease();
    result.latest = latest;
    result.updateAvailable = currentVersion ? compareVersions(latest.tag, currentVersion) > 0 : null;
  }
  return result;
}

async function sha256Buffer(buf) { return crypto.createHash('sha256').update(buf).digest('hex'); }

async function stageLatestCore(release) {
  await fsp.mkdir(CORE_STAGE_DIR, { recursive: true });
  await log(`Mihomo 内核更新：下载 ${release.asset.name}`);
  const resp = await requestWithNetworkFallback(release.asset.url, {
    binary: true,
    maxBytes: MAX_CORE_ASSET,
    timeoutMs: 120000,
    headers: { 'User-Agent': `Clash-for-fnos/v${APP_VERSION}`, 'Accept': 'application/octet-stream' }
  });
  const gz = Buffer.isBuffer(resp.body) ? resp.body : Buffer.from(resp.body);
  const compressedSha = await sha256Buffer(gz);
  if (release.asset.sha256 && compressedSha !== release.asset.sha256) throw new Error(`官方压缩包 SHA-256 不匹配：期望 ${release.asset.sha256}，实际 ${compressedSha}`);
  const stagePath = path.join(CORE_STAGE_DIR, `${release.asset.name}.${Date.now()}.download`);
  await fsp.writeFile(stagePath, gz, { mode: 0o600 });
  return { stagePath, compressedSha, networkLabel: resp.networkLabel };
}

async function updateMihomoCore(options = {}) {
  const before = await coreStatus(false);
  const release = await latestMihomoRelease();
  if (before.currentVersion && compareVersions(release.tag, before.currentVersion) <= 0 && !options.force) {
    return { ok: true, alreadyLatest: true, before, release };
  }
  const stage = await stageLatestCore(release);
  const install = await privilegedRequest('/core/install', {
    stagePath: stage.stagePath,
    expectedVersion: release.tag,
    restart: Boolean(options.restart)
  }, { timeoutMs: 120000 });
  try {
    if (options.restart) {
      if (!install.restarted) {
        await privilegedRequest('/core/rollback', { txId: install.txId, restart: before.mode === 'managed' }, { timeoutMs: 60000 }).catch(() => {});
        throw new Error(install.restartError ? `内核已回滚：重启服务失败：${install.restartError}` : '内核已回滚：未检测到可安全重启的 systemd 服务，请选择“仅更新文件”后手动重启 Mihomo');
      }
      let confirmed = false;
      let seen = null;
      for (let i = 0; i < 30; i++) {
        await new Promise(r => setTimeout(r, 1000));
        try {
          seen = (await mihomoFetch('/version', {}, 5000)).data?.version || null;
          if (seen && compareVersions(seen, release.tag) === 0) { confirmed = true; break; }
        } catch (_) {}
      }
      if (!confirmed) {
        await privilegedRequest('/core/rollback', { txId: install.txId, restart: true }, { timeoutMs: 60000 }).catch(() => {});
        throw new Error(`新内核启动后未能确认版本 ${release.tag}${seen ? `（检测到 ${seen}）` : ''}，已尝试回滚`);
      }
    }
    await privilegedRequest('/core/commit', { txId: install.txId }, { timeoutMs: 10000 }).catch(() => {});
    await log(`Mihomo 内核更新完成：${before.currentVersion || 'unknown'} -> ${release.tag}，备份：${install.backup}`);
    return { ok: true, before, release, stage: { compressedSha: stage.compressedSha, networkLabel: stage.networkLabel }, install };
  } finally {
    await fsp.unlink(stage.stagePath).catch(() => {});
  }
}

function publicProfile(item) {
  return {
    id: item.id,
    name: item.name,
    url: item.url,
    type: item.type || 'remote',
    sourcePath: item.sourcePath || null,
    autoUpdate: Boolean(item.autoUpdate),
    autoApply: Boolean(item.autoApply),
    intervalMinutes: Number(item.intervalMinutes || 0),
    updatedAt: item.updatedAt || null,
    lastError: item.lastError || null,
    subscriptionInfo: item.subscriptionInfo || null,
    profileWebPageUrl: item.profileWebPageUrl || null,
    userAgent: DEFAULT_SUBSCRIPTION_UA,
    lastDownload: item.lastDownload || null,
    current: profilesState.current === item.id
  };
}

async function updateProfile(item, manual = false) {
  let content;
  try {
    if ((item.type || 'remote') === 'remote') {
      const remote = await fetchRemoteText(item.url);
      content = remote.content;
      item.subscriptionInfo = remote.subscriptionInfo;
      item.profileWebPageUrl = remote.profileWebPageUrl;
      item.lastDownload = remote.downloadInfo;
    } else {
      content = await fsp.readFile(path.join(PROFILE_DIR, `${item.id}.yaml`), 'utf8');
    }
    await writeAtomic(path.join(PROFILE_DIR, `${item.id}.yaml`), content);
    item.updatedAt = Date.now();
    item.lastError = null;
    await writeJson(PROFILES_FILE, profilesState);
    if (profilesState.current === item.id && (item.autoApply || manual)) {
      await saveApplyAndSyncStartupConfig(content, { source: 'profile-auto', sourceId: item.id, sourceName: item.name });
    }
    return content;
  } catch (err) {
    item.lastError = err.message || String(err);
    if (err.downloadInfo) item.lastDownload = err.downloadInfo;
    await writeJson(PROFILES_FILE, profilesState).catch(() => {});
    throw err;
  }
}

async function activateProfile(item, syncStartup = false, options = {}) {
  const file = path.join(PROFILE_DIR, `${item.id}.yaml`);
  let content;
  try { content = await fsp.readFile(file, 'utf8'); }
  catch (_) {
    if ((item.type || 'remote') === 'remote') content = await updateProfile(item, false);
    else throw new Error('本地配置文件不存在');
  }
  let systemSync = null;
  if (syncStartup) {
    systemSync = await saveApplyAndSyncStartupConfig(content, { source: 'profile', sourceId: item.id, sourceName: item.name }, options);
  } else {
    await saveAndApplyManagedConfig(content);
    const previous = await readJson(CONFIG_META_FILE, {});
    await writeJson(CONFIG_META_FILE, { ...previous, source: 'profile', sourceId: item.id, sourceName: item.name, active: true, appliedAt: Date.now() });
  }
  profilesState.current = item.id;
  item.lastError = null;
  await writeJson(PROFILES_FILE, profilesState);
  return systemSync;
}

async function restoreSelections() {
  if (settings.persistSelections === false || !Object.keys(selectedState).length) return;
  let data;
  try { data = (await mihomoFetch('/proxies')).data; } catch (_) { return; }
  const all = data?.proxies || {};
  for (const [group, node] of Object.entries(selectedState)) {
    const g = all[group];
    if (!g || !Array.isArray(g.all) || !g.all.includes(node)) continue;
    try {
      await mihomoFetch(`/proxies/${encodeURIComponent(group)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: node })
      });
    } catch (_) {}
  }
}

function json(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store'
  });
  res.end(body);
}

function text(res, status, payload, type = 'text/plain; charset=utf-8') {
  const body = String(payload ?? '');
  res.writeHead(status, { 'Content-Type': type, 'Content-Length': Buffer.byteLength(body), 'Cache-Control': 'no-store' });
  res.end(body);
}

async function bodyJson(req) {
  const raw = await bodyText(req);
  if (!raw) return {};
  try { return JSON.parse(raw); } catch (_) { throw Object.assign(new Error('JSON 格式错误'), { statusCode: 400 }); }
}

function bodyText(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', chunk => {
      size += chunk.length;
      if (size > MAX_BODY) {
        reject(Object.assign(new Error('请求体过大'), { statusCode: 413 }));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

function stripPrefix(urlPath) {
  if (urlPath === GATEWAY_PREFIX) return '/';
  if (urlPath.startsWith(`${GATEWAY_PREFIX}/`)) return urlPath.slice(GATEWAY_PREFIX.length);
  return urlPath;
}

function safeStatic(rel) {
  const clean = rel.replace(/^\/+/, '') || 'index.html';
  const full = path.resolve(PUBLIC_DIR, clean);
  if (!full.startsWith(path.resolve(PUBLIC_DIR) + path.sep) && full !== path.resolve(PUBLIC_DIR, 'index.html')) return null;
  return full;
}

const mime = {
  '.html': 'text/html; charset=utf-8', '.js': 'application/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8', '.svg': 'image/svg+xml', '.png': 'image/png', '.ico': 'image/x-icon'
};

async function serveStatic(reqPath, res) {
  let rel = reqPath === '/' ? 'index.html' : reqPath;
  const full = safeStatic(rel);
  if (!full) return false;
  try {
    const stat = await fsp.stat(full);
    if (!stat.isFile()) return false;
    const data = await fsp.readFile(full);
    res.writeHead(200, {
      'Content-Type': mime[path.extname(full)] || 'application/octet-stream',
      'Cache-Control': 'no-store, no-cache, must-revalidate, max-age=0',
      'Pragma': 'no-cache',
      'Expires': '0'
    });
    res.end(data);
    return true;
  } catch (_) { return false; }
}

async function sseProxy(req, res, apiPath) {
  const controller = normalizeController(settings.controller);
  const url = `${controller}${apiPath}`;
  const ac = new AbortController();
  req.on('close', () => ac.abort());
  let upstream;
  try {
    upstream = await fetch(url, { headers: authHeaders(), signal: ac.signal });
  } catch (err) {
    json(res, 502, { error: err.message });
    return;
  }
  if (!upstream.ok) {
    json(res, 502, { error: `Mihomo ${upstream.status}` });
    return;
  }
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    'Connection': 'keep-alive',
    'X-Accel-Buffering': 'no'
  });
  const reader = upstream.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      let idx;
      while ((idx = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, idx).trim();
        buffer = buffer.slice(idx + 1);
        if (line) res.write(`data: ${line}\n\n`);
      }
    }
  } catch (_) {
  } finally {
    try { res.end(); } catch (_) {}
  }
}

async function reapplyManagedConfigOnStart() {
  // The real Mihomo startup config is the single source of truth. Older versions
  // re-applied the Manager-side profile snapshot a few seconds after startup,
  // which could make runtime state diverge from the actual config.yaml again.
  // Do not mutate the Core on Manager startup; only restore selector choices.
  const meta = await readJson(CONFIG_META_FILE, {});
  if (meta.active === false) return;
  await log('启动时沿用 Mihomo 实际启动配置，不再重复热加载 Manager 配置副本');
  setTimeout(() => restoreSelections().catch(() => {}), 1200);
}

async function normalizeLegacyControllerListenOnStart() {
  const status = await privilegedRequest('/network/status', null, { method: 'GET', timeoutMs: 10000 });
  const n = status?.settings;
  if (!n?.controller?.needsLoopbackNormalization) return { changed: false };

  // v0.3.6-v0.3.11 could preserve `external-controller: :PORT`. The Settings UI
  // has always represented Controller as a local-only 127.0.0.1 listener, so
  // normalize that legacy form through the same validated/backup/hot-load path
  // used by an explicit "保存网络设置" action.
  const result = await updateNetworkSettings({
    controller: { port: n.controller.port },
    mixed: n.mixed,
    socks: n.socks,
    http: n.http,
    redir: n.redir,
    tproxy: n.tproxy,
    allowLan: n.allowLan,
    tun: n.tun
  });
  await log(`已自动修正旧 Controller 监听写法：127.0.0.1:${n.controller.port}`);
  return { changed: true, result };
}

/** @type {Promise<unknown>} */
let networkUpdateQueue = Promise.resolve();

function updateNetworkSettings(body) {
  const run = networkUpdateQueue.then(() => performNetworkSettingsUpdate(body));
  networkUpdateQueue = run.catch(() => {});
  return run;
}

async function networkSettingsStatus() {
  const status = await privilegedRequest('/network/status', null, { method: 'GET', timeoutMs: 10000 });
  status.settings = {
    ...(status.settings || {}),
    dnsOverrideEnabled: settings.dnsOverrideEnabled === true,
    dns: normalizeStoredDnsOverride(settings.dnsOverrideSettings)
  };
  return status;
}

async function performNetworkSettingsUpdate(body) {
  const before = JSON.parse(JSON.stringify(settings));
  const requestBody = body && typeof body === 'object' && !Array.isArray(body) ? { ...body } : {};
  const dnsOverride = resolveDnsOverrideUpdate(settings.dnsOverrideEnabled, settings.dnsOverrideSettings, requestBody);
  const nextDnsOverrideEnabled = dnsOverride.enabled;
  const nextDnsOverrideSettings = dnsOverride.settings;

  delete requestBody.dnsOverrideEnabled;
  delete requestBody.dns;
  if (dnsOverride.shouldApply) requestBody.dns = nextDnsOverrideSettings;

  if (Object.keys(requestBody).length === 0) {
    settings.dnsOverrideEnabled = nextDnsOverrideEnabled;
    settings.dnsOverrideSettings = nextDnsOverrideSettings;
    await writeJson(SETTINGS_FILE, settings);
    await log(`DNS 覆写已${nextDnsOverrideEnabled ? '开启' : '关闭'}；DNS 模板已保存，启用 DNS ${nextDnsOverrideSettings.enable ? '开启' : '关闭'}`);
    return {
      ok: true,
      savedOnly: true,
      settings: { dnsOverrideEnabled: nextDnsOverrideEnabled, dns: nextDnsOverrideSettings }
    };
  }

  const prepared = await privilegedRequest('/network/update', requestBody, { timeoutMs: 60000 });
  let activation = null;
  let applyError = null;
  try {
    activation = await privilegedRequest('/config/activate', { txId: prepared.txId }, { timeoutMs: 60000 });
    if (activation?.method === 'hot-reload') {
      try { await applyPayload(prepared.effectiveContent, 120000); }
      catch (err) { applyError = err; }
    }
  } catch (err) {
    await privilegedRequest('/config/rollback', { txId: prepared.txId }, { timeoutMs: 60000 }).catch(() => {});
    throw err;
  }

  const nextController = prepared.controller?.clientUrl ? normalizeController(prepared.controller.clientUrl) : before.controller;
  settings.controller = nextController;
  settings.controllerAutoDetect = true;
  settings.dnsOverrideEnabled = nextDnsOverrideEnabled;
  settings.dnsOverrideSettings = nextDnsOverrideSettings;
  await writeJson(SETTINGS_FILE, settings);
  restartMihomoLogCollector();

  let version = null;
  let confirmed = false;
  for (let i = 0; i < 30; i++) {
    try {
      version = (await mihomoFetch('/version', {}, 3500)).data;
      confirmed = true;
      break;
    } catch (_) {
      await new Promise(resolve => setTimeout(resolve, 500));
    }
  }

  if (!confirmed) {
    const rollback = await privilegedRequest('/config/rollback', { txId: prepared.txId }, { timeoutMs: 60000 }).catch(() => null);
    settings = before;
    await writeJson(SETTINGS_FILE, settings).catch(() => {});
    restartMihomoLogCollector();
    const suffix = applyError ? `；热加载返回：${applyError.message}` : rollback?.restartError ? `；回滚后重启失败：${rollback.restartError}` : '';
    throw new Error(`网络设置已回滚：修改后无法连接 Controller ${nextController}${suffix}`);
  }

  await privilegedRequest('/config/commit', { txId: prepared.txId }, { timeoutMs: 10000 }).catch(() => {});
  let proxyEnvironment = null;
  let proxyEnvironmentWarning = null;
  try {
    proxyEnvironment = await privilegedRequest('/system/proxy-environment/sync', {}, { timeoutMs: 30000 });
  } catch (err) {
    proxyEnvironmentWarning = `代理环境变量自动同步失败：${err.message}`;
    await log(proxyEnvironmentWarning).catch(() => {});
  }
  await log(`网络设置已更新：Controller ${nextController}，TUN ${prepared.settings?.tun?.enabled ? '开启' : '关闭'}，方式：${activation?.method || 'unknown'}${applyError ? `，热加载提示：${applyError.message}` : ''}`);
  return {
    ok: true,
    settings: {
      ...prepared.settings,
      dnsOverrideEnabled: nextDnsOverrideEnabled,
      dns: nextDnsOverrideSettings
    },
    controller: nextController,
    configPath: prepared.target,
    backup: prepared.backup,
    validation: prepared.validation,
    activation: activation?.method || null,
    warning: [applyError?.message, proxyEnvironmentWarning].filter(Boolean).join('；') || null,
    proxyEnvironment,
    version
  };
}

function findProfile(id) {
  return profilesState.items.find(p => p.id === id);
}

async function route(req, res) {
  const parsed = new URL(req.url, 'http://localhost');
  // fnOS may launch the iframe at /app/clash-for-fnos without a trailing slash.
  // Redirect once so browser-relative assets resolve under the app gateway prefix.
  if (parsed.pathname === GATEWAY_PREFIX) {
    res.writeHead(302, { Location: `${GATEWAY_PREFIX}/${parsed.search || ''}` });
    res.end();
    return;
  }
  const p = stripPrefix(parsed.pathname);
  const method = req.method || 'GET';

  if (p === '/api/health' && method === 'GET') return json(res, 200, { ok: true, app: APP_NAME, version: APP_VERSION });
  if (p === '/api/app/icons' && method === 'GET') return json(res, 200, await privilegedRequest('/app/icon/status', null, { method: 'GET', timeoutMs: 10000 }));
  if (p === '/api/app/icon' && method === 'PUT') {
    const body = await bodyJson(req);
    const result = await privilegedRequest('/app/icon/update', { iconId: body?.iconId }, { timeoutMs: 15000 });
    await log(`软件图标已切换：${result?.selected || body?.iconId || 'unknown'}`).catch(() => {});
    return json(res, 200, result);
  }
  if (p === '/api/app/update-info' && method === 'GET') return json(res, 200, await appUpdateStatus(false));
  if (p === '/api/app/check-update' && method === 'POST') return json(res, 200, await appUpdateStatus(true));

  if (p === '/api/settings' && method === 'GET') return json(res, 200, sanitizeSettingsForClient());
  if (p === '/api/settings' && method === 'PUT') {
    const body = await bodyJson(req);
    if (body.controllerAutoDetect !== undefined) settings.controllerAutoDetect = Boolean(body.controllerAutoDetect);
    if (body.controller !== undefined) settings.controller = normalizeController(body.controller);
    if (body.secret !== undefined && body.secret !== '') settings.secret = String(body.secret);
    if (body.clearSecret === true) settings.secret = '';
    if (body.persistSelections !== undefined) settings.persistSelections = Boolean(body.persistSelections);
    if (body.applyManagedConfigOnStart !== undefined) settings.applyManagedConfigOnStart = Boolean(body.applyManagedConfigOnStart);
    if (body.healthcheckUrl !== undefined) settings.healthcheckUrl = String(body.healthcheckUrl || defaults.healthcheckUrl);
    if (body.healthcheckTimeout !== undefined) settings.healthcheckTimeout = Math.max(1000, Math.min(30000, Number(body.healthcheckTimeout) || defaults.healthcheckTimeout));
    await writeJson(SETTINGS_FILE, settings);
    if (settings.controllerAutoDetect !== false) await syncControllerSettings(true).catch(() => {});
    else restartMihomoLogCollector();
    return json(res, 200, sanitizeSettingsForClient());
  }
  if (p === '/api/settings/test' && method === 'POST') {
    await syncControllerSettings(true).catch(() => {});
    const version = (await mihomoFetch('/version')).data;
    return json(res, 200, { ok: true, version });
  }

  if (p === '/api/network/settings' && method === 'GET') {
    return json(res, 200, await networkSettingsStatus());
  }
  if (p === '/api/network/settings' && method === 'PUT') {
    const body = await bodyJson(req);
    return json(res, 200, await updateNetworkSettings(body));
  }

  if (p === '/api/system/status' && method === 'GET') return json(res, 200, await coreStatus(false));
  if (p === '/api/system/authorized-paths' && method === 'GET') return json(res, 200, await authorizedPathStatus());
  if (p === '/api/system/proxy-environment' && method === 'GET') {
    const result = await privilegedRequest('/system/proxy-environment', null, { method: 'GET', timeoutMs: 10000 }).catch(err => ({ ok: false, error: err.message, files: [], helperEnvironment: [], mihomoEnvironment: { pid: null, variables: [] }, management: null }));
    result.managerEnvironment = proxyEnvFromObject(process.env);
    return json(res, 200, result);
  }
  if (p === '/api/system/proxy-environment' && method === 'PUT') {
    const body = await bodyJson(req);
    const result = await privilegedRequest('/system/proxy-environment/update', body || {}, { timeoutMs: 30000 });
    result.managerEnvironment = proxyEnvFromObject(process.env);
    await log(`代理环境变量已${result?.management?.settings?.enabled ? '启用/更新' : '关闭'}：${(result?.operation?.changed || []).join(', ') || '无需改写系统文件'}`).catch(() => {});
    return json(res, 200, result);
  }
  if (p === '/api/system/proxy-environment' && method === 'DELETE') {
    const result = await privilegedRequest('/system/proxy-environment/update', { enabled: false }, { timeoutMs: 30000 });
    result.managerEnvironment = proxyEnvFromObject(process.env);
    await log('代理环境变量已关闭并移除 Clash for fnos 管理块').catch(() => {});
    return json(res, 200, result);
  }
  if (p === '/api/core/bootstrap/retry' && method === 'POST') {
    const result = await privilegedRequest('/bootstrap/retry', {}, { timeoutMs: 180000 });
    await syncControllerSettings(true).catch(() => {});
    return json(res, 200, result);
  }
  if (p === '/api/core/check-update' && method === 'POST') return json(res, 200, await coreStatus(true));
  if (p === '/api/core/update' && method === 'POST') {
    const body = await bodyJson(req);
    return json(res, 200, await updateMihomoCore({ restart: Boolean(body.restart), force: Boolean(body.force) }));
  }

  if (p === '/api/local-config/discover' && method === 'GET') return json(res, 200, await discoverLocalConfigs());
  if (p === '/api/local-config/check' && method === 'POST') {
    const body = await bodyJson(req);
    return json(res, 200, await checkManualLocalConfig(body.path));
  }
  if (p === '/api/local-config/import' && method === 'POST') {
    const body = await bodyJson(req);
    if (!body.token) throw Object.assign(new Error('缺少配置扫描标识'), { statusCode: 400 });
    return json(res, 200, await importLocalCandidate(body.token, { apply: body.apply, name: body.name }));
  }
  if (p === '/api/config/meta' && method === 'GET') {
    return json(res, 200, await readJson(CONFIG_META_FILE, { source: 'managed', path: null, importedAt: null, appliedAt: null }));
  }

  if (p === '/api/status' && method === 'GET') {
    await syncControllerSettings().catch(() => {});
    const [version, configs, conns] = await Promise.all([
      mihomoFetch('/version'), mihomoFetch('/configs'), mihomoFetch('/connections')
    ]);
    return json(res, 200, {
      online: true,
      version: version.data,
      configs: configs.data,
      connections: {
        count: Array.isArray(conns.data?.connections) ? conns.data.connections.length : 0,
        uploadTotal: conns.data?.uploadTotal || 0,
        downloadTotal: conns.data?.downloadTotal || 0,
        memory: conns.data?.memory || 0
      }
    });
  }

  if (p === '/api/proxies' && method === 'GET') return json(res, 200, await orderedProxiesPayload());
  if (p.startsWith('/api/proxies/') && method === 'PUT') {
    const group = decodeURIComponent(p.slice('/api/proxies/'.length));
    const body = await bodyJson(req);
    if (!body.name) throw Object.assign(new Error('缺少节点名称'), { statusCode: 400 });
    await mihomoFetch(`/proxies/${encodeURIComponent(group)}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: body.name })
    });
    if (settings.persistSelections !== false) {
      selectedState[group] = body.name;
      await writeJson(SELECTED_FILE, selectedState);
    }
    return json(res, 200, { ok: true });
  }
  if (p.startsWith('/api/delay/') && method === 'GET') {
    const name = decodeURIComponent(p.slice('/api/delay/'.length));
    const url = encodeURIComponent(settings.healthcheckUrl || defaults.healthcheckUrl);
    const timeout = Number(settings.healthcheckTimeout || defaults.healthcheckTimeout);
    return json(res, 200, (await mihomoFetch(`/proxies/${encodeURIComponent(name)}/delay?url=${url}&timeout=${timeout}`, {}, timeout + 3000)).data);
  }

  if (p === '/api/providers' && method === 'GET') return json(res, 200, (await mihomoFetch('/providers/proxies')).data);
  let m = p.match(/^\/api\/providers\/([^/]+)\/(update|healthcheck)$/);
  if (m) {
    const name = decodeURIComponent(m[1]);
    if (m[2] === 'update' && method === 'PUT') {
      await mihomoFetch(`/providers/proxies/${encodeURIComponent(name)}`, { method: 'PUT' }, 30000);
      return json(res, 200, { ok: true });
    }
    if (m[2] === 'healthcheck' && method === 'GET') {
      await mihomoFetch(`/providers/proxies/${encodeURIComponent(name)}/healthcheck`, {}, 30000);
      return json(res, 200, { ok: true });
    }
  }

  if (p === '/api/rule-providers' && method === 'GET') return json(res, 200, (await mihomoFetch('/providers/rules')).data);
  m = p.match(/^\/api\/rule-providers\/([^/]+)\/update$/);
  if (m && method === 'PUT') {
    const name = decodeURIComponent(m[1]);
    return json(res, 200, await updateRuleProviderWithDirectFallback(name));
  }

  if (p === '/api/connections' && method === 'GET') return json(res, 200, (await mihomoFetch('/connections')).data);
  if (p === '/api/connections' && method === 'DELETE') {
    await mihomoFetch('/connections', { method: 'DELETE' }); return json(res, 200, { ok: true });
  }
  if (p.startsWith('/api/connections/') && method === 'DELETE') {
    const id = decodeURIComponent(p.slice('/api/connections/'.length));
    await mihomoFetch(`/connections/${encodeURIComponent(id)}`, { method: 'DELETE' }); return json(res, 200, { ok: true });
  }
  if (p === '/api/rules' && method === 'GET') return json(res, 200, (await mihomoFetch('/rules')).data);

  if (p === '/api/runtime-config' && method === 'GET') return json(res, 200, (await mihomoFetch('/configs')).data);
  if (p === '/api/runtime-config' && method === 'PATCH') {
    const body = await bodyJson(req);
    await mihomoFetch('/configs', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    return json(res, 200, { ok: true });
  }

  if (p === '/api/config/effective' && method === 'GET') {
    const result = await privilegedRequest('/config/active-raw', null, { method: 'GET', timeoutMs: 10000 });
    return json(res, 200, result);
  }

  if (p === '/api/config/raw' && method === 'GET') {
    try { return text(res, 200, await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8'), 'text/yaml; charset=utf-8'); }
    catch (_) { return text(res, 200, '# 在这里粘贴完整的 Mihomo YAML 配置\n', 'text/yaml; charset=utf-8'); }
  }
  if (p === '/api/config/raw' && method === 'PUT') {
    const raw = await bodyText(req);
    await saveAndApplyManagedConfig(raw);
    const previous = await readJson(CONFIG_META_FILE, {});
    await writeJson(CONFIG_META_FILE, { ...previous, source: previous.source || 'managed', savedAt: Date.now(), appliedAt: Date.now() });
    return json(res, 200, { ok: true });
  }
  if (p === '/api/config/apply' && method === 'POST') {
    const raw = await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8');
    await applyPayload(raw);
    setTimeout(() => restoreSelections().catch(() => {}), 1200);
    return json(res, 200, { ok: true });
  }

  if (p === '/api/config/sync-startup' && method === 'POST') {
    const raw = await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8');
    const result = await saveApplyAndSyncStartupConfig(raw, { source: 'managed' });
    return json(res, 200, { ok: true, ...result });
  }
  if (p === '/api/config/backups' && method === 'GET') {
    const files = (await fsp.readdir(BACKUP_DIR).catch(() => [])).filter(f => f.endsWith('.yaml')).sort().reverse();
    return json(res, 200, { items: files });
  }
  if (p.startsWith('/api/config/backups/') && method === 'POST') {
    const name = decodeURIComponent(p.slice('/api/config/backups/'.length));
    if (!/^[0-9T\-.]+\.yaml$/.test(name)) throw Object.assign(new Error('备份文件名非法'), { statusCode: 400 });
    const raw = await fsp.readFile(path.join(BACKUP_DIR, name), 'utf8');
    await saveAndApplyManagedConfig(raw);
    return json(res, 200, { ok: true });
  }

  if (p === '/api/profiles' && method === 'GET') return json(res, 200, { current: profilesState.current, items: profilesState.items.map(publicProfile) });
  if (p === '/api/profiles' && method === 'POST') {
    const body = await bodyJson(req);
    if (!body.name) throw Object.assign(new Error('请输入订阅名称'), { statusCode: 400 });
    if (!body.url) throw Object.assign(new Error('请输入订阅 URL'), { statusCode: 400 });
    const id = crypto.randomBytes(6).toString('hex');
    const item = {
      id, name: String(body.name), url: String(body.url), type: 'remote',
      autoUpdate: body.autoUpdate !== false, autoApply: Boolean(body.autoApply),
      intervalMinutes: Math.max(0, Number(body.intervalMinutes || 360)), updatedAt: null, lastError: null
    };
    profilesState.items.push(item);
    await writeJson(PROFILES_FILE, profilesState);
    try { await updateProfile(item, false); } catch (err) { item.lastError = err.message; await writeJson(PROFILES_FILE, profilesState); }
    return json(res, 201, publicProfile(item));
  }
  if (p === '/api/profiles/import' && method === 'POST') {
    const body = await bodyJson(req);
    if (!body.name || !body.content) throw Object.assign(new Error('名称和配置内容不能为空'), { statusCode: 400 });
    const id = crypto.randomBytes(6).toString('hex');
    const item = { id, name: String(body.name), url: '', type: 'local', autoUpdate: false, autoApply: false, intervalMinutes: 0, updatedAt: Date.now(), lastError: null };
    await writeAtomic(path.join(PROFILE_DIR, `${id}.yaml`), String(body.content));
    profilesState.items.push(item);
    await writeJson(PROFILES_FILE, profilesState);
    return json(res, 201, publicProfile(item));
  }
  m = p.match(/^\/api\/jobs\/([a-f0-9]+)$/);
  if (m && method === 'GET') {
    const job = profileApplyJobs.get(m[1]);
    if (!job) throw Object.assign(new Error('应用任务不存在或已过期'), { statusCode: 404 });
    return json(res, 200, publicProfileApplyJob(job));
  }

  m = p.match(/^\/api\/profiles\/([a-f0-9]+)\/(update|activate|apply-system)$/);
  if (m) {
    const item = findProfile(m[1]);
    if (!item) throw Object.assign(new Error('配置不存在'), { statusCode: 404 });
    if (m[2] === 'update' && method === 'POST') { await updateProfile(item, true); return json(res, 200, publicProfile(item)); }
    if (m[2] === 'activate' && method === 'POST') { const job = startProfileApplyJob(item); return json(res, 202, job); }
    if (m[2] === 'apply-system' && method === 'POST') { const system = await activateProfile(item, true); return json(res, 200, { profile: publicProfile(item), system }); }
  }
  m = p.match(/^\/api\/profiles\/([a-f0-9]+)$/);
  if (m && method === 'PATCH') {
    const item = findProfile(m[1]);
    if (!item) throw Object.assign(new Error('配置不存在'), { statusCode: 404 });
    const body = await bodyJson(req);
    for (const key of ['name', 'url', 'autoUpdate', 'autoApply', 'intervalMinutes']) if (body[key] !== undefined) item[key] = body[key];
    item.intervalMinutes = Math.max(0, Number(item.intervalMinutes || 0));
    await writeJson(PROFILES_FILE, profilesState);
    return json(res, 200, publicProfile(item));
  }
  if (m && method === 'DELETE') {
    const id = m[1];
    const idx = profilesState.items.findIndex(x => x.id === id);
    if (idx < 0) throw Object.assign(new Error('配置不存在'), { statusCode: 404 });
    profilesState.items.splice(idx, 1);
    if (profilesState.current === id) profilesState.current = null;
    await fsp.unlink(path.join(PROFILE_DIR, `${id}.yaml`)).catch(() => {});
    await writeJson(PROFILES_FILE, profilesState);
    return json(res, 200, { ok: true });
  }

  if (p === '/api/stream/traffic' && method === 'GET') return sseProxy(req, res, '/traffic');
  if (p === '/api/logs/history' && method === 'GET') {
    const level = normalizeMihomoLogLevel(parsed.searchParams.get('level') || 'info');
    const limit = Number(parsed.searchParams.get('limit') || 800);
    return json(res, 200, await readMihomoLogHistory(level, limit));
  }
  if (p === '/api/logs/history' && method === 'DELETE') {
    await clearMihomoLogHistory();
    return json(res, 200, { ok: true });
  }
  if (p === '/api/stream/logs' && method === 'GET') {
    const level = normalizeMihomoLogLevel(parsed.searchParams.get('level') || 'info');
    return streamPersistedMihomoLogs(req, res, level);
  }

  if (method === 'GET' && (await serveStatic(p, res))) return;
  if (method === 'GET' && !p.startsWith('/api/')) return serveStatic('/', res);
  json(res, 404, { error: 'Not found' });
}

async function schedulerTick() {
  const now = Date.now();
  for (const item of profilesState.items) {
    if (!item.autoUpdate || (item.type || 'remote') !== 'remote') continue;
    const interval = Math.max(5, Number(item.intervalMinutes || 0));
    if (!interval) continue;
    if (item.updatedAt && now - Number(item.updatedAt) < interval * 60 * 1000) continue;
    try { await updateProfile(item, false); }
    catch (err) { item.lastError = err.message; await writeJson(PROFILES_FILE, profilesState); await log(`订阅自动更新失败 ${item.name}: ${err.message}`); }
  }
}

async function init() {
  await ensureDirs();
  const storedSettings = await readJson(SETTINGS_FILE, {});
  settings = {
    ...defaults,
    ...storedSettings,
    controllerAutoDetect: true,
    dnsOverrideEnabled: storedSettings.dnsOverrideEnabled === true,
    dnsOverrideSettings: normalizeStoredDnsOverride(storedSettings.dnsOverrideSettings)
  };
  profilesState = await readJson(PROFILES_FILE, { current: null, items: [] });
  if (!Array.isArray(profilesState.items)) profilesState.items = [];
  selectedState = await readJson(SELECTED_FILE, {});
  try { await writeJson(SETTINGS_FILE, settings); } catch (_) {}

  try { await fsp.unlink(SOCKET_PATH); } catch (_) {}
  const server = http.createServer((req, res) => {
    route(req, res).catch(async err => {
      const status = err.statusCode && Number.isInteger(err.statusCode) ? err.statusCode : 500;
      await log(`${req.method} ${req.url} -> ${status}: ${err.stack || err.message}`);
      if (!res.headersSent) json(res, status, { error: err.message || 'Internal error' });
      else try { res.end(); } catch (_) {}
    });
  });

  server.listen(SOCKET_PATH, async () => {
    try { await fsp.chmod(SOCKET_PATH, 0o660); } catch (_) {}
    await log(`Clash for fnos ${APP_VERSION} started on ${SOCKET_PATH}`);
    setTimeout(() => syncControllerSettings(true).catch(() => {}), 1000);
    setTimeout(() => normalizeLegacyControllerListenOnStart().catch(err => log(`自动修正 Controller 监听失败: ${err.message}`)), 2500);
    setTimeout(() => syncControllerSettings(true).catch(() => {}), 5000);
    setTimeout(() => privilegedRequest('/system/proxy-environment/sync', {}, { timeoutMs: 30000 })
      .then(result => { const changed = result?.operation?.changed || []; if (changed.length) return log(`代理环境变量启动同步完成：${changed.length} 个系统文件`); })
      .catch(err => log(`代理环境变量启动同步失败：${err.message}`)), 3500);
    setTimeout(() => reapplyManagedConfigOnStart().catch(() => {}), 7000);
    setTimeout(() => restoreSelections().catch(() => {}), 5000);
    setTimeout(() => restartMihomoLogCollector(), 1500);
  });

  setInterval(() => schedulerTick().catch(() => {}), 60 * 1000).unref();

  const shutdown = async () => {
    stopMihomoLogCollector();
    await log('Stopping Clash for fnos');
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 3000).unref();
  };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
}

init().catch(err => { console.error(err); process.exit(1); });
