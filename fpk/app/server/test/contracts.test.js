'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { requireInteger, requireObject, requireString, requireTransactionId } = require('../lib/contracts');

test('requireObject rejects arrays and primitives', () => {
  assert.deepEqual(requireObject({ enabled: true }), { enabled: true });
  assert.throws(() => requireObject([]), /必须是对象/);
  assert.throws(() => requireObject('value'), /必须是对象/);
});

test('requireString preserves configuration text when trim is disabled', () => {
  const yaml = '  mixed-port: 7890\n';
  assert.equal(requireString(yaml, '配置内容', { trim: false }), yaml);
  assert.equal(requireString('  icon  ', '图标'), 'icon');
});

test('transaction and integer contracts reject unsafe values', () => {
  assert.equal(requireTransactionId('config-123_test'), 'config-123_test');
  assert.throws(() => requireTransactionId('../config'), /格式不正确/);
  assert.equal(requireInteger('7890', '端口', { min: 1, max: 65535 }), 7890);
  assert.throws(() => requireInteger(70000, '端口', { min: 1, max: 65535 }), /不能大于/);
});
