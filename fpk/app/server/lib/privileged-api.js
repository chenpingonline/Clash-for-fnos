'use strict';

const { requireObject, requireString, requireTransactionId } = require('./contracts');

/** @typedef {(req: import('node:http').IncomingMessage) => Promise<unknown>} ReadBody */
/** @typedef {(status: number, payload: unknown) => void} SendJson */
/** @typedef {Record<string, (...args: any[]) => any>} PrivilegedHandlers */

/**
 * Keeps the root process API surface explicit and validates every mutation at
 * the socket boundary before a system-level handler is called.
 * @param {import('node:http').IncomingMessage} req
 * @param {{readBody: ReadBody, sendJson: SendJson, handlers: PrivilegedHandlers, maxConfigBytes: number}} context
 */
async function handlePrivilegedApi(req, context) {
  const { readBody, sendJson, handlers, maxConfigBytes } = context;
  const method = req.method || 'GET';
  const url = req.url || '/';
  /** @param {unknown | Promise<unknown>} value */
  const ok = async value => sendJson(200, await value);
  const bodyObject = async () => requireObject(await readBody(req));

  if (method === 'GET' && url === '/status') return ok(handlers.systemStatus());
  if (method === 'GET' && url === '/bootstrap/status') return ok(handlers.bootstrapStatus());
  if (method === 'POST' && url === '/bootstrap/retry') return ok(handlers.ensureCoreBootstrap(true));
  if (method === 'GET' && url === '/config/proxy-group-order') return ok(handlers.startupProxyGroupOrder());
  if (method === 'GET' && url === '/config/active-raw') return ok(handlers.activeStartupConfigRaw());
  if (method === 'GET' && url === '/network/status') return ok(handlers.networkStatus());
  if (method === 'GET' && url === '/app/icon/status') return ok(handlers.appIconStatus());
  if (method === 'GET' && url === '/system/proxy-environment') return ok(handlers.systemProxyEnvironment());
  if (method === 'POST' && url === '/system/proxy-environment/sync') return ok(handlers.syncSystemProxyEnvironment());

  if (method !== 'POST') return false;
  const mutationPaths = new Set([
    '/config/sync', '/config/activate', '/config/rollback', '/config/commit',
    '/config/inspect-path', '/config/read-path', '/app/icon/update',
    '/system/proxy-environment/update', '/network/update', '/core/install',
    '/core/rollback', '/core/commit'
  ]);
  if (!mutationPaths.has(url)) return false;
  const body = await bodyObject();
  if (url === '/config/sync') return ok(handlers.syncStartupConfig(requireString(body.content, '配置内容', { required: true, maxLength: maxConfigBytes, trim: false })));
  if (url === '/config/activate') return ok(handlers.activateStartupConfig(requireTransactionId(body.txId)));
  if (url === '/config/rollback') return ok(handlers.rollbackStartupConfig(requireTransactionId(body.txId)));
  if (url === '/config/commit') return ok(handlers.commitStartupConfig(requireTransactionId(body.txId)));
  if (url === '/config/inspect-path') return ok(handlers.inspectSystemConfigPath(requireString(body.path, '配置路径', { required: true, maxLength: 4096 })));
  if (url === '/config/read-path') return ok(handlers.readSystemConfigPath(requireString(body.path, '配置路径', { required: true, maxLength: 4096 })));
  if (url === '/app/icon/update') return ok(handlers.applyAppIcon(requireString(body.iconId, '图标 ID', { required: true, maxLength: 64, pattern: /^[a-z0-9][a-z0-9-]*$/ }), true));
  if (url === '/system/proxy-environment/update') return ok(handlers.updateSystemProxyEnvironment(body));
  if (url === '/network/update') return ok(handlers.updateNetworkConfig(body));
  if (url === '/core/install') return ok(handlers.installCore(
    requireString(body.stagePath, 'Core 暂存路径', { required: true, maxLength: 4096 }),
    requireString(body.expectedVersion, 'Core 版本', { required: true, maxLength: 64 }),
    Boolean(body.restart)
  ));
  if (url === '/core/rollback') return ok(handlers.rollbackCore(requireTransactionId(body.txId), Boolean(body.restart)));
  if (url === '/core/commit') return ok(handlers.commitCore(requireTransactionId(body.txId)));
  return false;
}

module.exports = { handlePrivilegedApi };
