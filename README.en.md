# Clash for fnOS

[简体中文](README.md) | English

<img src="fpk/ICON_256.PNG" alt="Clash for fnOS" width="96" />

A native Mihomo manager for fnOS. This repository owns **FPK packaging, host integration and native releases**. Shared Vue / Go code, translations and Docker builds are maintained in [Clash-Manager](https://github.com/chenpingonline/Clash-Manager).

[FPK downloads](https://github.com/chenpingonline/Clash-for-fnos/releases) · [User guide](docs/user-guide.md) · [Docker](https://github.com/chenpingonline/Clash-Manager/blob/master/docker/README.en.md) · [Changelog](CHANGELOG.md)

![Interface example](img.png)

## Compatibility

The app keeps profiles, proxies, rules, configuration editing, connections, logs, DNS, TUN, Core / GEO management, fnOS system proxy, native folder authorization, four icons and FPK updates. Native startup opens the main UI directly. Language selection appears only in the Settings header and is remembered in the browser.

The split preserves appname, Gateway paths, Unix sockets, data paths and lifecycle scripts. Shared code retains the fnOS capability adapter, avoiding two implementations of business logic.

## Build

Install Bash, Git, Python 3.12+, Go, Node.js / npm (see the shared web/package.json engines), tar, gzip, md5sum and sha256sum. Linux, WSL and macOS are supported; macOS needs GNU checksum tools. The first build needs access to GitHub and npm.

```bash
./scripts/check.sh
# Before a new installable FPK, increment the manifest patch and complete CHANGELOG.
./scripts/build-manual.sh x86
./scripts/build-manual.sh arm
./scripts/build-manual.sh all
python3 scripts/audit-fpk.py
```

upstream.lock pins the shared repository, version and full commit SHA. Builds export that exact commit into a temporary directory, inject this repository's manifest version, changelog and icons, then compile and assemble the FPK. Neither source checkout is rewritten. Installed apps do not fetch program source from GitHub.

Build serially. x86 / arm include matching Mihomo; all contains both program architectures and downloads Core for the device architecture when first enabled. Artifacts and checksum sidecars are written to dist/.

## Adopt a shared update

```bash
./scripts/update-upstream.sh <full-40-character-commit-SHA>
./scripts/check.sh
```

The script reads VERSION from that commit and updates upstream.lock. Review and validate before committing the lock and native changes. Shared and FPK versions are independent; packaged build-info.json records both and the shared commit.

Git objects are cached in .cache/upstream.git. CLASH_OFFLINE=1 prevents Git network fetches when cached; npm dependencies must also be available. CLASH_SHARED_SOURCE=/absolute/path/Clash-Manager reads a local Git checkout, but its HEAD must equal the locked commit and the source must be clean.

See [maintenance](docs/repository-maintenance.md). Git pushes, FPK publication and Docker image publication are separate operations.

## Validation and license

Local checks and package audits do not prove installation, upgrades, host theme, TUN or networking on a real fnOS device. Those require separate validation.

[GPL-3.0](LICENSE). No proxy nodes or subscriptions are provided.
