'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { compareVersions, detectMihomoDownloadTarget, mihomoReleaseAssetNames, versionTuple } = require('../lib/version');

test('semantic versions are compared without changing legacy invalid-version behavior', () => {
  assert.deepEqual(versionTuple('v1.19.30'), [1, 19, 30]);
  assert.equal(versionTuple('latest'), null);
  assert.equal(compareVersions('v1.20.0', 'v1.19.30'), 1);
  assert.equal(compareVersions('v1.19.30', 'v1.19.30'), 0);
  assert.equal(compareVersions('latest', 'v1.19.30'), 0);
});

test('Mihomo assets map common fnOS CPU architectures', () => {
  assert.equal(detectMihomoDownloadTarget('linux', 'x86_64', 'x64').arch, 'amd64');
  assert.equal(detectMihomoDownloadTarget('linux', 'aarch64', 'arm64').arch, 'arm64');
  assert.equal(detectMihomoDownloadTarget('linux', 'armv7l', 'arm').arch, 'armv7');
  assert.equal(detectMihomoDownloadTarget('linux', 'loongarch64', 'loong64').arch, 'loong64');
  assert.throws(() => detectMihomoDownloadTarget('darwin', 'arm64', 'arm64'), /不是 Linux/);
});

test('Mihomo release assets follow Clash Verge Rev Linux preferences', () => {
  assert.deepEqual(mihomoReleaseAssetNames({ os: 'linux', arch: 'amd64' }, 'v1.19.30'), [
    'mihomo-linux-amd64-v2-v1.19.30.gz',
    'mihomo-linux-amd64-v1.19.30.gz'
  ]);
  assert.deepEqual(mihomoReleaseAssetNames({ os: 'linux', arch: 'arm64' }, 'v1.19.30'), [
    'mihomo-linux-arm64-v1.19.30.gz'
  ]);
  assert.deepEqual(mihomoReleaseAssetNames({ os: 'linux', arch: 'armv7' }, 'v1.19.30'), [
    'mihomo-linux-armv7-v1.19.30.gz'
  ]);
});
