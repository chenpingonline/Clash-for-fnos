'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

test('starting a new page aborts and invalidates the previous render', async () => {
  const { createPageLifecycle } = await import('../public/lib/page-lifecycle.js');
  const lifecycle = createPageLifecycle();
  const logs = lifecycle.begin('logs');
  assert.equal(logs.isCurrent(), true);

  const settings = lifecycle.begin('settings');
  assert.equal(logs.signal.aborted, true);
  assert.equal(logs.isCurrent(), false);
  assert.equal(settings.isCurrent(), true);
});

test('cancel invalidates the active page', async () => {
  const { createPageLifecycle, isAbortError } = await import('../public/lib/page-lifecycle.js');
  const lifecycle = createPageLifecycle();
  const page = lifecycle.begin('logs');
  lifecycle.cancel();
  assert.equal(page.signal.aborted, true);
  assert.equal(page.isCurrent(), false);
  assert.equal(isAbortError(new DOMException('aborted', 'AbortError')), true);
});
