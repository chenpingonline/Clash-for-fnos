'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

test('frontend view helpers escape user-controlled content and format values', async () => {
  const { escapeHtml, formatBytes, formatRate, normalizeSubscriptionInfo } = await import('../public/lib/view-utils.js');
  assert.equal(escapeHtml('<img src=x onerror="alert(1)">'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
  assert.equal(formatBytes(1536), '1.5 KB');
  assert.equal(formatRate(1024), '1.0 KB/s');
  assert.deepEqual(normalizeSubscriptionInfo({ Upload: '10', Download: 20, Total: 100 }), { upload: 10, download: 20, total: 100, expire: 0 });
});
