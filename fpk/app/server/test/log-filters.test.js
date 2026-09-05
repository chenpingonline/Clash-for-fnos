'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

async function setup() {
  const source = fs.readFileSync(require.resolve('../public/app.js'), 'utf8');
  const { escapeHtml } = await import('../public/lib/view-utils.js');
  const nodes = {};
  const qs = selector => nodes[selector] ||= { innerHTML: '', textContent: '', classList: { add() {} } };
  const streams = [];
  const context = vm.createContext({
    qs, esc: escapeHtml, PREFIX: '', logES: null, requestAnimationFrame: fn => fn(),
    api: async (url, options) => ({ items: options?.method === 'DELETE' ? [] : Array.from({ length: 1200 }, (_, i) => ({time: '2026-09-05T11:00:00Z', message: i % 2 ? 'ChatGPT.com [a+b] <img>' : 'example.com'})) }),
    EventSource: class { constructor() { streams.push(this); } close() {} },
    toast() {}, isAbortError: () => false,
  });
  vm.runInContext(source.slice(source.indexOf('const logFilters ='), source.indexOf('\nfunction portRow')), context);
  await vm.runInContext('renderLogs({isCurrent:()=>true})', context);
  return { qs, streams };
}

test('log filters cap the latest matches, highlight literal text, and escape markup', async () => {
  const { qs } = await setup();
  assert.equal((qs('#logs').innerHTML.match(/class="log-line/g) || []).length, 800);
  qs('#logSearch').oninput({ target: { value: 'chatgpt' } });
  qs('#logLimit').onchange({ target: { value: '100' } });
  assert.equal((qs('#logs').innerHTML.match(/<mark>ChatGPT<\/mark>/g) || []).length, 100);
  assert.match(qs('#logSummary').textContent, /100 \/ 600/);
  qs('#logSearch').oninput({ target: { value: '[a+b]' } });
  assert.match(qs('#logs').innerHTML, /<mark>\[a\+b\]<\/mark>/);
  assert.ok(!qs('#logs').innerHTML.includes('<img>'));
  qs('#logSearch').oninput({ target: { value: 'missing' } });
  assert.match(qs('#logs').innerHTML, /没有匹配/);
  qs('#logSearch').oninput({ target: { value: '' } });
  assert.match(qs('#logSummary').textContent, /100 \/ 1200/);
});

test('stream updates respect search, paused level changes stay paused, and clear resets the buffer', async () => {
  const { qs, streams } = await setup();
  qs('#logSearch').oninput({ target: { value: 'unique' } });
  streams[0].onmessage({ data: JSON.stringify({ message: 'UNIQUE event' }) });
  assert.match(qs('#logs').innerHTML, /<mark>UNIQUE<\/mark>/);
  qs('#toggleLogs').onclick();
  await qs('#logLevel').onchange({ target: { value: 'error' } });
  assert.equal(streams.length, 1);
  assert.equal(qs('#toggleLogs').textContent, '继续');
  await qs('#clearLogs').onclick();
  qs('#logSearch').oninput({ target: { value: '' } });
  assert.match(qs('#logSummary').textContent, /0 \/ 0/);
});
