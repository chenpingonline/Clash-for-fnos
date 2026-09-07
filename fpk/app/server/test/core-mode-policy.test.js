'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { resolveStartupCoreMode } = require('../lib/core-mode-policy');

test('defaults to Manager when no external Core is available', () => {
  assert.deepEqual(resolveStartupCoreMode(), { mode: 'managed', requiresChoice: false });
  assert.deepEqual(resolveStartupCoreMode({ externalAvailable: false }), { mode: 'managed', requiresChoice: false });
});

test('requires a startup choice when an external Core is available', () => {
  assert.deepEqual(resolveStartupCoreMode({ externalAvailable: true }), { mode: 'auto', requiresChoice: true });
});

test('an explicit startup choice is applied for the current run', () => {
  assert.deepEqual(resolveStartupCoreMode({ requestedMode: 'external', externalAvailable: true }), { mode: 'external', requiresChoice: false });
  assert.deepEqual(resolveStartupCoreMode({ requestedMode: 'managed', externalAvailable: true }), { mode: 'managed', requiresChoice: false });
});
