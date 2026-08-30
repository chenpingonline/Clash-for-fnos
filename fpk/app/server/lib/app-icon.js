'use strict';

const crypto = require('node:crypto');

/**
 * @param {string} iconId
 * @param {Buffer | Uint8Array | string} icon64
 * @param {Buffer | Uint8Array | string} icon256
 */
function versionedAppIconKey(iconId, icon64, icon256) {
  const id = String(iconId || '').trim();
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(id)) throw new Error('软件图标标识无效');
  const revision = crypto.createHash('sha256')
    .update(icon64)
    .update(icon256)
    .digest('hex')
    .slice(0, 12);
  return `${id}-${revision}`;
}

/** @param {string} iconKey */
function appIconEntryPath(iconKey) {
  const key = String(iconKey || '').trim();
  if (!/^[a-z0-9][a-z0-9-]{0,80}$/.test(key)) throw new Error('软件图标资源标识无效');
  return `images/icons/${key}_{0}.png`;
}

module.exports = { appIconEntryPath, versionedAppIconKey };
