'use strict';

const http = require('http');
const https = require('https');
const zlib = require('zlib');
const net = require('net');
const fs = require('fs');
const fsp = fs.promises;
const path = require('path');
const crypto = require('crypto');
const os = require('os');
const { spawn } = require('child_process');
const {
  detectMihomoDownloadTarget: resolveMihomoDownloadTarget,
  mihomoReleaseAssetNames
} = require('./lib/version');
const { parseProxyGroupOrder } = require('./lib/yaml-proxy-groups');
const { handlePrivilegedApi } = require('./lib/privileged-api');
const { appIconEntryPath, versionedAppIconKey } = require('./lib/app-icon');
const { DEFAULT_DNS_SETTINGS, normalizeDnsSettings, parseDnsSettingsBlock, parseHostsBlock, renderDnsSettingsBlock, renderHostsBlock } = require('./lib/dns-settings');
const {
  DEFAULT_PROXY_ENV_SETTINGS,
  PROXY_ENV_BEGIN,
  PROXY_ENV_END,
  managedProxyEnvBlock,
  normalizeProxyEnvSettings,
  parseProxyEnvFile,
  proxyEnvFromObject,
  stripManagedProxyEnvBlock,
  withManagedProxyEnvBlock
} = require('./lib/proxy-environment');

const SOCKET_PATH = process.env.PRIV_SOCKET_PATH || '/tmp/clash-for-fnos-priv.sock';
const ETC_DIR = process.env.TRIM_PKGETC || '/tmp/clash-for-fnos-etc';
const VAR_DIR = process.env.TRIM_PKGVAR || '/tmp/clash-for-fnos-var';
const MAX_CONFIG = 12 * 1024 * 1024;
const CORE_STAGE_DIR = path.join(VAR_DIR, 'core-stage');
const MANAGED_CORE_DIR = path.join(VAR_DIR, 'managed-core');
const MANAGED_CORE_BIN = path.join(MANAGED_CORE_DIR, 'mihomo');
const MANAGED_CORE_PID = path.join(MANAGED_CORE_DIR, 'mihomo.pid');
const MANAGED_CORE_LOG = path.join(MANAGED_CORE_DIR, 'mihomo.log');
const MANAGED_CONFIG_DIR = path.join(ETC_DIR, 'mihomo');
const MANAGED_CONFIG_FILE = path.join(MANAGED_CONFIG_DIR, 'config.yaml');
const SYSTEM_MIHOMO_CONFIG_PATHS = new Set([
  '/etc/mihomo/config.yaml', '/etc/clash/config.yaml',
  '/usr/local/etc/mihomo/config.yaml', '/usr/local/etc/clash/config.yaml',
  '/opt/mihomo/config.yaml', '/var/lib/mihomo/config.yaml',
  '/root/.config/mihomo/config.yaml', '/root/.config/clash/config.yaml'
].map(x => path.normalize(x)));
const BOOTSTRAP_META_FILE = path.join(ETC_DIR, 'managed-core-meta.json');
const BUNDLED_CORE_DIR = path.resolve(__dirname, '..', 'core');
const BUNDLED_CORE_META_FILE = path.join(BUNDLED_CORE_DIR, 'bundled-core.json');
const ONLINE_CORE_MARKER_FILE = path.join(BUNDLED_CORE_DIR, 'online-core.json');
const MAX_CORE_ASSET = 80 * 1024 * 1024;
const ALLOWED_CORE_DOWNLOAD_HOSTS = new Set(['github.com', 'objects.githubusercontent.com', 'release-assets.githubusercontent.com']);
const SYSTEM_BACKUP_DIR = path.join(ETC_DIR, 'system-backups');
const CONFIG_BACKUP_DIR = path.join(SYSTEM_BACKUP_DIR, 'config');
const CORE_BACKUP_DIR = path.join(SYSTEM_BACKUP_DIR, 'core');
const PROXY_ENV_BACKUP_DIR = path.join(SYSTEM_BACKUP_DIR, 'proxy-environment');
const PROXY_ENV_SETTINGS_FILE = path.join(ETC_DIR, 'proxy-environment.json');
const APP_ROOT_DIR = path.resolve(__dirname, '..');
const APP_UI_IMAGES_DIR = path.join(APP_ROOT_DIR, 'ui', 'images');
const APP_ICON_PRESET_DIR = path.join(APP_UI_IMAGES_DIR, 'icons');
const APP_ICON_MANIFEST_FILE = path.join(APP_ICON_PRESET_DIR, 'manifest.json');
const APP_ICON_SETTINGS_FILE = path.join(ETC_DIR, 'app-icon.json');
const APP_UI_CONFIG_FILE = path.join(APP_ROOT_DIR, 'ui', 'config');
const APP_WEB_PUBLIC_DIR = path.join(APP_ROOT_DIR, 'server', 'public');
const APP_ICON_DEFAULT = 'cat-orbit';
const APP_INSTALL_NAME = /^[a-zA-Z0-9._-]+$/.test(String(process.env.TRIM_APPNAME || '')) ? String(process.env.TRIM_APPNAME) : 'clash-for-fnos';
const APP_INSTALL_ROOT = path.join('/var/apps', APP_INSTALL_NAME);
const APP_INSTALL_ICON64 = path.join(APP_INSTALL_ROOT, 'ICON.PNG');
const APP_INSTALL_ICON256 = path.join(APP_INSTALL_ROOT, 'ICON_256.PNG');
const APP_DESKTOP_SERVICE_NAME = 'clash-for-fnos.main';
const APPCENTER_DB_NAME = 'appcenter';
function normalizeAppIconId(input) {
  const id = String(input || '').trim();
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(id)) throw Object.assign(new Error('软件图标标识无效'), { statusCode: 400 });
  return id;
}

async function readAppIconManifest() {
  let parsed;
  try { parsed = JSON.parse(await fsp.readFile(APP_ICON_MANIFEST_FILE, 'utf8')); }
  catch (err) { throw Object.assign(new Error(`读取软件图标清单失败：${err.message || String(err)}`), { statusCode: 500 }); }
  const rawIcons = Array.isArray(parsed?.icons) ? parsed.icons : [];
  const icons = [];
  for (const raw of rawIcons) {
    const id = normalizeAppIconId(raw?.id);
    const file64 = path.join(APP_ICON_PRESET_DIR, `${id}_64.png`);
    const file256 = path.join(APP_ICON_PRESET_DIR, `${id}_256.png`);
    const web64 = path.join(APP_WEB_PUBLIC_DIR, 'icons', `${id}_64.png`);
    const web256 = path.join(APP_WEB_PUBLIC_DIR, 'icons', `${id}_256.png`);
    const files = [file64, file256, web64, web256];
    const exists = (await Promise.all(files.map(p => fsp.access(p, fs.constants.R_OK).then(() => true).catch(() => false)))).every(Boolean);
    if (!exists) continue;
    icons.push({
      id,
      name: String(raw?.name || id).slice(0, 80),
      description: String(raw?.description || '').slice(0, 160),
      file64, file256,
      preview: `/app/clash-for-fnos/icons/${id}_256.png`
    });
  }
  if (!icons.length) throw Object.assign(new Error('没有可用的软件图标预设'), { statusCode: 500 });
  const requestedDefault = String(parsed?.default || APP_ICON_DEFAULT);
  const defaultId = icons.some(x => x.id === requestedDefault) ? requestedDefault : icons[0].id;
  return { defaultId, icons };
}

async function readAppIconSettings(defaultId = APP_ICON_DEFAULT) {
  try {
    const parsed = JSON.parse(await fsp.readFile(APP_ICON_SETTINGS_FILE, 'utf8'));
    return { selected: normalizeAppIconId(parsed?.selected || defaultId) };
  } catch (err) {
    if (err?.code === 'ENOENT') return { selected: defaultId };
    return { selected: defaultId };
  }
}

async function writeAppIconSettings(selected) {
  await fsp.mkdir(ETC_DIR, { recursive: true, mode: 0o750 });
  const tmp = `${APP_ICON_SETTINGS_FILE}.${process.pid}.${Date.now()}.tmp`;
  await fsp.writeFile(tmp, JSON.stringify({ selected }, null, 2), { mode: 0o600 });
  await fsp.rename(tmp, APP_ICON_SETTINGS_FILE);
}

async function atomicIconCopy(src, dst) {
  await fsp.mkdir(path.dirname(dst), { recursive: true });
  const tmp = `${dst}.${process.pid}.${Date.now()}.tmp`;
  await fsp.copyFile(src, tmp);
  await fsp.chmod(tmp, 0o644).catch(() => {});
  await fsp.rename(tmp, dst);
}

async function materializeVersionedAppIcon(selected) {
  const [icon64, icon256] = await Promise.all([
    fsp.readFile(selected.file64),
    fsp.readFile(selected.file256)
  ]);
  const iconKey = versionedAppIconKey(selected.id, icon64, icon256);
  await atomicIconCopy(selected.file64, path.join(APP_ICON_PRESET_DIR, `${iconKey}_64.png`));
  await atomicIconCopy(selected.file256, path.join(APP_ICON_PRESET_DIR, `${iconKey}_256.png`));
  return { iconKey, entryIcon: appIconEntryPath(iconKey) };
}

async function updateAppUiIconReference(entryIcon) {
  let parsed;
  try { parsed = JSON.parse(await fsp.readFile(APP_UI_CONFIG_FILE, 'utf8')); }
  catch (err) { throw Object.assign(new Error(`读取 fnOS 桌面入口配置失败：${err.message || String(err)}`), { statusCode: 500 }); }
  const entries = parsed?.['.url'];
  const entry = entries?.['clash-for-fnos.main'] || (entries && Object.values(entries)[0]);
  if (!entry || typeof entry !== 'object') throw Object.assign(new Error('未找到 fnOS 桌面入口配置'), { statusCode: 500 });
  entry.icon = entryIcon;
  const tmp = `${APP_UI_CONFIG_FILE}.${process.pid}.${Date.now()}.tmp`;
  await fsp.writeFile(tmp, `${JSON.stringify(parsed, null, 2)}\n`, { mode: 0o644 });
  await fsp.chmod(tmp, 0o644).catch(() => {});
  await fsp.rename(tmp, APP_UI_CONFIG_FILE);
  return entry.icon;
}

function sqlString(value) {
  return `'${String(value ?? '').replace(/'/g, "''")}'`;
}

async function firstExecutable(candidates) {
  for (const candidate of candidates) {
    try { await fsp.access(candidate, fs.constants.X_OK); return candidate; } catch (_) {}
  }
  return null;
}

async function appcenterSql(sql) {
  const runuser = await firstExecutable(['/usr/sbin/runuser', '/usr/bin/runuser']);
  const psql = await firstExecutable(['/usr/bin/psql', '/bin/psql']);
  if (!runuser || !psql) {
    return { available: false, stdout: '', warning: !runuser ? '系统缺少 runuser，无法同步 fnOS 桌面入口' : '系统缺少 psql，无法同步 fnOS 桌面入口' };
  }
  const result = await runCommand(runuser, ['-u', 'postgres', '--', psql, '-X', '-qAt', '-F', '\t', '-d', APPCENTER_DB_NAME, '-v', 'ON_ERROR_STOP=1', '-c', sql], 12000);
  if (result.code !== 0) throw new Error(result.output || `psql 退出码 ${result.code}`);
  return { available: true, stdout: result.stdout, warning: null };
}

async function syncRegisteredDesktopIcon(entryIcon) {
  const registeredIcon = `ui/${entryIcon}`;
  const appName = APP_INSTALL_NAME;
  const serviceName = APP_DESKTOP_SERVICE_NAME;
  const selectSql = `SELECT s.id::text, s.service_name, COALESCE(s.icon, '')\nFROM public.app_service s\nJOIN public.app a ON a.id = s.app_id\nWHERE a.app_name = ${sqlString(appName)}\nORDER BY CASE WHEN s.service_name = ${sqlString(serviceName)} THEN 0 ELSE 1 END, s.id;`;
  try {
    const query = await appcenterSql(selectSql);
    if (!query.available) return { available: false, updated: false, registeredIcon, warning: query.warning };
    const rows = String(query.stdout || '').split(/\r?\n/).filter(Boolean).map(line => {
      const [id, service, icon = ''] = line.split('\t');
      return { id: Number(id), service: String(service || ''), icon: String(icon || '') };
    }).filter(row => Number.isInteger(row.id) && row.id > 0);
    if (!rows.length) return { available: true, updated: false, registeredIcon, warning: 'appcenter 未找到 Clash for fnos 桌面入口记录' };
    const exact = rows.filter(row => row.service === serviceName);
    const target = exact.length === 1 ? exact[0] : (rows.length === 1 ? rows[0] : null);
    if (!target) return { available: true, updated: false, registeredIcon, warning: '检测到多个桌面入口，未自动修改 appcenter 注册信息' };
    if (target.icon === registeredIcon) return { available: true, updated: false, matched: true, serviceId: target.id, serviceName: target.service, previousIcon: target.icon, registeredIcon };
    const updateSql = `UPDATE public.app_service SET icon = ${sqlString(registeredIcon)}, updated_at = NOW() WHERE id = ${target.id} RETURNING id::text;`;
    const update = await appcenterSql(updateSql);
    const changedId = Number(String(update.stdout || '').trim().split(/\s+/)[0]);
    if (changedId !== target.id) {
      return { available: false, updated: false, registeredIcon, warning: '同步 fnOS 桌面入口失败：appcenter 桌面入口更新结果异常' };
    }
    return { available: true, updated: true, matched: true, serviceId: target.id, serviceName: target.service, previousIcon: target.icon, registeredIcon };
  } catch (err) {
    return { available: false, updated: false, registeredIcon, warning: `同步 fnOS 桌面入口失败：${err.message || String(err)}` };
  }
}

async function appIconStatus() {
  const manifest = await readAppIconManifest();
  let state = await readAppIconSettings(manifest.defaultId);
  if (!manifest.icons.some(x => x.id === state.selected)) state = { selected: manifest.defaultId };
  return {
    ok: true,
    selected: state.selected,
    defaultId: manifest.defaultId,
    requiresWindowReload: true,
    requiresDesktopReload: true,
    options: manifest.icons.map(({ id, name, description, preview }) => ({ id, name, description, preview }))
  };
}

async function applyAppIcon(input, persist = true) {
  const manifest = await readAppIconManifest();
  const id = normalizeAppIconId(input || manifest.defaultId);
  const selected = manifest.icons.find(x => x.id === id);
  if (!selected) throw Object.assign(new Error('所选软件图标不存在'), { statusCode: 404 });
  // fnOS caches desktop/window icons by URL. Include a content revision in the
  // filename so switching presets and upgrading artwork cannot reuse stale pixels.
  const { iconKey, entryIcon } = await materializeVersionedAppIcon(selected);
  await updateAppUiIconReference(entryIcon);
  await atomicIconCopy(selected.file64, path.join(APP_UI_IMAGES_DIR, 'icon_64.png'));
  await atomicIconCopy(selected.file256, path.join(APP_UI_IMAGES_DIR, 'icon_256.png'));
  await atomicIconCopy(selected.file64, path.join(APP_WEB_PUBLIC_DIR, 'icon-current.png'));
  await atomicIconCopy(selected.file256, path.join(APP_WEB_PUBLIC_DIR, 'icon-current-256.png'));
  // Keep the installed package icons in sync too. App Center uses these package icons.
  let packageIconsChanged = false;
  try {
    const rootOk = await fsp.stat(APP_INSTALL_ROOT).then(st => st.isDirectory()).catch(() => false);
    if (rootOk) {
      // Keep the legacy package behavior used by releases through v0.8.5:
      // the installed App Center detail view renders ICON.PNG at a large size,
      // so both package-level filenames must contain the 256px asset.
      await atomicIconCopy(selected.file256, APP_INSTALL_ICON64);
      await atomicIconCopy(selected.file256, APP_INSTALL_ICON256);
      packageIconsChanged = true;
    }
  } catch (_) {}
  // fnOS registers desktop/window launch metadata into appcenter.app_service at install time.
  // Updating target/ui/config alone does not refresh that registered icon, so update only this
  // app's own service record. No other app rows or desktop DOM nodes are touched.
  const desktopRegistration = await syncRegisteredDesktopIcon(entryIcon);
  if (persist) await writeAppIconSettings(id);
  return { ...(await appIconStatus()), changed: true, iconKey, entryIcon, packageIconsChanged, desktopRegistration };
}

async function syncAppIcon() {
  const manifest = await readAppIconManifest();
  const state = await readAppIconSettings(manifest.defaultId);
  const selected = manifest.icons.some(x => x.id === state.selected) ? state.selected : manifest.defaultId;
  return applyAppIcon(selected, selected !== state.selected);
}

const pendingConfigTransactions = new Map();
const pendingCoreTransactions = new Map();
let managedChild = null;
let bootstrapPromise = null;
let bootstrapState = { state: 'idle', mode: null, delivery: fs.existsSync(ONLINE_CORE_MARKER_FILE) ? 'online' : 'bundled', message: '尚未检测', error: null, progress: 0, updatedAt: Date.now() };

function json(res, status, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Content-Length': Buffer.byteLength(body), 'Cache-Control': 'no-store' });
  res.end(body);
}

function bodyJson(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on('data', chunk => {
      size += chunk.length;
      if (size > MAX_CONFIG + 1024 * 1024) {
        reject(Object.assign(new Error('请求体过大'), { statusCode: 413 }));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => {
      try { resolve(chunks.length ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : {}); }
      catch (_) { reject(Object.assign(new Error('JSON 格式错误'), { statusCode: 400 })); }
    });
    req.on('error', reject);
  });
}


async function readProxyEnvFile(file) {
  try {
    const st = await fsp.stat(file);
    if (!st.isFile()) return { path: file, exists: false, readable: false, managed: false, variables: [] };
    const raw = await fsp.readFile(file, 'utf8');
    return { path: file, exists: true, readable: true, managed: raw.includes(PROXY_ENV_BEGIN) && raw.includes(PROXY_ENV_END), variables: parseProxyEnvFile(raw) };
  } catch (err) {
    return { path: file, exists: fs.existsSync(file), readable: false, managed: false, variables: [], error: err.message || String(err) };
  }
}

async function processProxyEnvironment(pid) {
  if (!pid) return { pid: null, variables: [] };
  try {
    const raw = await fsp.readFile(`/proc/${pid}/environ`);
    /** @type {Record<string, string>} */
    const env = {};
    for (const item of raw.toString('utf8').split('\0')) {
      if (!item) continue;
      const idx = item.indexOf('=');
      if (idx <= 0) continue;
      env[item.slice(0, idx)] = item.slice(idx + 1);
    }
    return { pid: Number(pid), variables: proxyEnvFromObject(env) };
  } catch (err) {
    return { pid: Number(pid), variables: [], error: err.message || String(err) };
  }
}

const PROXY_ENV_TARGETS = [
  { key: 'environment', path: '/etc/environment', shell: false },
  { key: 'profile', path: '/etc/profile', shell: true },
  { key: 'bashrc', path: '/etc/bash.bashrc', shell: true }
];
async function readProxyEnvSettings() {
  try {
    const raw = await fsp.readFile(PROXY_ENV_SETTINGS_FILE, 'utf8');
    return normalizeProxyEnvSettings(JSON.parse(raw), DEFAULT_PROXY_ENV_SETTINGS);
  } catch (err) {
    if (err?.code === 'ENOENT') return normalizeProxyEnvSettings({}, DEFAULT_PROXY_ENV_SETTINGS);
    throw Object.assign(new Error(`代理环境变量设置文件无效：${err.message || String(err)}`), { statusCode: 500 });
  }
}

async function writeProxyEnvSettings(settings) {
  await fsp.mkdir(ETC_DIR, { recursive: true, mode: 0o750 });
  const tmp = `${PROXY_ENV_SETTINGS_FILE}.${process.pid}.${Date.now()}.tmp`;
  await fsp.writeFile(tmp, JSON.stringify(settings, null, 2), { mode: 0o600 });
  await fsp.rename(tmp, PROXY_ENV_SETTINGS_FILE);
}

async function proxyEnvFileSnapshot(file) {
  try {
    const st = await fsp.stat(file);
    if (!st.isFile()) return { path: file, exists: false, raw: '', stat: null };
    return { path: file, exists: true, raw: await fsp.readFile(file, 'utf8'), stat: st };
  } catch (err) {
    if (err?.code === 'ENOENT') return { path: file, exists: false, raw: '', stat: null };
    throw err;
  }
}

async function backupProxyEnvSnapshot(snapshot) {
  if (!snapshot.exists) return null;
  await fsp.mkdir(PROXY_ENV_BACKUP_DIR, { recursive: true, mode: 0o750 });
  const backup = path.join(PROXY_ENV_BACKUP_DIR, `${path.basename(snapshot.path)}.${safeStamp()}.bak`);
  await fsp.writeFile(backup, snapshot.raw, { mode: snapshot.stat.mode & 0o7777 });
  await fsp.chown(backup, snapshot.stat.uid, snapshot.stat.gid).catch(() => {});
  return backup;
}

async function atomicWriteProxyEnvFile(snapshot, raw) {
  const file = snapshot.path;
  if (!snapshot.exists && !raw) return;
  if (!raw && !snapshot.exists) return;
  const tmp = path.join(path.dirname(file), `.${path.basename(file)}.${process.pid}.${Date.now()}.clash-for-fnos.tmp`);
  const mode = snapshot.stat ? snapshot.stat.mode & 0o7777 : 0o644;
  await fsp.writeFile(tmp, raw, { mode });
  if (snapshot.stat) await fsp.chown(tmp, snapshot.stat.uid, snapshot.stat.gid).catch(() => {});
  else await fsp.chown(tmp, 0, 0).catch(() => {});
  await fsp.rename(tmp, file);
}

async function restoreProxyEnvSnapshot(snapshot) {
  if (!snapshot.exists) {
    await fsp.unlink(snapshot.path).catch(err => { if (err?.code !== 'ENOENT') throw err; });
    return;
  }
  await atomicWriteProxyEnvFile({ ...snapshot, exists: true }, snapshot.raw);
}

async function proxyEnvironmentRuntime(settings) {
  const net = await networkStatus().catch(() => null);
  const mixed = net?.settings?.mixed || { enabled: false, port: settings.port || 7890 };
  const port = settings.followMixedPort ? Number(mixed.port || settings.port || 7890) : Number(settings.port || 7890);
  const active = Boolean(settings.enabled && (!settings.followMixedPort || mixed.enabled));
  return {
    active,
    port,
    proxyUrl: `http://127.0.0.1:${port}`,
    mixedEnabled: Boolean(mixed.enabled),
    mixedPort: Number(mixed.port || 0) || null,
    suspendedReason: settings.enabled && settings.followMixedPort && !mixed.enabled ? 'mixed-port-disabled' : null
  };
}

async function applyProxyEnvironmentSettings(settings, options = {}) {
  const normalized = normalizeProxyEnvSettings(settings, await readProxyEnvSettings());
  const runtime = await proxyEnvironmentRuntime(normalized);
  const snapshots = [];
  const changed = [];
  const backups = [];
  try {
    for (const target of PROXY_ENV_TARGETS) {
      const snapshot = await proxyEnvFileSnapshot(target.path);
      snapshots.push(snapshot);
      const selected = Boolean(normalized.targets[target.key]);
      const block = runtime.active && selected ? managedProxyEnvBlock(runtime.port, normalized.noProxy, target.shell) : '';
      const nextRaw = withManagedProxyEnvBlock(snapshot.raw, block);
      if (nextRaw === snapshot.raw) continue;
      const backup = await backupProxyEnvSnapshot(snapshot);
      await atomicWriteProxyEnvFile(snapshot, nextRaw);
      changed.push(target.path);
      if (backup) backups.push(backup);
    }
    if (options.saveSettings !== false) await writeProxyEnvSettings(normalized);
  } catch (err) {
    for (const snapshot of snapshots.reverse()) {
      if (!changed.includes(snapshot.path)) continue;
      await restoreProxyEnvSnapshot(snapshot).catch(() => {});
    }
    throw err;
  }
  return { ok: true, settings: normalized, runtime, changed, backups };
}

async function updateSystemProxyEnvironment(body) {
  const current = await readProxyEnvSettings();
  const next = normalizeProxyEnvSettings(body || {}, current);
  const result = await applyProxyEnvironmentSettings(next, { saveSettings: true });
  return { ...await systemProxyEnvironment(), operation: result };
}

async function syncSystemProxyEnvironment() {
  const settings = await readProxyEnvSettings();
  const result = await applyProxyEnvironmentSettings(settings, { saveSettings: false });
  return { ...await systemProxyEnvironment(), operation: result };
}

async function systemProxyEnvironment() {
  const files = await Promise.all(['/etc/profile', '/etc/bash.bashrc', '/etc/environment'].map(readProxyEnvFile));
  const proc = await detectPrimaryMihomo({ allowMissing: true }).catch(() => null);
  const settings = await readProxyEnvSettings();
  const runtime = await proxyEnvironmentRuntime(settings);
  return {
    ok: true,
    files,
    management: { settings, ...runtime },
    helperEnvironment: proxyEnvFromObject(process.env),
    mihomoEnvironment: await processProxyEnvironment(proc?.pid || null),
    mihomoPid: proc?.pid || null
  };
}

function optionValue(args, names) {
  for (let i = 1; i < args.length; i++) {
    const arg = String(args[i] || '');
    for (const name of names) {
      if (arg === name && i + 1 < args.length) return args[i + 1];
      if (arg.startsWith(`${name}=`)) return arg.slice(name.length + 1);
    }
  }
  return null;
}

async function procInfo(pid) {
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
    if (!/(mihomo|clash-meta)/i.test(`${base} ${argv0}`) || /clash-for-fnos/i.test(`${base} ${argv0}`)) return null;
    let cgroup = '';
    try { cgroup = await fsp.readFile(`/proc/${pid}/cgroup`, 'utf8'); } catch (_) {}
    const containerized = /(docker|containerd|kubepods|lxc)/i.test(cgroup);
    const unitMatch = cgroup.match(/(?:^|\/)([^/\n]+\.service)(?:$|\n|\/)/m);
    const unit = unitMatch ? unitMatch[1] : null;
    return { pid: Number(pid), exe: exe || args[0], cwd, args, containerized, unit };
  } catch (_) { return null; }
}

function resolveArg(value, proc, baseDir = null) {
  if (!value) return null;
  if (path.isAbsolute(value)) return path.normalize(value);
  return path.resolve(baseDir || proc.cwd || '/', value);
}

async function listMihomoProcesses() {
  let entries = [];
  try { entries = (await fsp.readdir('/proc')).filter(x => /^\d+$/.test(x)); } catch (_) {}
  const list = [];
  for (const pid of entries) {
    const proc = await procInfo(pid);
    if (!proc) continue;
    const dirArg = optionValue(proc.args, ['-d', '--dir', '--home-dir']);
    const fileArg = optionValue(proc.args, ['-f', '--config', '--config-file', '-config']);
    const configDir = resolveArg(dirArg, proc) || null;
    let configPath = null;
    if (fileArg) configPath = resolveArg(fileArg, proc, configDir);
    else if (configDir) configPath = path.join(configDir, 'config.yaml');
    const exeNorm = String(proc.exe || '').replace(/ \(deleted\)$/, '');
    const managed = path.normalize(exeNorm) === path.normalize(MANAGED_CORE_BIN);
    list.push({ ...proc, managed, configDir: configDir || (configPath ? path.dirname(configPath) : null), configPath });
  }
  list.sort((a, b) => Number(a.containerized) - Number(b.containerized) || Number(!a.configPath) - Number(!b.configPath) || a.pid - b.pid);
  return list;
}

async function detectPrimaryMihomo(options = {}) {
  const list = await listMihomoProcesses();
  const external = list.find(x => !x.containerized && !x.managed && x.configPath) || list.find(x => !x.containerized && !x.managed);
  const managed = list.find(x => !x.containerized && x.managed);
  const preferManaged = Boolean(managed && fs.existsSync(BOOTSTRAP_META_FILE));
  const primary = preferManaged ? managed : (external || managed || list[0] || null);
  if (!primary) {
    if (options.allowMissing) return null;
    throw Object.assign(new Error('未检测到正在运行的 Mihomo 进程'), { statusCode: 404 });
  }
  if (primary.containerized) throw Object.assign(new Error('当前 Mihomo 位于容器中，暂不允许直接修改宿主机启动配置/内核'), { statusCode: 409 });
  if (!path.isAbsolute(primary.exe || '')) throw Object.assign(new Error('无法确定 Mihomo 二进制绝对路径'), { statusCode: 409 });
  return primary;
}

async function findExternalInstallation() {
  const candidates = ['/usr/local/bin/mihomo', '/usr/bin/mihomo', '/opt/mihomo/mihomo', '/usr/local/bin/clash-meta', '/usr/bin/clash-meta'];
  for (const file of candidates) {
    if (path.normalize(file) === path.normalize(MANAGED_CORE_BIN)) continue;
    try {
      const st = await fsp.stat(file);
      if (!st.isFile()) continue;
      const configs = ['/etc/mihomo/config.yaml', '/etc/clash/config.yaml', '/usr/local/etc/mihomo/config.yaml', '/opt/mihomo/config.yaml'];
      const configPath = configs.find(x => fs.existsSync(x)) || null;
      return { binaryPath: file, configPath };
    } catch (_) {}
  }
  return null;
}

function setBootstrap(state, patch = {}) {
  bootstrapState = { ...bootstrapState, state, ...patch, updatedAt: Date.now() };
}

function portAvailable(port) {
  return new Promise(resolve => {
    const server = net.createServer();
    server.unref();
    server.once('error', () => resolve(false));
    server.listen({ host: '127.0.0.1', port, exclusive: true }, () => server.close(() => resolve(true)));
  });
}

async function choosePort(start, span = 20) {
  for (let port = start; port < start + span; port++) if (await portAvailable(port)) return port;
  throw new Error(`无法在 ${start}-${start + span - 1} 找到可用端口`);
}

function yamlInlineValue(rawValue) {
  const text = String(rawValue ?? '').trim();
  if (!text) return '';
  let quote = null;
  let escaped = false;
  let end = text.length;
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quote) {
      if (quote === '"' && escaped) { escaped = false; continue; }
      if (quote === '"' && ch === '\\') { escaped = true; continue; }
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") { quote = ch; continue; }
    if (ch === '#' && (i === 0 || /\s/.test(text[i - 1]))) { end = i; break; }
  }
  let value = text.slice(0, end).trim();
  if (value.length >= 2 && value[0] === '"' && value[value.length - 1] === '"') {
    try { return JSON.parse(value); } catch (_) { return value.slice(1, -1); }
  }
  if (value.length >= 2 && value[0] === "'" && value[value.length - 1] === "'") {
    return value.slice(1, -1).replace(/''/g, "'");
  }
  return value;
}

function topLevelScalar(raw, key) {
  for (const line of String(raw || '').split(/\r?\n/)) {
    if (!line || /^\s/.test(line) || /^\s*#/.test(line)) continue;
    const m = line.match(/^([A-Za-z0-9_-]+)\s*:\s*(.*)$/);
    if (m && m[1] === key) return { present: true, value: yamlInlineValue(m[2]) };
  }
  return { present: false, value: null };
}

function yamlBool(value, fallback = false) {
  const v = String(value ?? '').trim().toLowerCase();
  if (['true', 'yes', 'on', '1'].includes(v)) return true;
  if (['false', 'no', 'off', '0'].includes(v)) return false;
  return fallback;
}

function yamlPort(value, fallback = null) {
  const n = Number(String(value ?? '').trim());
  return Number.isInteger(n) && n >= 1 && n <= 65535 ? n : fallback;
}

function topLevelBlock(raw, key) {
  const lines = String(raw || '').split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line || /^\s/.test(line) || /^\s*#/.test(line)) continue;
    const m = line.match(/^([A-Za-z0-9_-]+)\s*:\s*(.*)$/);
    if (!m || m[1] !== key) continue;
    const tail = String(m[2] || '').trim();
    if (tail && !tail.startsWith('#')) return { present: true, text: line, inline: true };
    let end = i + 1;
    while (end < lines.length) {
      const next = lines[end];
      if (next && !/^\s/.test(next) && !/^\s*#/.test(next) && /^[A-Za-z0-9_-]+\s*:/.test(next)) break;
      end++;
    }
    return { present: true, text: lines.slice(i, end).join('\n').replace(/\s+$/, ''), inline: false };
  }
  return { present: false, text: '', inline: false };
}

function stripTopLevelBlocks(raw, keys) {
  const set = new Set(keys);
  const lines = String(raw || '').split(/\r?\n/);
  const out = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (!line || /^\s/.test(line) || /^\s*#/.test(line)) { out.push(line); continue; }
    const m = line.match(/^([A-Za-z0-9_-]+)\s*:\s*(.*)$/);
    if (!m || !set.has(m[1])) { out.push(line); continue; }
    const tail = String(m[2] || '').trim();
    if (tail && !tail.startsWith('#')) continue;
    while (i + 1 < lines.length) {
      const next = lines[i + 1];
      if (next && !/^\s/.test(next) && !/^\s*#/.test(next) && /^[A-Za-z0-9_-]+\s*:/.test(next)) break;
      i++;
    }
  }
  return out.join('\n').replace(/^\s+/, '').replace(/\s+$/, '') + '\n';
}

function nestedScalar(blockText, key) {
  const text = String(blockText || '');
  const first = text.split(/\r?\n/, 1)[0] || '';
  const inline = first.match(/^\s*[A-Za-z0-9_-]+\s*:\s*\{(.*)}\s*(?:#.*)?$/);
  if (inline) {
    const re = new RegExp(`(?:^|,)\\s*${key.replace(/[.*+?^${}()|[\\]\\\\]/g, '\\$&')}\\s*:\\s*([^,}]+)`, 'i');
    const m = inline[1].match(re);
    return m ? { present: true, value: yamlInlineValue(m[1]) } : { present: false, value: null };
  }
  for (const line of text.split(/\r?\n/).slice(1)) {
    const m = line.match(/^\s+([A-Za-z0-9_-]+)\s*:\s*(.*)$/);
    if (m && m[1] === key) return { present: true, value: yamlInlineValue(m[2]) };
  }
  return { present: false, value: null };
}

function stripIpv6Brackets(value) {
  const text = String(value ?? '');
  return text.startsWith('[') && text.endsWith(']') ? text.slice(1, -1) : text;
}

function parseListenAddress(raw, fallbackPort = 9090) {
  let value = String(raw || '').trim();
  if (!value) return { host: '127.0.0.1', port: fallbackPort };
  if (/^https?:\/\//i.test(value)) {
    try {
      const u = new URL(value);
      return { host: stripIpv6Brackets(u.hostname) || '127.0.0.1', port: Number(u.port || fallbackPort) };
    } catch (_) { return { host: '127.0.0.1', port: fallbackPort }; }
  }
  if (/^\d+$/.test(value)) return { host: '127.0.0.1', port: yamlPort(value, fallbackPort) };
  if (/^:\d+$/.test(value)) return { host: '127.0.0.1', port: yamlPort(value.slice(1), fallbackPort), legacyShortListen: true };
  const bracket = value.match(/^\[([^]]+)]:(\d+)$/);
  if (bracket) return { host: bracket[1], port: yamlPort(bracket[2], fallbackPort) };
  const idx = value.lastIndexOf(':');
  if (idx > 0) return { host: value.slice(0, idx).trim(), port: yamlPort(value.slice(idx + 1), fallbackPort) };
  return { host: '127.0.0.1', port: fallbackPort };
}

function formatListenAddress(host, port) {
  let h = String(host ?? '').trim();
  // The Settings UI is intentionally local-only. Never regenerate the legacy
  // ':port' wildcard form that older Clash for fnos versions produced.
  if (!h || ['0.0.0.0', '::', '[::]', '*'].includes(h)) h = '127.0.0.1';
  if (h.includes(':') && !h.startsWith('[')) return `[${h}]:${port}`;
  return `${h}:${port}`;
}


function portSetting(raw, key, fallback) {
  const v = topLevelScalar(raw, key);
  return { enabled: v.present && yamlPort(v.value, null) !== null, port: yamlPort(v.value, fallback) || fallback };
}

function tunSettings(raw) {
  const block = topLevelBlock(raw, 'tun');
  if (!block.present) return { enabled: false, stack: 'mixed', autoRoute: true, autoRedirect: true, autoDetectInterface: true, strictRoute: false, dnsHijack: true, mtu: 9000 };
  const get = (key) => nestedScalar(block.text, key);
  const enable = get('enable');
  const stack = get('stack');
  const autoRoute = get('auto-route');
  const autoRedirect = get('auto-redirect');
  const autoDetect = get('auto-detect-interface');
  const strictRoute = get('strict-route');
  const mtu = get('mtu');
  const dnsHijack = /(^|\n)\s+dns-hijack\s*:/m.test(block.text) || /dns-hijack\s*:/i.test(block.text.split(/\r?\n/, 1)[0] || '');
  return {
    enabled: enable.present ? yamlBool(enable.value, false) : false,
    stack: ['mixed', 'system', 'gvisor'].includes(String(stack.value || '').toLowerCase()) ? String(stack.value).toLowerCase() : 'mixed',
    autoRoute: autoRoute.present ? yamlBool(autoRoute.value, true) : true,
    autoRedirect: autoRedirect.present ? yamlBool(autoRedirect.value, true) : true,
    autoDetectInterface: autoDetect.present ? yamlBool(autoDetect.value, true) : true,
    strictRoute: strictRoute.present ? yamlBool(strictRoute.value, false) : false,
    dnsHijack,
    mtu: Number.isInteger(Number(mtu.value)) && Number(mtu.value) >= 1280 && Number(mtu.value) <= 65535 ? Number(mtu.value) : 9000
  };
}

async function tunCapability(proc) {
  let uid = null;
  let capEff = 0n;
  if (proc?.pid) {
    try {
      const status = await fsp.readFile(`/proc/${proc.pid}/status`, 'utf8');
      uid = Number(status.match(/^Uid:\s+(\d+)/m)?.[1] ?? NaN);
      const cap = status.match(/^CapEff:\s+([0-9A-Fa-f]+)/m)?.[1];
      if (cap) capEff = BigInt(`0x${cap}`);
    } catch (_) {}
  }
  const netAdmin = uid === 0 || (capEff & (1n << 12n)) !== 0n;
  const tunDevice = fs.existsSync('/dev/net/tun');
  return { supported: Boolean(netAdmin && tunDevice), uid: Number.isFinite(uid) ? uid : null, netAdmin, tunDevice };
}

function parseNetworkSettings(raw) {
  const plain = topLevelScalar(raw, 'external-controller');
  const tls = topLevelScalar(raw, 'external-controller-tls');
  const controllerKey = plain.present ? 'external-controller' : (tls.present ? 'external-controller-tls' : 'external-controller');
  const controllerRaw = plain.present ? plain.value : (tls.present ? tls.value : '127.0.0.1:9090');
  const controllerListen = parseListenAddress(controllerRaw, 9090);
  const allowLan = topLevelScalar(raw, 'allow-lan');
  const dnsBlock = topLevelBlock(raw, 'dns');
  const hostsBlock = topLevelBlock(raw, 'hosts');
  const globalIpv6 = topLevelScalar(raw, 'ipv6');
  const unifiedDelay = topLevelScalar(raw, 'unified-delay');
  const dns = dnsBlock.present ? parseDnsSettingsBlock(dnsBlock.text) : { ...DEFAULT_DNS_SETTINGS, enable: false };
  dns.hosts = hostsBlock.present ? parseHostsBlock(hostsBlock.text) : [];
  return {
    controller: { key: controllerKey, host: controllerListen.host, port: controllerListen.port, protocol: controllerKey === 'external-controller-tls' ? 'https' : 'http', needsLoopbackNormalization: controllerListen.legacyShortListen === true },
    mixed: portSetting(raw, 'mixed-port', 7897),
    socks: portSetting(raw, 'socks-port', 7898),
    http: portSetting(raw, 'port', 7899),
    redir: portSetting(raw, 'redir-port', 7895),
    tproxy: portSetting(raw, 'tproxy-port', 7896),
    allowLan: allowLan.present ? yamlBool(allowLan.value, false) : false,
    dnsEnabled: dns.enable,
    dns,
    core: {
      ipv6: globalIpv6.present ? yamlBool(globalIpv6.value, true) : true,
      unifiedDelay: unifiedDelay.present ? yamlBool(unifiedDelay.value, false) : false
    },
    tun: tunSettings(raw)
  };
}

function requirePort(value, name) {
  const port = Number(value);
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw Object.assign(new Error(`${name} 必须是 1-65535 的端口`), { statusCode: 400 });
  return port;
}

function normalizeNetworkInput(body, current) {
  const updates = { dns: body?.dns !== undefined, core: body?.core !== undefined };
  const portObj = (name, label) => {
    const src = body?.[name];
    if (src === undefined) return { ...current[name] };
    return { enabled: Boolean(src.enabled), port: requirePort(src.port ?? current[name].port, label) };
  };
  const controller = { ...current.controller, host: '127.0.0.1', port: requirePort(body?.controller?.port ?? current.controller.port, 'Controller'), needsLoopbackNormalization: false };
  const tun = { ...current.tun, ...(body?.tun || {}) };
  tun.enabled = Boolean(tun.enabled);
  tun.stack = ['mixed', 'system', 'gvisor'].includes(String(tun.stack || '').toLowerCase()) ? String(tun.stack).toLowerCase() : 'mixed';
  tun.autoRoute = tun.autoRoute !== false;
  tun.autoRedirect = tun.autoRoute && tun.autoRedirect !== false;
  tun.autoDetectInterface = tun.autoDetectInterface !== false;
  tun.strictRoute = Boolean(tun.strictRoute);
  tun.dnsHijack = tun.dnsHijack !== false;
  tun.mtu = Number(tun.mtu || 9000);
  if (!Number.isInteger(tun.mtu) || tun.mtu < 1280 || tun.mtu > 65535) throw Object.assign(new Error('TUN MTU 必须是 1280-65535'), { statusCode: 400 });
  const dns = body?.dns === undefined ? current.dns : normalizeDnsSettings(body.dns, current.dns);
  const coreInput = body?.core && typeof body.core === 'object' && !Array.isArray(body.core) ? body.core : {};
  const core = {
    ipv6: coreInput.ipv6 === undefined ? current.core.ipv6 : Boolean(coreInput.ipv6),
    unifiedDelay: coreInput.unifiedDelay === undefined ? current.core.unifiedDelay : Boolean(coreInput.unifiedDelay)
  };
  const normalized = {
    controller,
    mixed: portObj('mixed', 'Mixed Port'),
    socks: portObj('socks', 'SOCKS Port'),
    http: portObj('http', 'HTTP Port'),
    redir: portObj('redir', 'Redir Port'),
    tproxy: portObj('tproxy', 'TProxy Port'),
    allowLan: body?.allowLan === undefined ? current.allowLan : Boolean(body.allowLan),
    dns,
    core,
    _updates: updates,
    tun
  };
  const active = [normalized.controller.port];
  for (const key of ['mixed', 'socks', 'http', 'redir', 'tproxy']) if (normalized[key].enabled) active.push(normalized[key].port);
  if (new Set(active).size !== active.length) throw Object.assign(new Error('启用的端口不能重复'), { statusCode: 400 });
  return normalized;
}

function renderTunBlock(tun, existingBlock = '') {
  const known = new Set(['enable', 'stack', 'auto-route', 'auto-redirect', 'auto-detect-interface', 'strict-route', 'mtu', 'dns-hijack']);
  const unknown = [];
  const rawLines = String(existingBlock || '').split(/\r?\n/).slice(1);
  let minIndent = Infinity;
  for (const line of rawLines) {
    if (!line.trim() || /^\s*#/.test(line)) continue;
    const m = line.match(/^(\s+)[A-Za-z0-9_-]+\s*:/);
    if (m) minIndent = Math.min(minIndent, m[1].length);
  }
  if (!Number.isFinite(minIndent)) minIndent = 2;
  for (let i = 0; i < rawLines.length; i++) {
    const line = rawLines[i];
    const m = line.match(/^(\s+)([A-Za-z0-9_-]+)\s*:/);
    if (!m || m[1].length !== minIndent || !known.has(m[2])) { unknown.push(line); continue; }
    const indent = m[1].length;
    while (i + 1 < rawLines.length) {
      const next = rawLines[i + 1];
      if (!next.trim()) { i++; continue; }
      const lead = next.match(/^(\s*)/)[1].length;
      if (lead <= indent && !/^\s*#/.test(next)) break;
      i++;
    }
  }

  const lines = ['tun:'];
  lines.push(`  enable: ${tun.enabled ? 'true' : 'false'}`);
  lines.push(`  stack: ${tun.stack}`);
  lines.push(`  auto-route: ${tun.autoRoute ? 'true' : 'false'}`);
  lines.push(`  auto-redirect: ${tun.autoRedirect ? 'true' : 'false'}`);
  lines.push(`  auto-detect-interface: ${tun.autoDetectInterface ? 'true' : 'false'}`);
  lines.push(`  strict-route: ${tun.strictRoute ? 'true' : 'false'}`);
  lines.push(`  mtu: ${tun.mtu}`);
  if (tun.dnsHijack) {
    lines.push('  dns-hijack:');
    lines.push('    - any:53');
    lines.push('    - tcp://any:53');
  }
  const cleanedUnknown = unknown.join('\n').replace(/^\s*\n+|\n+\s*$/g, '');
  if (cleanedUnknown) lines.push(cleanedUnknown);
  return lines.join('\n');
}

function renderNetworkOverrides(settings, existingTun = '', existingDns = '', existingHosts = '') {
  const lines = [];
  lines.push(`${settings.controller.key}: ${formatListenAddress(settings.controller.host, settings.controller.port)}`);
  if (settings.mixed.enabled) lines.push(`mixed-port: ${settings.mixed.port}`);
  if (settings.socks.enabled) lines.push(`socks-port: ${settings.socks.port}`);
  if (settings.http.enabled) lines.push(`port: ${settings.http.port}`);
  if (settings.redir.enabled) lines.push(`redir-port: ${settings.redir.port}`);
  if (settings.tproxy.enabled) lines.push(`tproxy-port: ${settings.tproxy.port}`);
  lines.push(`allow-lan: ${settings.allowLan ? 'true' : 'false'}`);
  if (settings._updates.core) {
    lines.push(`ipv6: ${settings.core.ipv6 ? 'true' : 'false'}`);
    lines.push(`unified-delay: ${settings.core.unifiedDelay ? 'true' : 'false'}`);
  }
  lines.push(renderTunBlock(settings.tun, existingTun));
  if (settings._updates.dns) {
    lines.push(renderDnsSettingsBlock(settings.dns, existingDns));
    lines.push(renderHostsBlock(settings.dns.hosts, existingHosts));
  }
  return lines.join('\n');
}

function applyNetworkOverrides(raw, settings) {
  const oldTun = topLevelBlock(raw, 'tun');
  const oldDns = topLevelBlock(raw, 'dns');
  const oldHosts = topLevelBlock(raw, 'hosts');
  const keys = [settings.controller.key, 'mixed-port', 'socks-port', 'port', 'redir-port', 'tproxy-port', 'allow-lan', 'tun'];
  if (settings._updates.core) keys.push('ipv6', 'unified-delay');
  if (settings._updates.dns) keys.push('dns', 'hosts');
  const stripped = stripTopLevelBlocks(raw, keys);
  return `${renderNetworkOverrides(settings, oldTun.present ? oldTun.text : '', oldDns.present ? oldDns.text : '', oldHosts.present ? oldHosts.text : '')}\n\n${stripped.replace(/^\s+/, '')}`;
}

function controllerUrlFromListenAddress(raw, protocol = 'http') {
  let value = String(raw || '').trim();
  if (!value) return null;
  if (/^https?:\/\//i.test(value)) {
    try {
      const u = new URL(value);
      if (['0.0.0.0', '::', '[::]', '*'].includes(u.hostname)) u.hostname = '127.0.0.1';
      return u.toString().replace(/\/$/, '');
    } catch (_) { return null; }
  }
  if (/^(unix|pipe):/i.test(value) || value.startsWith('/')) return null;

  let host = '';
  let portText = '';
  if (/^\d+$/.test(value)) {
    portText = value;
  } else if (/^:\d+$/.test(value)) {
    portText = value.slice(1);
  } else {
    const bracket = value.match(/^\[([^]]+)]:(\d+)$/);
    if (bracket) {
      host = bracket[1];
      portText = bracket[2];
    } else {
      const i = value.lastIndexOf(':');
      if (i <= 0) return null;
      host = value.slice(0, i).trim();
      portText = value.slice(i + 1).trim();
    }
  }
  const port = Number(portText);
  if (!Number.isInteger(port) || port < 1 || port > 65535) return null;
  host = stripIpv6Brackets(host);
  if (!host || ['0.0.0.0', '::', '*'].includes(host)) host = '127.0.0.1';
  const displayHost = host.includes(':') ? `[${host}]` : host;
  return `${protocol}://${displayHost}:${port}`;
}

async function detectControllerAccess(configPath) {
  if (!configPath) return { readable: false, controller: null, secretPresent: false, configPath: null };
  try {
    const raw = await fsp.readFile(configPath, 'utf8');
    const plain = topLevelScalar(raw, 'external-controller');
    const tlsController = topLevelScalar(raw, 'external-controller-tls');
    const secret = topLevelScalar(raw, 'secret');
    let controller = null;
    let key = null;
    if (plain.present && plain.value) {
      controller = controllerUrlFromListenAddress(plain.value, 'http');
      key = controller ? 'external-controller' : null;
    }
    if (!controller && tlsController.present && tlsController.value) {
      controller = controllerUrlFromListenAddress(tlsController.value, 'https');
      key = controller ? 'external-controller-tls' : null;
    }
    return {
      readable: true,
      controller,
      controllerKey: key,
      secretPresent: secret.present,
      secret: secret.present ? String(secret.value || '') : undefined,
      configPath
    };
  } catch (err) {
    return { readable: false, controller: null, secretPresent: false, configPath, error: err.message || String(err) };
  }
}

async function ensureManagedConfig() {
  await fsp.mkdir(MANAGED_CONFIG_DIR, { recursive: true, mode: 0o750 });
  if (fs.existsSync(MANAGED_CONFIG_FILE)) {
    const raw = await fsp.readFile(MANAGED_CONFIG_FILE, 'utf8');
    const effectiveRaw = enforceManagedGeoConfig(raw);
    if (effectiveRaw !== raw) {
      const tmp = `${MANAGED_CONFIG_FILE}.${process.pid}.${Date.now()}.geo.tmp`;
      await fsp.writeFile(tmp, effectiveRaw, { mode: 0o640 });
      await fsp.rename(tmp, MANAGED_CONFIG_FILE);
    }
    const controller = effectiveRaw.match(/^external-controller\s*:\s*([^#\r\n]+)/m)?.[1]?.trim().replace(/^['"]|['"]$/g, '') || '127.0.0.1:9090';
    const secret = effectiveRaw.match(/^secret\s*:\s*([^#\r\n]*)/m)?.[1]?.trim().replace(/^['"]|['"]$/g, '') || '';
    const mixedPort = Number(effectiveRaw.match(/^mixed-port\s*:\s*(\d+)/m)?.[1] || 7890);
    return { configPath: MANAGED_CONFIG_FILE, controller, secret, mixedPort, existing: true };
  }
  const mixedPort = await choosePort(7890, 30);
  const controllerPort = await choosePort(9090, 30);
  const secret = crypto.randomBytes(24).toString('hex');
  const baseRaw = `# Generated by Clash for fnos\nmixed-port: ${mixedPort}\nallow-lan: false\nmode: rule\nlog-level: info\nexternal-controller: 127.0.0.1:${controllerPort}\nsecret: "${secret}"\nprofile:\n  store-selected: true\n${renderDnsSettingsBlock(DEFAULT_DNS_SETTINGS)}\nrules:\n  - MATCH,DIRECT\n`;
  const raw = enforceManagedGeoConfig(baseRaw);
  const tmp = `${MANAGED_CONFIG_FILE}.${process.pid}.${Date.now()}.tmp`;
  await fsp.writeFile(tmp, raw, { mode: 0o640 });
  await fsp.rename(tmp, MANAGED_CONFIG_FILE);
  return { configPath: MANAGED_CONFIG_FILE, controller: `127.0.0.1:${controllerPort}`, secret, mixedPort, existing: false };
}

function managedControllerUrl(meta) {
  const raw = String(meta?.controller || '127.0.0.1:9090');
  return /^https?:\/\//i.test(raw) ? raw : `http://${raw}`;
}

function mihomoChildEnv() {
  // Mihomo is itself the proxy engine. Inheriting a system HTTP(S)/ALL proxy
  // that points back to its own mixed-port can create a self-proxy loop and
  // break Provider downloads. Always let Mihomo use its own routing stack.
  const env = { ...process.env };
  for (const key of ['http_proxy','https_proxy','all_proxy','HTTP_PROXY','HTTPS_PROXY','ALL_PROXY']) delete env[key];
  return env;
}

async function managedProcess() {
  const list = await listMihomoProcesses();
  return list.find(x => x.managed && !x.containerized) || null;
}

async function startManagedCore(options = {}) {
  const existing = await managedProcess();
  if (existing) return existing;
  if (!fs.existsSync(MANAGED_CORE_BIN)) throw new Error('Manager 托管 Mihomo Core 尚未安装');
  const cfg = await ensureManagedConfig();
  const validation = options.skipValidation
    ? '跳过预校验；由实际 Core 启动与 Controller 健康检查确认配置'
    : await validateConfig(MANAGED_CORE_BIN, MANAGED_CONFIG_FILE, MANAGED_CONFIG_DIR);
  await fsp.mkdir(MANAGED_CORE_DIR, { recursive: true, mode: 0o750 });
  const out = fs.openSync(MANAGED_CORE_LOG, 'a');
  const child = spawn(MANAGED_CORE_BIN, ['-d', MANAGED_CONFIG_DIR], { stdio: ['ignore', out, out], env: mihomoChildEnv() });
  managedChild = child;
  await fsp.writeFile(MANAGED_CORE_PID, String(child.pid), { mode: 0o600 });
  child.once('exit', () => { if (managedChild?.pid === child.pid) managedChild = null; fsp.unlink(MANAGED_CORE_PID).catch(() => {}); });
  for (let i = 0; i < 40; i++) {
    await new Promise(r => setTimeout(r, 250));
    const proc = await managedProcess();
    if (proc) return { ...proc, validation, controller: managedControllerUrl(cfg), secret: cfg.secret, mixedPort: cfg.mixedPort };
  }
  throw new Error('Manager 托管 Mihomo Core 启动超时');
}

async function stopManagedCore() {
  const proc = await managedProcess();
  if (!proc) return false;
  try { process.kill(proc.pid, 'SIGTERM'); } catch (_) {}
  for (let i = 0; i < 30; i++) {
    await new Promise(r => setTimeout(r, 250));
    if (!(await managedProcess())) { await fsp.unlink(MANAGED_CORE_PID).catch(() => {}); return true; }
  }
  try { process.kill(proc.pid, 'SIGKILL'); } catch (_) {}
  await fsp.unlink(MANAGED_CORE_PID).catch(() => {});
  return true;
}

function runCommand(file, args = [], timeoutMs = 20000, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(file, args, { stdio: ['ignore', 'pipe', 'pipe'], env: options.env || process.env });
    const stdout = [];
    const stderr = [];
    let killed = false;
    const timer = setTimeout(() => { killed = true; child.kill('SIGKILL'); }, timeoutMs);
    child.stdout.on('data', d => stdout.push(d));
    child.stderr.on('data', d => stderr.push(d));
    child.once('error', err => { clearTimeout(timer); reject(err); });
    child.once('close', code => {
      clearTimeout(timer);
      const out = Buffer.concat(stdout).toString('utf8').trim();
      const err = Buffer.concat(stderr).toString('utf8').trim();
      if (killed) return reject(new Error(`命令超时: ${path.basename(file)} ${args.join(' ')}`));
      resolve({ code: Number(code ?? -1), stdout: out, stderr: err, output: [out, err].filter(Boolean).join('\n') });
    });
  });
}

const MANAGED_GEO_KEYS = ['geodata-mode', 'geo-auto-update', 'geo-update-interval', 'geox-url'];
const MANAGED_GEO_CONFIG = `geodata-mode: true
geo-auto-update: true
geo-update-interval: 24
geox-url:
  geoip: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat"
  geosite: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat"
  mmdb: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb"
  asn: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/GeoLite2-ASN.mmdb"`;

function enforceManagedGeoConfig(raw) {
  // GEO source is a Manager-owned local policy for the bundled managed Core.
  // Keep profile snapshots untouched, but make the effective startup config
  // consistently use the same MetaCubeX data source and daily refresh policy.
  const stripped = stripTopLevelBlocks(raw, MANAGED_GEO_KEYS).replace(/^\s+/, '').replace(/\s+$/, '');
  return `${MANAGED_GEO_CONFIG}\n\n${stripped}\n`;
}

function preserveManagerAccess(candidate, existing) {
  // These are local-machine settings rather than subscription content. Preserve
  // their exact YAML blocks (including advanced TUN options) and strip any
  // conflicting values supplied by the remote profile.
  const keys = ['external-controller', 'external-controller-tls', 'secret', 'mixed-port', 'socks-port', 'port', 'redir-port', 'tproxy-port', 'allow-lan', 'tun'];
  const preserved = keys.map(key => topLevelBlock(existing, key)).filter(x => x.present).map(x => x.text);
  const stripped = stripTopLevelBlocks(candidate, keys).replace(/^\s+/, '');
  return `${preserved.length ? `${preserved.join('\n')}\n\n` : ''}${stripped}`;
}

function safeStamp() { return new Date().toISOString().replace(/[:.]/g, '-'); }

async function copyWithMetadata(src, dst) {
  const st = await fsp.stat(src);
  await fsp.copyFile(src, dst);
  await fsp.chmod(dst, st.mode & 0o7777).catch(() => {});
  await fsp.chown(dst, st.uid, st.gid).catch(() => {});
  return st;
}

async function validateConfig(binary, configFile, configDir, timeoutMs = 60000) {
  const args = ['-t'];
  if (configDir) args.push('-d', configDir);
  args.push('-f', configFile);
  const result = await runCommand(binary, args, timeoutMs, { env: mihomoChildEnv() });
  if (result.code !== 0) throw new Error(`Mihomo 配置校验失败: ${result.output || `exit ${result.code}`}`);
  return result.output || '配置校验通过';
}

async function systemStatus() {
  const proc = await detectPrimaryMihomo({ allowMissing: true });
  let configStat = null;
  if (proc?.configPath) {
    try {
      const st = await fsp.stat(proc.configPath);
      configStat = { exists: st.isFile(), size: st.size, mode: (st.mode & 0o7777).toString(8), uid: st.uid, gid: st.gid };
    } catch (_) { configStat = { exists: false }; }
  }
  let binaryVersion = null;
  const binaryPath = proc?.exe || (fs.existsSync(MANAGED_CORE_BIN) ? MANAGED_CORE_BIN : bootstrapState.binaryPath || null);
  if (binaryPath) {
    try {
      const r = await runCommand(binaryPath, ['-v'], 8000);
      binaryVersion = r.output || null;
    } catch (_) {}
  }
  let managedConfig = null;
  if ((proc?.managed || bootstrapState.mode === 'managed') && fs.existsSync(MANAGED_CONFIG_FILE)) {
    try { managedConfig = await ensureManagedConfig(); } catch (_) {}
  }
  const mode = proc ? (proc.managed ? 'managed' : 'external') : (bootstrapState.mode || null);
  const detectedAccess = mode === 'external' && proc?.configPath ? await detectControllerAccess(proc.configPath) : null;
  return {
    privileged: typeof process.getuid === 'function' ? process.getuid() === 0 : true,
    available: true,
    mode,
    bootstrap: bootstrapState,
    pid: proc?.pid || bootstrapState.pid || null,
    binaryPath,
    configPath: proc?.configPath || (mode === 'managed' ? MANAGED_CONFIG_FILE : bootstrapState.configPath || null),
    configDir: proc?.configDir || (mode === 'managed' ? MANAGED_CONFIG_DIR : null),
    serviceUnit: proc?.unit || null,
    canRestartService: Boolean(proc?.unit || proc?.managed || mode === 'managed'),
    binaryVersion,
    config: configStat,
    managedController: managedConfig ? managedControllerUrl(managedConfig) : (bootstrapState.controller || null),
    managedSecret: managedConfig?.secret || bootstrapState.secret || null,
    managedMixedPort: managedConfig?.mixedPort || bootstrapState.mixedPort || null,
    detectedController: detectedAccess?.controller || null,
    detectedControllerKey: detectedAccess?.controllerKey || null,
    detectedConfigPath: detectedAccess?.configPath || null,
    detectedConfigReadable: detectedAccess?.readable === true,
    detectedControllerError: detectedAccess?.error || null,
    detectedSecretPresent: detectedAccess?.secretPresent === true,
    detectedSecret: detectedAccess?.secretPresent === true ? detectedAccess.secret : undefined,
    managed: mode === 'managed'
  };
}

async function syncStartupConfig(content) {
  const raw = String(content || '');
  if (!raw.trim()) throw Object.assign(new Error('配置内容为空'), { statusCode: 400 });
  if (Buffer.byteLength(raw) > MAX_CONFIG) throw Object.assign(new Error('配置文件过大'), { statusCode: 413 });
  const proc = await detectPrimaryMihomo();
  if (!proc.configPath) throw Object.assign(new Error('无法从 Mihomo 进程定位启动配置'), { statusCode: 409 });
  const target = path.normalize(proc.configPath);
  if (!path.isAbsolute(target) || /^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) throw Object.assign(new Error('启动配置路径不安全'), { statusCode: 409 });
  let old = '';
  let st;
  try { st = await fsp.stat(target); old = await fsp.readFile(target, 'utf8'); }
  catch (err) { throw Object.assign(new Error(`无法读取启动配置 ${target}: ${err.message}`), { statusCode: 500 }); }
  if (!st.isFile()) throw Object.assign(new Error('启动配置不是普通文件'), { statusCode: 409 });

  let effective = preserveManagerAccess(raw, old);
  if (proc.managed) effective = enforceManagedGeoConfig(effective);
  const dir = path.dirname(target);
  const tmp = path.join(dir, `.${path.basename(target)}.clash-for-fnos.${process.pid}.${Date.now()}.tmp`);
  await fsp.writeFile(tmp, effective, { mode: st.mode & 0o7777 });
  await fsp.chown(tmp, st.uid, st.gid).catch(() => {});
  try {
    // For a managed Core, running `mihomo -t` against the live data directory
    // while the old Core is still active can block on real-world configs. The
    // safe validation path is transactional: backup -> write -> restart Core ->
    // Controller health check -> rollback on failure. External Core keeps the
    // explicit pre-validation because we may not control its lifecycle.
    const validation = proc.managed
      ? '托管 Core：通过实际重启与 Controller 健康检查验证配置'
      : await validateConfig(proc.exe, tmp, proc.configDir || dir, 60000);
    await fsp.mkdir(CONFIG_BACKUP_DIR, { recursive: true });
    const backup = path.join(CONFIG_BACKUP_DIR, `${path.basename(target)}.${safeStamp()}.bak`);
    await copyWithMetadata(target, backup);
    await fsp.rename(tmp, target);
    await fsp.chmod(target, st.mode & 0o7777).catch(() => {});
    await fsp.chown(target, st.uid, st.gid).catch(() => {});
    const txId = crypto.randomBytes(12).toString('hex');
    pendingConfigTransactions.set(txId, { target, backup, createdAt: Date.now(), managed: Boolean(proc.managed), unit: proc.unit || null, type: 'profile' });
    return { ok: true, txId, target, backup, effectiveContent: effective, validation };
  } catch (err) {
    await fsp.unlink(tmp).catch(() => {});
    throw err;
  }
}

async function allowedSystemConfigPath(input) {
  const value = String(input || '').trim();
  if (!value || value.length > 4096 || value.includes('\0')) throw Object.assign(new Error('配置路径无效'), { statusCode: 400 });
  if (!path.isAbsolute(value)) throw Object.assign(new Error('配置路径必须是绝对路径'), { statusCode: 400 });
  const target = path.normalize(value);
  if (!/\.ya?ml$/i.test(target)) throw Object.assign(new Error('只允许读取 YAML/YML 配置文件'), { statusCode: 400 });
  if (/^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) throw Object.assign(new Error('不允许读取该系统路径'), { statusCode: 403 });
  const proc = await detectPrimaryMihomo({ allowMissing: true });
  const active = proc?.configPath ? path.normalize(proc.configPath) : null;
  if (target === path.normalize(MANAGED_CONFIG_FILE) || SYSTEM_MIHOMO_CONFIG_PATHS.has(target) || (active && target === active)) return target;
  throw Object.assign(new Error('该路径不是当前 Mihomo 或受支持的系统配置路径'), { statusCode: 403 });
}

async function inspectSystemConfigPath(input) {
  const target = await allowedSystemConfigPath(input);
  try {
    const st = await fsp.stat(target);
    if (!st.isFile()) return { ok: true, path: target, exists: false, readable: false, realPath: null };
    const realPath = await fsp.realpath(target).catch(() => path.resolve(target));
    return {
      ok: true, path: target, realPath, exists: true, readable: true,
      size: st.size, mtime: st.mtimeMs, dev: String(st.dev), ino: String(st.ino)
    };
  } catch (err) {
    if (err?.code === 'ENOENT') return { ok: true, path: target, exists: false, readable: false, realPath: null };
    if (err?.code === 'EACCES' || err?.code === 'EPERM') return { ok: true, path: target, exists: true, readable: false, permissionDenied: true, realPath: null, error: err.message };
    throw Object.assign(new Error(`无法检测配置 ${target}: ${err.message || err}`), { statusCode: 500 });
  }
}

async function readSystemConfigPath(input) {
  const target = await allowedSystemConfigPath(input);
  let st;
  try { st = await fsp.stat(target); }
  catch (err) {
    if (err?.code === 'ENOENT') throw Object.assign(new Error('配置文件不存在'), { statusCode: 404 });
    throw Object.assign(new Error(`无法读取配置 ${target}: ${err.message || err}`), { statusCode: 500 });
  }
  if (!st.isFile()) throw Object.assign(new Error('目标不是普通文件'), { statusCode: 400 });
  if (st.size <= 0) throw Object.assign(new Error('配置文件为空'), { statusCode: 400 });
  if (st.size > MAX_CONFIG) throw Object.assign(new Error('配置文件过大'), { statusCode: 413 });
  const content = await fsp.readFile(target, 'utf8');
  if (!content.trim()) throw Object.assign(new Error('配置文件为空'), { statusCode: 400 });
  if (content.includes('\0')) throw Object.assign(new Error('配置文件不是文本 YAML'), { statusCode: 400 });
  const realPath = await fsp.realpath(target).catch(() => path.resolve(target));
  return { ok: true, path: target, realPath, size: st.size, mtime: st.mtimeMs, content };
}

async function startupProxyGroupOrder() {
  const proc = await detectPrimaryMihomo();
  if (!proc?.configPath) return { ok: true, configPath: null, order: [] };
  const target = path.normalize(proc.configPath);
  if (!path.isAbsolute(target) || /^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) return { ok: true, configPath: null, order: [] };
  try {
    const raw = await fsp.readFile(target, 'utf8');
    return { ok: true, configPath: target, order: parseProxyGroupOrder(raw) };
  } catch (_) {
    return { ok: true, configPath: target, order: [] };
  }
}

async function activeStartupConfigRaw() {
  const proc = await detectPrimaryMihomo();
  if (!proc?.configPath) throw Object.assign(new Error('无法从 Mihomo 进程定位启动配置'), { statusCode: 409 });
  const target = path.normalize(proc.configPath);
  if (!path.isAbsolute(target) || /^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) {
    throw Object.assign(new Error('启动配置路径不安全'), { statusCode: 409 });
  }
  let st;
  try { st = await fsp.stat(target); }
  catch (err) { throw Object.assign(new Error(`无法读取启动配置 ${target}: ${err.message}`), { statusCode: 500 }); }
  if (!st.isFile()) throw Object.assign(new Error('启动配置不是普通文件'), { statusCode: 409 });
  if (st.size > MAX_CONFIG) throw Object.assign(new Error('启动配置文件过大'), { statusCode: 413 });
  try {
    const content = await fsp.readFile(target, 'utf8');
    return { ok: true, configPath: target, pid: proc.pid || null, mode: proc.managed ? 'managed' : 'external', content };
  } catch (err) {
    throw Object.assign(new Error(`无法读取启动配置 ${target}: ${err.message}`), { statusCode: 500 });
  }
}

async function networkStatus() {
  const proc = await detectPrimaryMihomo();
  if (!proc.configPath) throw Object.assign(new Error('无法从 Mihomo 进程定位启动配置'), { statusCode: 409 });
  const target = path.normalize(proc.configPath);
  let raw;
  try { raw = await fsp.readFile(target, 'utf8'); }
  catch (err) { throw Object.assign(new Error(`无法读取启动配置 ${target}: ${err.message}`), { statusCode: 500 }); }
  const settings = parseNetworkSettings(raw);
  const capability = await tunCapability(proc);
  return {
    ok: true,
    mode: proc.managed ? 'managed' : 'external',
    configPath: target,
    settings,
    tunCapability: capability,
    controllerClientUrl: controllerUrlFromListenAddress(formatListenAddress(settings.controller.host, settings.controller.port), settings.controller.protocol)
  };
}

async function updateNetworkConfig(input) {
  const proc = await detectPrimaryMihomo();
  if (!proc.configPath) throw Object.assign(new Error('无法从 Mihomo 进程定位启动配置'), { statusCode: 409 });
  const target = path.normalize(proc.configPath);
  if (!path.isAbsolute(target) || /^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) throw Object.assign(new Error('启动配置路径不安全'), { statusCode: 409 });
  let old = '';
  let st;
  try { st = await fsp.stat(target); old = await fsp.readFile(target, 'utf8'); }
  catch (err) { throw Object.assign(new Error(`无法读取启动配置 ${target}: ${err.message}`), { statusCode: 500 }); }
  if (!st.isFile()) throw Object.assign(new Error('启动配置不是普通文件'), { statusCode: 409 });

  const current = parseNetworkSettings(old);
  const settings = normalizeNetworkInput(input || {}, current);
  const capability = await tunCapability(proc);
  if (settings.tun.enabled && !current.tun.enabled && !capability.supported) {
    const reason = !capability.tunDevice ? '/dev/net/tun 不存在' : '当前 Mihomo 进程没有 root/CAP_NET_ADMIN 权限';
    throw Object.assign(new Error(`无法开启 TUN：${reason}`), { statusCode: 409 });
  }

  const effective = applyNetworkOverrides(old, settings);
  const dir = path.dirname(target);
  const tmp = path.join(dir, `.${path.basename(target)}.clash-for-fnos-network.${process.pid}.${Date.now()}.tmp`);
  await fsp.writeFile(tmp, effective, { mode: st.mode & 0o7777 });
  await fsp.chown(tmp, st.uid, st.gid).catch(() => {});
  try {
    const validation = proc.managed
      ? '托管 Core：通过实际重启与 Controller 健康检查验证配置'
      : await validateConfig(proc.exe, tmp, proc.configDir || dir, 60000);
    await fsp.mkdir(CONFIG_BACKUP_DIR, { recursive: true });
    const backup = path.join(CONFIG_BACKUP_DIR, `${path.basename(target)}.${safeStamp()}.network.bak`);
    await copyWithMetadata(target, backup);
    await fsp.rename(tmp, target);
    await fsp.chmod(target, st.mode & 0o7777).catch(() => {});
    await fsp.chown(target, st.uid, st.gid).catch(() => {});
    const txId = crypto.randomBytes(12).toString('hex');
    pendingConfigTransactions.set(txId, { target, backup, createdAt: Date.now(), type: 'network', managed: Boolean(proc.managed), unit: proc.unit || null });
    const listen = formatListenAddress(settings.controller.host, settings.controller.port);
    return {
      ok: true,
      txId,
      target,
      backup,
      effectiveContent: effective,
      settings,
      tunCapability: capability,
      controller: {
        key: settings.controller.key,
        listen,
        clientUrl: controllerUrlFromListenAddress(listen, settings.controller.protocol)
      },
      validation
    };
  } catch (err) {
    await fsp.unlink(tmp).catch(() => {});
    throw err;
  }
}

async function activateStartupConfig(txId) {
  const key = String(txId || '');
  const tx = pendingConfigTransactions.get(key);
  if (!tx) throw Object.assign(new Error('配置应用事务不存在或已失效'), { statusCode: 409 });

  const current = await detectPrimaryMihomo({ allowMissing: true });
  const managed = Boolean(tx.managed || current?.managed);
  const unit = tx.unit || current?.unit || null;

  if (managed) {
    try {
      await stopManagedCore();
      const started = await startManagedCore({ skipValidation: true });
      return { ok: true, method: 'managed-restart', restarted: true, pid: started?.pid || null };
    } catch (err) {
      await copyWithMetadata(tx.backup, tx.target).catch(() => {});
      let rollbackError = null;
      try { await stopManagedCore(); await startManagedCore({ skipValidation: true }); }
      catch (rollbackErr) { rollbackError = rollbackErr?.message || String(rollbackErr); }
      pendingConfigTransactions.delete(key);
      const suffix = rollbackError ? `；旧配置恢复后启动也失败：${rollbackError}` : '；已恢复旧配置并重新启动';
      throw new Error(`新配置启动失败：${err?.message || String(err)}${suffix}`);
    }
  }

  if (unit) {
    const result = await runCommand('systemctl', ['restart', unit], 30000).catch(err => ({ code: -1, output: err.message || String(err) }));
    if (result.code === 0) return { ok: true, method: 'service-restart', restarted: true, serviceUnit: unit };

    await copyWithMetadata(tx.backup, tx.target).catch(() => {});
    const rollback = await runCommand('systemctl', ['restart', unit], 30000).catch(err => ({ code: -1, output: err.message || String(err) }));
    pendingConfigTransactions.delete(key);
    const suffix = rollback.code === 0 ? '；已恢复旧配置并重启服务' : `；旧配置已恢复，但服务重启失败：${rollback.output || `exit ${rollback.code}`}`;
    throw new Error(`重启 ${unit} 应用新配置失败：${result.output || `exit ${result.code}`}${suffix}`);
  }

  return { ok: true, method: 'hot-reload', restarted: false };
}

async function rollbackStartupConfig(txId) {
  const key = String(txId || '');
  const tx = pendingConfigTransactions.get(key);
  if (!tx) throw Object.assign(new Error('配置回滚事务不存在或已失效'), { statusCode: 409 });
  await copyWithMetadata(tx.backup, tx.target);
  let restarted = false;
  let restartError = null;
  if (tx.managed) {
    try { await stopManagedCore(); await startManagedCore({ skipValidation: true }); restarted = true; }
    catch (err) { restartError = err?.message || String(err); }
  } else if (tx.unit) {
    const r = await runCommand('systemctl', ['restart', tx.unit], 30000).catch(err => ({ code: -1, output: err.message || String(err) }));
    restarted = r.code === 0;
    if (!restarted) restartError = r.output || `exit ${r.code}`;
  }
  pendingConfigTransactions.delete(key);
  return { ok: true, target: tx.target, backup: tx.backup, restarted, restartError };
}

async function commitStartupConfig(txId) {
  const tx = pendingConfigTransactions.get(String(txId || ''));
  if (!tx) return { ok: true };
  pendingConfigTransactions.delete(txId);
  return { ok: true, target: tx.target, backup: tx.backup };
}

function assertStagePath(input) {
  const stage = path.resolve(String(input || ''));
  const root = path.resolve(CORE_STAGE_DIR) + path.sep;
  if (!stage.startsWith(root)) throw Object.assign(new Error('内核暂存路径不合法'), { statusCode: 400 });
  return stage;
}

async function sha256File(file) {
  return new Promise((resolve, reject) => {
    const h = crypto.createHash('sha256');
    const s = fs.createReadStream(file);
    s.on('data', d => h.update(d));
    s.on('end', () => resolve(h.digest('hex')));
    s.on('error', reject);
  });
}


function detectMihomoDownloadTarget() {
  const platform = process.platform;
  const machine = String(typeof os.machine === 'function' ? os.machine() : process.arch).trim().toLowerCase();
  return resolveMihomoDownloadTarget(platform, machine, process.arch);
}


async function readBundledCore() {
  const target = detectMihomoDownloadTarget();
  let meta;
  try { meta = JSON.parse(await fsp.readFile(BUNDLED_CORE_META_FILE, 'utf8')); }
  catch (err) { throw new Error(`安装包内置 Core 元数据不可用: ${err.message}`); }

  const tag = String(meta?.tag || '');
  const arch = String(meta?.arch || '');
  const sha256 = String(meta?.sha256 || '').toLowerCase();
  const size = Number(meta?.size || 0);
  if (!/^v\d+\.\d+\.\d+$/.test(tag)) throw new Error(`安装包内置 Core 版本号无效: ${tag || 'unknown'}`);
  if (arch !== target.arch) throw new Error(`当前 NAS 架构为 ${target.machine} (${target.arch})，但此安装包内置的是 ${arch || 'unknown'} Core，请安装正确架构的 FPK`);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error('安装包内置 Core SHA-256 元数据无效');
  if (!Number.isInteger(size) || size <= 0 || size > MAX_CORE_ASSET) throw new Error(`安装包内置 Core 大小元数据无效: ${size}`);

  // File name is derived from the detected runtime target, never trusted from metadata.
  const assetName = `mihomo-linux-${target.arch}-${tag}.gz`;
  const bundledPath = path.resolve(BUNDLED_CORE_DIR, assetName);
  if (!bundledPath.startsWith(BUNDLED_CORE_DIR + path.sep)) throw new Error('安装包内置 Core 路径非法');
  const st = await fsp.stat(bundledPath).catch(() => null);
  if (!st?.isFile()) throw new Error(`安装包缺少内置 Core: ${assetName}`);
  if (st.size !== size) throw new Error(`安装包内置 Core 大小校验失败: ${st.size} != ${size}`);
  const digest = await sha256File(bundledPath);
  if (digest.toLowerCase() !== sha256) throw new Error(`安装包内置 Core SHA-256 校验失败: ${digest}`);

  return { tag, target, assetName, sha256, size, path: bundledPath, bundled: true };
}

function parseOfficialRelease(raw, expectedVersion = null) {
  let data;
  try { data = typeof raw === 'string' ? JSON.parse(raw) : raw; }
  catch (_) { throw new Error('GitHub Release JSON 无效'); }
  const tag = String(data?.tag_name || '');
  if (!/^v\d+\.\d+\.\d+$/.test(tag)) throw new Error(`官方 latest 版本号无效: ${tag || 'unknown'}`);
  if (expectedVersion && tag !== String(expectedVersion)) throw new Error(`待安装版本 ${expectedVersion} 不是当前官方 latest ${tag}`);
  const target = detectMihomoDownloadTarget();
  const names = mihomoReleaseAssetNames(target, tag);
  const asset = Array.isArray(data?.assets) ? names.map(name => data.assets.find(x => x.name === name)).find(Boolean) : null;
  const name = String(asset?.name || names[0]);
  const digest = String(asset?.digest || '');
  if (!asset || !asset.browser_download_url || !digest.startsWith('sha256:')) throw new Error(`官方 Release 未提供适用于 ${target.machine} 的 ${name} 及 SHA-256 digest，拒绝安装`);
  const sha256 = digest.slice(7).toLowerCase();
  const size = Number(asset.size || 0);
  if (!/^[a-f0-9]{64}$/.test(sha256)) throw new Error(`官方 Release 的 ${name} SHA-256 digest 无效，拒绝安装`);
  if (!Number.isInteger(size) || size <= 0 || size > MAX_CORE_ASSET) throw new Error(`官方 Release 的 ${name} 文件大小无效: ${size}`);
  return { tag, target, assetName: name, sha256, size, url: asset.browser_download_url, bundled: false };
}

function fetchGithubReleaseDirect() {
  return new Promise((resolve, reject) => {
    const req = https.request({
      protocol: 'https:', hostname: 'api.github.com', port: 443,
      path: '/repos/MetaCubeX/mihomo/releases/latest', method: 'GET',
      headers: { 'User-Agent': 'Clash-for-fnos-Privileged-Helper', 'Accept': 'application/vnd.github+json' },
      timeout: 20000
    }, res => {
      const chunks = []; let total = 0;
      res.on('data', c => {
        total += c.length;
        if (total > 4 * 1024 * 1024) res.destroy(new Error('GitHub Release 响应过大'));
        else chunks.push(c);
      });
      res.on('end', () => {
        if ((res.statusCode || 0) !== 200) return reject(new Error(`GitHub HTTP ${res.statusCode || 0}`));
        resolve(Buffer.concat(chunks).toString('utf8'));
      });
    });
    req.on('timeout', () => req.destroy(new Error('访问 GitHub Release 超时')));
    req.on('error', reject);
    req.end();
  });
}

async function readLocalProxyPort(proc) {
  if (!proc?.configPath) return null;
  try {
    const raw = await fsp.readFile(proc.configPath, 'utf8');
    for (const key of ['mixed-port', 'port']) {
      const m = raw.match(new RegExp(`^${key}\\s*:\\s*([0-9]{1,5})\\s*(?:#.*)?$`, 'm'));
      if (m) {
        const port = Number(m[1]);
        if (port > 0 && port <= 65535) return port;
      }
    }
  } catch (_) {}
  return null;
}

async function fetchOfficialLatestRelease(expectedVersion, proc) {
  let directError = null;
  try {
    const raw = await fetchGithubReleaseDirect();
    return parseOfficialRelease(raw, expectedVersion);
  } catch (err) { directError = err; }

  // GitHub may be unreachable directly on some fnOS networks. Fall back only to the
  // locally detected Mihomo HTTP/mixed port and a fixed official GitHub URL.
  const proxyPort = await readLocalProxyPort(proc);
  if (proxyPort) {
    let proxyError;
    try {
      const curl = ['/usr/bin/curl', '/bin/curl', '/usr/local/bin/curl'].find(x => fs.existsSync(x)) || 'curl';
      const r = await runCommand(curl, [
        '-fsSL', '--connect-timeout', '10', '--max-time', '30',
        '-x', `http://127.0.0.1:${proxyPort}`,
        '-A', 'Clash-for-fnos-Privileged-Helper',
        '-H', 'Accept: application/vnd.github+json',
        'https://api.github.com/repos/MetaCubeX/mihomo/releases/latest'
      ], 35000);
      if (r.code === 0 && r.stdout) return parseOfficialRelease(r.stdout, expectedVersion);
      proxyError = new Error(r.output || `curl exit ${r.code}`);
    } catch (err) {
      proxyError = err;
    }
    throw new Error(`无法独立校验官方 Release：直连失败（${directError?.message || directError}）；Mihomo 代理失败（${proxyError?.message || proxyError}）`);
  }
  throw new Error(`无法独立校验官方 Release：${directError?.message || directError}；且未从启动配置检测到 mixed-port/port`);
}



function assertOfficialCoreDownloadUrl(input) {
  let url;
  try { url = new URL(String(input || '')); }
  catch (_) { throw new Error('官方 Mihomo Core 下载地址无效'); }
  if (url.protocol !== 'https:' || !ALLOWED_CORE_DOWNLOAD_HOSTS.has(url.hostname)) throw new Error(`拒绝访问非官方 Mihomo Core 下载地址: ${url.hostname || input}`);
  return url;
}

async function downloadOfficialCore(official) {
  const initialUrl = assertOfficialCoreDownloadUrl(official.url);
  await fsp.mkdir(CORE_STAGE_DIR, { recursive: true, mode: 0o750 });
  const stage = path.join(CORE_STAGE_DIR, `bootstrap-${process.pid}-${Date.now()}.gz`);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 120000);
  try {
    const response = await fetch(initialUrl, {
      redirect: 'follow',
      signal: controller.signal,
      headers: { 'User-Agent': 'Clash-for-fnos-Privileged-Helper', 'Accept': 'application/octet-stream' }
    });
    assertOfficialCoreDownloadUrl(response.url);
    if (!response.ok) throw new Error(`下载官方 Mihomo Core 失败: HTTP ${response.status}`);
    const declaredSize = Number(response.headers.get('content-length') || 0);
    if (declaredSize > MAX_CORE_ASSET) throw new Error(`官方 Mihomo Core 超过大小限制: ${declaredSize}`);
    if (!response.body) throw new Error('官方 Mihomo Core 下载响应为空');

    const chunks = [];
    const hash = crypto.createHash('sha256');
    let total = 0;
    for await (const chunk of response.body) {
      const data = Buffer.from(chunk);
      total += data.length;
      if (total > MAX_CORE_ASSET) throw new Error(`官方 Mihomo Core 超过大小限制: ${total}`);
      hash.update(data);
      chunks.push(data);
    }
    if (total !== official.size) throw new Error(`官方 Mihomo Core 大小校验失败: ${total} != ${official.size}`);
    const digest = hash.digest('hex');
    if (digest !== official.sha256) throw new Error(`官方 Mihomo Core SHA-256 校验失败: ${digest}`);
    const compressed = Buffer.concat(chunks);
    if (compressed.length < 2 || compressed[0] !== 0x1f || compressed[1] !== 0x8b) throw new Error('官方 Mihomo Core 不是有效的 gzip 文件');
    await fsp.writeFile(stage, compressed, { mode: 0o600, flag: 'wx' });
    return stage;
  } catch (err) {
    await fsp.unlink(stage).catch(() => {});
    if (err?.name === 'AbortError') throw new Error('下载官方 Mihomo Core 超时');
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

async function installManagedCoreFromStage(stage, official) {
  const compressed = await fsp.readFile(stage);
  let binary;
  try { binary = zlib.gunzipSync(compressed, { maxOutputLength: 128 * 1024 * 1024 }); }
  catch (err) { throw new Error(`Mihomo Core 解压失败: ${err.message}`); }
  await fsp.mkdir(MANAGED_CORE_DIR, { recursive: true, mode: 0o750 });
  const candidate = path.join(MANAGED_CORE_DIR, `.mihomo.${process.pid}.${Date.now()}.new`);
  await fsp.writeFile(candidate, binary, { mode: 0o755 });
  await fsp.chmod(candidate, 0o755);
  const vr = await runCommand(candidate, ['-v'], 10000);
  if (vr.code !== 0 || !vr.output.includes(official.tag.replace(/^v/, ''))) { await fsp.unlink(candidate).catch(() => {}); throw new Error(`Mihomo Core 自检失败: ${vr.output || vr.code}`); }
  const cfg = await ensureManagedConfig();
  await validateConfig(candidate, MANAGED_CONFIG_FILE, MANAGED_CONFIG_DIR);
  if (fs.existsSync(MANAGED_CORE_BIN)) {
    await fsp.mkdir(CORE_BACKUP_DIR, { recursive: true });
    const backup = path.join(CORE_BACKUP_DIR, `managed-mihomo-${safeStamp()}`);
    await fsp.copyFile(MANAGED_CORE_BIN, backup);
    await fsp.chmod(backup, 0o755).catch(() => {});
  }
  await fsp.rename(candidate, MANAGED_CORE_BIN);
  await fsp.chmod(MANAGED_CORE_BIN, 0o755);
  await fsp.writeFile(BOOTSTRAP_META_FILE, JSON.stringify({ mode: 'managed', installedVersion: official.tag, installedAt: Date.now(), asset: official.assetName, target: official.target, sha256: official.sha256, source: official.bundled ? 'bundled' : 'online' }, null, 2), { mode: 0o600 });
  return cfg;
}

async function ensureCoreBootstrap(force = false) {
  if (bootstrapPromise && !force) return bootstrapPromise;
  bootstrapPromise = (async () => {
    const onlineDelivery = fs.existsSync(ONLINE_CORE_MARKER_FILE);
    setBootstrap('checking', { mode: null, delivery: onlineDelivery ? 'online' : 'bundled', message: '正在检测本机 Mihomo', error: null, progress: 5 });
    try {
      const processes = await listMihomoProcesses();
      const managedMarker = fs.existsSync(BOOTSTRAP_META_FILE);
      const managed = processes.find(x => !x.containerized && x.managed);
      const external = processes.find(x => !x.containerized && !x.managed);
      if (external && !(managedMarker && managed)) {
        setBootstrap('ready', { mode: 'external', message: '已连接外部 Mihomo Core', progress: 100, pid: external.pid, binaryPath: external.exe, configPath: external.configPath });
        return bootstrapState;
      }
      if (managed) {
        const cfg = await ensureManagedConfig();
        setBootstrap('ready', { mode: 'managed', message: 'Manager 托管 Mihomo Core 已运行', progress: 100, pid: managed.pid, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE, controller: managedControllerUrl(cfg), secret: cfg.secret, mixedPort: cfg.mixedPort });
        return bootstrapState;
      }
      const installedExternal = await findExternalInstallation();
      if (installedExternal) {
        setBootstrap('external-stopped', { mode: 'external', message: '检测到外部 Mihomo 已安装但未运行，未自动安装第二份 Core', progress: 100, binaryPath: installedExternal.binaryPath, configPath: installedExternal.configPath });
        return bootstrapState;
      }
      if (fs.existsSync(MANAGED_CORE_BIN)) {
        setBootstrap('starting', { mode: 'managed', message: '正在启动 Manager 托管 Mihomo Core', progress: 80, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE });
        const proc = await startManagedCore({ skipValidation: true });
        const cfg = await ensureManagedConfig();
        setBootstrap('ready', { mode: 'managed', message: 'Manager 托管 Mihomo Core 已启动', progress: 100, pid: proc.pid, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE, controller: managedControllerUrl(cfg), secret: cfg.secret, mixedPort: cfg.mixedPort });
        return bootstrapState;
      }

      let core;
      let stage;
      if (onlineDelivery) {
        setBootstrap('downloading', { mode: 'managed', message: '未检测到 Mihomo，正在查询 MetaCubeX 官方 Release', progress: 20 });
        core = await fetchOfficialLatestRelease(null, null);
        setBootstrap('downloading', { mode: 'managed', message: `检测到 ${core.target.machine}，正在下载 ${core.assetName}`, progress: 35, targetVersion: core.tag });
        stage = await downloadOfficialCore(core);
        setBootstrap('installing', { mode: 'managed', message: `SHA-256 校验通过，正在安装 ${core.assetName}`, progress: 65, targetVersion: core.tag });
      } else {
        setBootstrap('installing', { mode: 'managed', message: '未检测到 Mihomo，正在校验安装包内置 Core', progress: 25 });
        core = await readBundledCore();
        stage = core.path;
        setBootstrap('installing', { mode: 'managed', message: `检测到 ${core.target.machine}，正在安装内置 ${core.assetName}`, progress: 55, targetVersion: core.tag });
      }
      let cfg;
      try { cfg = await installManagedCoreFromStage(stage, core); }
      finally { if (onlineDelivery) await fsp.unlink(stage).catch(() => {}); }
      setBootstrap('starting', { mode: 'managed', message: '正在启动 Mihomo Core', progress: 85, targetVersion: core.tag, controller: managedControllerUrl(cfg), secret: cfg.secret, mixedPort: cfg.mixedPort });
      const proc = await startManagedCore();
      setBootstrap('ready', { mode: 'managed', message: `Mihomo ${core.tag} (${core.target.arch}) 已${onlineDelivery ? '从官方 Release 下载' : '从安装包启用'}并启动`, progress: 100, targetVersion: core.tag, pid: proc.pid, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE, controller: managedControllerUrl(cfg), secret: cfg.secret, mixedPort: cfg.mixedPort });
      return bootstrapState;
    } catch (err) {
      setBootstrap('error', { message: 'Mihomo Core 启动准备失败', error: err.message || String(err), progress: 0 });
      throw err;
    } finally {
      bootstrapPromise = null;
    }
  })();
  return bootstrapPromise;
}

async function installCore(stagePath, expectedVersion, restartRequested) {
  const stage = assertStagePath(stagePath);
  const proc = await detectPrimaryMihomo();
  const target = path.normalize(proc.exe);
  if (!path.isAbsolute(target) || /^(\/proc|\/sys|\/dev)(\/|$)/.test(target)) throw Object.assign(new Error('Mihomo 二进制路径不安全'), { statusCode: 409 });
  const stageStat = await fsp.stat(stage).catch(() => null);
  if (!stageStat?.isFile()) throw Object.assign(new Error('暂存的官方内核压缩包不存在'), { statusCode: 404 });
  const official = await fetchOfficialLatestRelease(expectedVersion, proc);
  if (official.size > 0 && stageStat.size !== official.size) throw new Error(`官方内核压缩包大小不匹配: ${stageStat.size} != ${official.size}`);
  const digest = await sha256File(stage);
  if (digest.toLowerCase() !== official.sha256) throw Object.assign(new Error(`官方内核 SHA-256 校验失败: ${digest}`), { statusCode: 409 });
  const compressed = await fsp.readFile(stage);
  let binary;
  try { binary = zlib.gunzipSync(compressed, { maxOutputLength: 128 * 1024 * 1024 }); }
  catch (err) { throw new Error(`官方内核解压失败: ${err.message}`); }
  const candidate = `${stage}.verified.${process.pid}`;
  await fsp.writeFile(candidate, binary, { mode: 0o755 });
  await fsp.chmod(candidate, 0o755);
  const versionCheck = await runCommand(candidate, ['-v'], 10000);
  if (versionCheck.code !== 0) { await fsp.unlink(candidate).catch(() => {}); throw new Error(`新内核无法运行: ${versionCheck.output || versionCheck.code}`); }
  if (expectedVersion && !versionCheck.output.includes(String(expectedVersion).replace(/^v/, ''))) {
    await fsp.unlink(candidate).catch(() => {});
    throw new Error(`新内核版本不匹配: ${versionCheck.output}`);
  }
  if (proc.configPath) {
    try { await validateConfig(candidate, proc.configPath, proc.configDir || path.dirname(proc.configPath)); }
    catch (err) { await fsp.unlink(candidate).catch(() => {}); throw err; }
  }

  const oldStat = await fsp.stat(target);
  await fsp.mkdir(CORE_BACKUP_DIR, { recursive: true });
  const oldVersionCheck = await runCommand(target, ['-v'], 8000).catch(() => ({ output: 'unknown' }));
  const oldVersion = String(oldVersionCheck.output || 'unknown').match(/v?\d+\.\d+\.\d+/)?.[0] || 'unknown';
  const backup = path.join(CORE_BACKUP_DIR, `mihomo-${oldVersion}-${safeStamp()}`);
  await copyWithMetadata(target, backup);
  await fsp.chmod(backup, 0o755).catch(() => {});

  const tempTarget = path.join(path.dirname(target), `.${path.basename(target)}.clash-for-fnos.${process.pid}.${Date.now()}.new`);
  await fsp.copyFile(candidate, tempTarget);
  await fsp.chmod(tempTarget, oldStat.mode & 0o7777 || 0o755);
  await fsp.chown(tempTarget, oldStat.uid, oldStat.gid).catch(() => {});
  await fsp.rename(tempTarget, target);

  const txId = crypto.randomBytes(12).toString('hex');
  const tx = { target, backup, unit: proc.unit, managed: Boolean(proc.managed), createdAt: Date.now(), oldVersion, expectedVersion };
  pendingCoreTransactions.set(txId, tx);
  let restarted = false;
  let restartError = null;
  if (restartRequested && proc.managed) {
    try { await stopManagedCore(); await startManagedCore(); restarted = true; }
    catch (err) { restartError = err.message || String(err); }
  } else if (restartRequested && proc.unit) {
    const r = await runCommand('systemctl', ['restart', proc.unit], 30000).catch(err => ({ code: -1, output: err.message }));
    if (r.code === 0) restarted = true;
    else restartError = r.output || `exit ${r.code}`;
  }
  await fsp.unlink(candidate).catch(() => {});
  return { ok: true, txId, target, backup, oldVersion, newVersion: expectedVersion, versionOutput: versionCheck.output, officialSha256: official.sha256, serviceUnit: proc.unit, restarted, restartError, restartRequired: !restarted };
}

async function rollbackCore(txId, restart = false) {
  const tx = pendingCoreTransactions.get(String(txId || ''));
  if (!tx) throw Object.assign(new Error('内核回滚事务不存在或已失效'), { statusCode: 409 });
  await copyWithMetadata(tx.backup, tx.target);
  await fsp.chmod(tx.target, 0o755).catch(() => {});
  let restarted = false;
  if (restart && tx.managed) {
    try { await stopManagedCore(); await startManagedCore(); restarted = true; } catch (_) {}
  } else if (restart && tx.unit) {
    const r = await runCommand('systemctl', ['restart', tx.unit], 30000).catch(() => ({ code: -1 }));
    restarted = r.code === 0;
  }
  if (tx.managed) {
    await fsp.writeFile(BOOTSTRAP_META_FILE, JSON.stringify({ mode: 'managed', installedVersion: tx.oldVersion, installedAt: Date.now(), rollback: true }, null, 2), { mode: 0o600 }).catch(() => {});
    const proc = await managedProcess().catch(() => null);
    const cfg = await ensureManagedConfig().catch(() => null);
    if (proc) setBootstrap('ready', { mode: 'managed', message: `Mihomo ${tx.oldVersion} 已回滚并运行`, progress: 100, pid: proc.pid, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE, controller: cfg ? managedControllerUrl(cfg) : undefined, secret: cfg?.secret, mixedPort: cfg?.mixedPort, targetVersion: tx.oldVersion });
  }
  pendingCoreTransactions.delete(txId);
  return { ok: true, target: tx.target, backup: tx.backup, restarted };
}

async function commitCore(txId) {
  const tx = pendingCoreTransactions.get(String(txId || ''));
  if (!tx) return { ok: true };
  if (tx.managed) {
    await fsp.writeFile(BOOTSTRAP_META_FILE, JSON.stringify({ mode: 'managed', installedVersion: tx.expectedVersion, installedAt: Date.now() }, null, 2), { mode: 0o600 }).catch(() => {});
    const proc = await managedProcess().catch(() => null);
    const cfg = await ensureManagedConfig().catch(() => null);
    if (proc) setBootstrap('ready', { mode: 'managed', message: `Mihomo ${tx.expectedVersion} 已更新并运行`, progress: 100, pid: proc.pid, binaryPath: MANAGED_CORE_BIN, configPath: MANAGED_CONFIG_FILE, controller: cfg ? managedControllerUrl(cfg) : undefined, secret: cfg?.secret, mixedPort: cfg?.mixedPort, targetVersion: tx.expectedVersion });
  }
  pendingCoreTransactions.delete(txId);
  return { ok: true, target: tx.target, backup: tx.backup };
}

async function route(req, res) {
  try {
    const handled = await handlePrivilegedApi(req, {
      readBody: bodyJson,
      sendJson: (status, payload) => json(res, status, payload),
      maxConfigBytes: MAX_CONFIG,
      handlers: {
        activeStartupConfigRaw,
        activateStartupConfig,
        appIconStatus,
        applyAppIcon,
        bootstrapStatus: () => bootstrapState,
        commitCore,
        commitStartupConfig,
        ensureCoreBootstrap,
        inspectSystemConfigPath,
        installCore,
        networkStatus,
        readSystemConfigPath,
        rollbackCore,
        rollbackStartupConfig,
        startupProxyGroupOrder,
        syncStartupConfig,
        syncSystemProxyEnvironment,
        systemProxyEnvironment,
        systemStatus,
        updateNetworkConfig,
        updateSystemProxyEnvironment
      }
    });
    if (handled === false) return json(res, 404, { error: 'Not found' });
  } catch (err) {
    return json(res, err.statusCode || 500, { error: err.message || String(err) });
  }
}

(async () => {
  if (typeof process.getuid === 'function' && process.getuid() !== 0) {
    console.error('privileged-helper must run as root');
    process.exit(1);
  }
  await fsp.mkdir(ETC_DIR, { recursive: true });
  await fsp.mkdir(VAR_DIR, { recursive: true });
  await fsp.mkdir(CORE_STAGE_DIR, { recursive: true });
  await fsp.mkdir(MANAGED_CORE_DIR, { recursive: true });
  await fsp.chmod(MANAGED_CORE_DIR, 0o750).catch(() => {});
  await fsp.mkdir(MANAGED_CONFIG_DIR, { recursive: true });
  await fsp.chmod(MANAGED_CONFIG_DIR, 0o750).catch(() => {});
  await fsp.mkdir(SYSTEM_BACKUP_DIR, { recursive: true });
  await syncAppIcon().catch(err => console.error(`软件图标同步失败：${err.message || String(err)}`));
  await fsp.unlink(SOCKET_PATH).catch(() => {});
  const server = http.createServer((req, res) => route(req, res));
  server.listen(SOCKET_PATH, () => {
    try { fs.chmodSync(SOCKET_PATH, 0o660); } catch (_) {}
    console.log(`Clash for fnos privileged helper listening on ${SOCKET_PATH}`);
    setTimeout(() => ensureCoreBootstrap(false).catch(err => console.error(`Mihomo bootstrap failed: ${err.message}`)), 300);
  });
  const shutdown = async () => {
    try { if (bootstrapState.mode === 'managed' || await managedProcess()) await stopManagedCore(); } catch (_) {}
    server.close(() => { try { fs.unlinkSync(SOCKET_PATH); } catch (_) {} process.exit(0); });
    setTimeout(() => process.exit(0), 4000).unref();
  };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
})().catch(err => { console.error(err); process.exit(1); });
