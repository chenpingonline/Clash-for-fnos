'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { parseProxyGroupOrder, parseYamlNameScalar } = require('../lib/yaml-proxy-groups');

test('proxy groups preserve YAML order and quoted names', () => {
  const yaml = `
mixed-port: 7890
proxy-groups:
  - name: 节点选择
    type: select
  - { name: "香港, 自动", type: url-test }
  - name: 'Work''s Proxy'
rules:
  - MATCH,节点选择
`;
  assert.deepEqual(parseProxyGroupOrder(yaml), ['节点选择', '香港, 自动', "Work's Proxy"]);
});

test('inline root proxy group lists fall back to API order', () => {
  assert.deepEqual(parseProxyGroupOrder('proxy-groups: [{ name: A }]'), []);
});

test('YAML scalar parser handles comments and escapes', () => {
  assert.equal(parseYamlNameScalar('Proxy # comment'), 'Proxy');
  assert.equal(parseYamlNameScalar('"Line\\nName"'), 'Line\nName');
});
