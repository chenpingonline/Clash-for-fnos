// @ts-check

/** @param {unknown} value */
export function escapeHtml(value) {
  return String(value ?? '').replace(/[&<>"']/g, character => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  })[character] || character);
}

/** @param {unknown} value */
export function formatBytes(value) {
  let bytes = Number(value || 0);
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let index = 0;
  while (bytes >= 1024 && index < units.length - 1) { bytes /= 1024; index += 1; }
  return `${bytes.toFixed(index ? 1 : 0)} ${units[index]}`;
}

/** @param {unknown} value */
export function formatRate(value) {
  return `${formatBytes(value)}/s`;
}

/** @param {unknown} value */
export function formatTime(value) {
  return value ? new Date(Number(value)).toLocaleString() : '从未';
}

/** @param {unknown} raw */
export function normalizeSubscriptionInfo(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const source = /** @type {Record<string, unknown>} */ (raw);
  /** @param {...string} keys */
  const pick = (...keys) => {
    for (const key of keys) {
      if (source[key] === undefined || source[key] === null) continue;
      const number = Number(source[key]);
      if (Number.isFinite(number) && number >= 0) return number;
    }
    return 0;
  };
  const info = { upload: pick('upload', 'Upload'), download: pick('download', 'Download'), total: pick('total', 'Total'), expire: pick('expire', 'Expire') };
  return (info.upload || info.download || info.total || info.expire) ? info : null;
}
