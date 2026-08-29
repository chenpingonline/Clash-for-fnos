'use strict';

/** @param {unknown} value */
function versionTuple(value) {
  const match = String(value || '').match(/v?(\d+)\.(\d+)\.(\d+)/i);
  return match ? match.slice(1).map(Number) : null;
}

/** @param {unknown} a @param {unknown} b */
function compareVersions(a, b) {
  const left = versionTuple(a);
  const right = versionTuple(b);
  if (!left || !right) return 0;
  for (let index = 0; index < 3; index += 1) {
    if (left[index] !== right[index]) return left[index] > right[index] ? 1 : -1;
  }
  return 0;
}

/** @param {string} platform @param {string} machineInput @param {string} nodeArch */
function detectMihomoDownloadTarget(platform, machineInput, nodeArch) {
  const machine = String(machineInput || nodeArch).trim().toLowerCase();
  if (platform !== 'linux') throw new Error(`当前系统不是 Linux，无法自动选择 Mihomo Core：${platform}/${machine}`);
  let arch = null;
  if (['x86_64', 'amd64'].includes(machine) || nodeArch === 'x64') arch = 'amd64';
  else if (['aarch64', 'arm64'].includes(machine) || nodeArch === 'arm64') arch = 'arm64';
  else if (/^(i[3-6]86|x86)$/.test(machine) || nodeArch === 'ia32') arch = '386';
  else if (/^armv7/.test(machine)) arch = 'armv7';
  else if (/^armv6/.test(machine)) arch = 'armv6';
  else if (/^armv5/.test(machine)) arch = 'armv5';
  else if (machine === 'riscv64') arch = 'riscv64';
  else if (['loongarch64', 'loong64'].includes(machine)) arch = 'loong64';
  else if (machine === 'ppc64le') arch = 'ppc64le';
  else if (machine === 's390x') arch = 's390x';
  if (!arch) throw new Error(`暂不支持自动选择 Mihomo Core 的 CPU 架构：${machine}（Node: ${nodeArch}）`);
  return { os: 'linux', arch, machine, nodeArch };
}

/**
 * Return official release asset names in preference order. Clash Verge Rev uses
 * amd64-v2 on 64-bit x86 and the standard variants on the other Linux targets.
 * The plain amd64 asset remains a compatibility fallback for older releases.
 * @param {{os: string, arch: string}} target
 * @param {string} tag
 */
function mihomoReleaseAssetNames(target, tag) {
  if (!/^v\d+\.\d+\.\d+$/.test(String(tag || ''))) throw new Error(`Mihomo 版本号无效：${tag || 'unknown'}`);
  const prefix = `mihomo-${target.os}-`;
  if (target.arch === 'amd64') return [`${prefix}amd64-v2-${tag}.gz`, `${prefix}amd64-${tag}.gz`];
  return [`${prefix}${target.arch}-${tag}.gz`];
}

module.exports = { compareVersions, detectMihomoDownloadTarget, mihomoReleaseAssetNames, versionTuple };
