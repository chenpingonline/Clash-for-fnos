'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { appIconEntryPath, versionedAppIconKey } = require('../lib/app-icon');

const fpkDir = path.resolve(__dirname, '..', '..', '..');
const uiIconDir = path.join(fpkDir, 'app', 'ui', 'images', 'icons');
const webIconDir = path.join(fpkDir, 'app', 'server', 'public', 'icons');

function pngSize(file) {
  const bytes = fs.readFileSync(file);
  assert.deepEqual(bytes.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), `${file} 不是 PNG`);
  return { width: bytes.readUInt32BE(16), height: bytes.readUInt32BE(20) };
}

test('all icon presets provide exact 64 and 256 pixel assets', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(uiIconDir, 'manifest.json'), 'utf8'));
  assert.ok(manifest.icons.length > 1);
  for (const { id } of manifest.icons) {
    for (const dir of [uiIconDir, webIconDir]) {
      assert.deepEqual(pngSize(path.join(dir, `${id}_64.png`)), { width: 64, height: 64 });
      assert.deepEqual(pngSize(path.join(dir, `${id}_256.png`)), { width: 256, height: 256 });
    }
  }
});

test('package and current entry icons use their declared dimensions', () => {
  assert.deepEqual(pngSize(path.join(fpkDir, 'ICON.PNG')), { width: 256, height: 256 });
  assert.deepEqual(pngSize(path.join(fpkDir, 'ICON_256.PNG')), { width: 256, height: 256 });
  assert.deepEqual(
    fs.readFileSync(path.join(fpkDir, 'ICON.PNG')),
    fs.readFileSync(path.join(fpkDir, 'ICON_256.PNG'))
  );
  assert.deepEqual(pngSize(path.join(fpkDir, 'app', 'ui', 'images', 'icon_64.png')), { width: 64, height: 64 });
  assert.deepEqual(pngSize(path.join(fpkDir, 'app', 'ui', 'images', 'icon_256.png')), { width: 256, height: 256 });
});

test('versioned icon references are stable for identical artwork and change with content', () => {
  const key = versionedAppIconKey('cat-orbit', Buffer.from('64'), Buffer.from('256'));
  assert.equal(key, versionedAppIconKey('cat-orbit', Buffer.from('64'), Buffer.from('256')));
  assert.notEqual(key, versionedAppIconKey('cat-orbit', Buffer.from('changed'), Buffer.from('256')));
  assert.match(appIconEntryPath(key), /^images\/icons\/cat-orbit-[a-f0-9]{12}_\{0\}\.png$/);
});
