'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { handlePrivilegedApi } = require('../lib/privileged-api');

function context(body, calls) {
  return {
    maxConfigBytes: 1024,
    readBody: async () => body,
    sendJson: (status, payload) => calls.push({ status, payload }),
    handlers: new Proxy({}, {
      get: (_, name) => (...args) => ({ name, args })
    })
  };
}

test('privileged API validates a transaction before invoking its handler', async () => {
  const calls = [];
  await handlePrivilegedApi({ method: 'POST', url: '/config/activate' }, context({ txId: 'tx-123' }, calls));
  assert.deepEqual(calls, [{ status: 200, payload: { name: 'activateStartupConfig', args: ['tx-123'] } }]);

  await assert.rejects(
    handlePrivilegedApi({ method: 'POST', url: '/config/activate' }, context({ txId: '../../etc/passwd' }, [])),
    /格式不正确/
  );
});

test('unknown privileged routes do not parse a body or call a handler', async () => {
  let bodyReads = 0;
  const handled = await handlePrivilegedApi({ method: 'POST', url: '/unknown' }, {
    ...context({}, []),
    readBody: async () => { bodyReads += 1; return {}; }
  });
  assert.equal(handled, false);
  assert.equal(bodyReads, 0);
});

test('configuration text is preserved at the privileged boundary', async () => {
  const calls = [];
  await handlePrivilegedApi({ method: 'POST', url: '/config/sync' }, context({ content: '  mixed-port: 7890\n' }, calls));
  assert.deepEqual(calls[0].payload.args, ['  mixed-port: 7890\n']);
});

test('app icon routes expose status and validate updates', async () => {
  const statusCalls = [];
  await handlePrivilegedApi({ method: 'GET', url: '/app/icon/status' }, context({}, statusCalls));
  assert.deepEqual(statusCalls, [{ status: 200, payload: { name: 'appIconStatus', args: [] } }]);

  const updateCalls = [];
  await handlePrivilegedApi({ method: 'POST', url: '/app/icon/update' }, context({ iconId: 'cat-world' }, updateCalls));
  assert.deepEqual(updateCalls, [{ status: 200, payload: { name: 'applyAppIcon', args: ['cat-world', true] } }]);

  await assert.rejects(
    handlePrivilegedApi({ method: 'POST', url: '/app/icon/update' }, context({ iconId: '../cat-world' }, [])),
    /格式不正确/
  );
});
