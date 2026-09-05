'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const fsp = fs.promises;
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const root = path.resolve(__dirname, '../../../..');

async function fixture(t) {
  const dir = await fsp.mkdtemp(path.join(os.tmpdir(), 'clash-version-'));
  t.after(() => fsp.rm(dir, { recursive: true, force: true }));
  await fsp.cp(path.join(root, 'scripts'), path.join(dir, 'scripts'), { recursive: true });
  await fsp.cp(path.join(root, 'fpk'), path.join(dir, 'fpk'), {
    recursive: true,
    filter: source => !['node_modules', 'test'].includes(path.basename(source)),
  });
  const manifest = path.join(dir, 'fpk/manifest');
  await fsp.writeFile(manifest, (await fsp.readFile(manifest, 'utf8')).replace(/^version\s*=.*$/m, 'version         = 9.8.7'));
  return dir;
}

test('manifest synchronizes application metadata and cache keys without changing dependencies', async t => {
  const dir = await fixture(t);
  const lockPath = path.join(dir, 'fpk/app/server/package-lock.json');
  const before = JSON.parse(await fsp.readFile(lockPath, 'utf8'));
  execFileSync('bash', [path.join(dir, 'scripts/sync-version.sh')]);
  const after = JSON.parse(await fsp.readFile(lockPath, 'utf8'));
  assert.equal(after.version, '9.8.7');
  assert.equal(after.packages[''].version, '9.8.7');
  for (const [key, value] of Object.entries(before.packages)) {
    if (key) assert.deepEqual(after.packages[key], value);
  }
  assert.equal(JSON.parse(await fsp.readFile(path.join(dir, 'fpk/app/server/package.json'), 'utf8')).version, '9.8.7');
  const html = await fsp.readFile(path.join(dir, 'fpk/app/server/public/index.html'), 'utf8');
  assert.equal((html.match(/\?v=9\.8\.7/g) || []).length, 3);
  execFileSync('bash', [path.join(dir, 'scripts/sync-version.sh')]);
  assert.equal(await fsp.readFile(lockPath, 'utf8'), JSON.stringify(after, null, 2) + '\n');
});

test('invalid manifest version is rejected before modifying derived files', async t => {
  const dir = await fixture(t);
  const pkg = path.join(dir, 'fpk/app/server/package.json');
  const before = await fsp.readFile(pkg, 'utf8');
  await fsp.writeFile(path.join(dir, 'fpk/manifest'), 'version = invalid\n');
  assert.throws(() => execFileSync('bash', [path.join(dir, 'scripts/sync-version.sh')], { stdio: 'pipe' }), /Invalid version/);
  assert.equal(await fsp.readFile(pkg, 'utf8'), before);
});

test('FPK build derives packaged versions from manifest without editing source metadata', async t => {
  const dir = await fixture(t);
  const pkg = path.join(dir, 'fpk/app/server/package.json');
  const before = await fsp.readFile(pkg, 'utf8');
  execFileSync('bash', [path.join(dir, 'scripts/build-manual.sh'), 'all'], { stdio: 'pipe' });
  const archive = path.join(dir, 'dist/Clash for fnos_9.8.7_all.fpk');
  const manifest = execFileSync('tar', ['-xOf', archive, './manifest'], { encoding: 'utf8' });
  assert.match(manifest, /^version\s*= 9\.8\.7$/m);
  const appArchive = path.join(dir, 'app.tgz');
  execFileSync('tar', ['-xf', archive, '-C', dir, './app.tgz'], { stdio: 'pipe' });
  const packaged = JSON.parse(execFileSync('tar', ['-xOf', appArchive, './server/package.json'], { encoding: 'utf8' }));
  assert.equal(packaged.version, '9.8.7');
  assert.match(execFileSync('tar', ['-xOf', appArchive, './server/public/index.html'], { encoding: 'utf8' }), /app\.js\?v=9\.8\.7/);
  assert.equal(await fsp.readFile(pkg, 'utf8'), before);
});
