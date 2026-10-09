<div align="center">

<img src="fpk/ICON_256.PNG" alt="Clash for fnos" width="128" />

# Clash for fnos

[简体中文](README.md) | English

**A native Mihomo / Clash manager for fnOS**

Manage Mihomo Core, proxies, profiles, rules, connections, logs, TUN and system proxy environment variables from the fnOS desktop.

[![Release](https://img.shields.io/github/v/release/chenpingonline/Clash-for-fnos?display_name=tag)](https://github.com/chenpingonline/Clash-for-fnos/releases)
[![Downloads](https://img.shields.io/github/downloads/chenpingonline/Clash-for-fnos/total)](https://github.com/chenpingonline/Clash-for-fnos/releases)
![fnOS](https://img.shields.io/badge/fnOS-x86__64%20%7C%20ARM64-2ea44f)
[![Mihomo](https://img.shields.io/badge/Core-Mihomo-6f42c1)](https://github.com/MetaCubeX/mihomo)
[![License](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

[User guide (Chinese)](docs/user-guide.md) · [Download](https://github.com/chenpingonline/Clash-for-fnos/releases) · [Issues](https://github.com/chenpingonline/Clash-for-fnos/issues) · [Docker deployment](https://github.com/chenpingonline/Clash-Manager/blob/master/docker/README.en.md) · [Mihomo](https://github.com/MetaCubeX/mihomo) · [Clash-Manager](https://github.com/chenpingonline/Clash-Manager)

</div>

---

![Clash for fnos example interface](img.png)

The screenshot is illustrative. Features and navigation depend on the current version.

## Overview

Clash for fnos is a Mihomo manager designed for **fnOS NAS devices**, offering a graphical interface without frequent SSH sessions or manual YAML editing. The Docker edition is called **Clash Manager** and shares the application code.

This repository maintains native FPK packaging, fnOS integration and releases. Shared frontend, backend, translations, Linux packages and Docker live in Clash-Manager. Builds use a pinned shared commit, so common features are developed once.

The backend is fully implemented in Go. An unprivileged Web service handles the UI, APIs and Mihomo Controller communication. A separate Go Root Helper performs required system operations through a private Unix socket with an explicit API allowlist.

Two Core modes and both offline architecture packages and an online universal package are available:

- **Manager-managed mode** is the default on first startup. Architecture packages use their bundled Core; the `all` package downloads the appropriate Core from official Releases. Both verify SHA-256 before activation.
- **External mode** uses an existing Mihomo installation. Select it under Settings → Mihomo Core settings → Core management → Core operating mode. The selection persists across restarts and upgrades. Managed mode checks actual port conflicts and does not stop external Mihomo processes.

> [!IMPORTANT]
> This project manages Mihomo. It **does not provide proxies, subscription services or network access**. Supply your own valid Mihomo configuration or profile.

## Languages

The interface supports **Simplified Chinese** and **English**. Select a language under **Settings → Language (top right)**. **Follow system** uses the browser language: Chinese locales select Chinese; other locales select English. Your explicit selection is stored in the current browser and takes effect immediately.

Proxy names, profile names, YAML keys and content, addresses, paths and original logs retain their original values. Dates use the selected interface language. Both the native fnOS edition and Docker edition use the same language packs. See [Translation maintenance](docs/i18n.md) for terminology and contribution rules.

## Features

| Module | Features |
| --- | --- |
| Dashboard | Core status, exit location, persistent traffic totals, up to 10 minutes of live traffic, memory, ports, LAN, IPv6 and TUN status |
| Proxies | Proxy groups, selection, latency tests and persistent group choices |
| Profiles | Remote profiles, local YAML imports, updates, application, automatic updates/application; visual rules/proxies/groups editors and profile/global overrides and scripts |
| Configuration | Preview and edit managed startup YAML; validate, save and apply with automatic recovery on failure |
| Rules | View rules, persist enabled/disabled states per profile, explain sources, update individual or all rule providers |
| Connections | Active connections, upload/download totals, close one or all connections |
| Logs | Live and historical Mihomo logs, level/line-count filters, search highlighting, wrapping and clearing |
| Network | Mixed / HTTP / SOCKS / Redir / TProxy ports, Allow LAN and IPv6 |
| DNS | Mihomo DNS, resolvers, Fake IP, domain policies, fallback filters and Hosts; backup, validation, application and rollback |
| TUN | Toggle, stack, MTU, auto route, DNS hijacking, strict route and excluded networks, with validation and upgrade recovery |
| Environment | Proxy variables in `/etc/environment`, `/etc/profile` and `/etc/bash.bashrc` (native fnOS only) |
| Core | Detect installed Mihomo; bundled/downloaded Core, online updates, backups and rollback |
| GEO | Bundled initial data; GeoIP, GeoSite, Country MMDB and ASN MMDB updates |
| Icons | fnOS desktop/window icon choices |
| Application updates | GitHub Release checks; fnOS Application Center handles FPK upgrades |
| Languages | Chinese / English / Follow system, with a persistent browser preference |

### Configuration editing and rule toggles

- Configuration previews default to compact YAML, with formatted/compact views, line numbers and syntax highlighting. Compact lists stay on a single line; the horizontal scrollbar stays visible, as does the editor's vertical scrollbar. Very long lines initially show their prefix and a **Show more (size)** button. Expand or collapse them; searching reveals the matching line and can locate text near its end. Copying and editing use the complete configuration. Formatting/compacting a draft supports undo. Content is written only after **Save and apply**; collapsed content is retained.
- **Configuration → Edit configuration** edits the startup YAML of the running managed Core. Saving reuses backup, Mihomo validation, application and rollback, and synchronizes startup configuration. External Core configurations are read-only. If the profile or configuration changes while editing, overwriting is refused: keep the draft, reload and merge it.
- Direct edits may be overwritten by later profile application. For lasting changes, use **Profiles → Edit → Edit rules / Edit proxies / Edit proxy groups**, overrides or scripts. Visual editors offer source filters, prepend/append entries, exclusion and restoration; enhancements are stored per profile and reapplied after updates. Advanced mode edits enhancement YAML. Current Controller address and Secret are preserved.
- Each rule toggle immediately changes Core runtime behavior and is saved automatically. Reloading configurations, applying profiles and restarting Core restore saved states. Matching uses rule type, content and target policy rather than position. Different profiles keep separate records. Changed content or duplicate counts skip restoration to avoid disabling the wrong entry. The backend retries every 10 seconds. Unsupported Core versions disable the toggles. A disabled rule falls through; disabling `RULE-SET` skips the referenced provider.
- Proxy groups and routing rules usually come from the full profile YAML. The manager does not add them by default. Profile/global enhancements can modify the final configuration. Inspect `proxy-groups`, `rules` and `rule-providers`, or open **Rule help**, to identify sources.

### Profile enhancements

Open a profile's **Edit** menu:

- **Edit rules**: choose type, content and policy, prepend/append, search/edit/reorder added rules, exclude/restore original rules.
- **Edit proxies**: paste one proxy URI per line or import a Base64 proxy list; prepend/append, search/filter/reorder, exclude/restore original proxies. Use table **Edit** to access advanced YAML for custom parameters.
- **Edit proxy groups**: type, name, members, providers and health checks; add/edit/reorder and filter/exclude/restore original groups. Optional health-check fields are under **More settings**.
- **Override configuration / Extension script**: other configuration fields or complex changes. Global enhancement controls on the Profiles page participate in every profile application.

The first three editors default to visual mode; **Advanced** edits `prepend`, `append` and `delete` YAML. **Save and apply** stores enhancements and applies the profile. Reset changes only the current editor draft until saved. Download remote profiles first to view their original entries.

The question mark beside each editor title shows examples: enhancement YAML for rules/proxies/groups, configuration fragments for overrides, and JavaScript for scripts. Help remains available in advanced mode.

The final configuration is generated in this order:

```text
Original profile → rule / proxy / group enhancements → global override
→ global script → profile override → profile script
→ saved Core settings and enabled DNS / Hosts overrides → validate and apply
```

Objects merge recursively; arrays replace the previous array. Scripts define `main(config, profileName)` and return a configuration object. Enhancement files are separate from original profiles, so updates reapply your changes.

## Core modes

### Manager-managed mode

On initial startup or when selecting managed mode:

1. Detect the Linux CPU architecture.
2. Use bundled Core for architecture packages or select an official GitHub Release asset for `all`.
3. Verify file size and SHA-256.
4. Install Core in the application's own data directory.
5. Prepare managed configuration and GEO data.
6. Start Mihomo and read Controller, Secret and Mixed Port settings.

Start, stop or restart managed Core under **Settings → Mihomo Core settings → Core management**. Stopping leaves the management interface and managed mode available; start Core or use dashboard detection to recover. Restarting the application automatically enables managed Core again. External Core lifecycle stays with its existing service.

Architecture packages do not need a Core download on first use. The smaller `all` package needs access to GitHub for initial activation.

### External mode

Selecting external Mihomo avoids overwriting the existing installation, leaves startup/shutdown to its original service, attempts to discover its process/configuration/Controller/Secret, and retains proxy, rule, connection and log management.

Maintain the external configuration yourself. System configuration operations attempt to back up before applying changes.

## Supported platforms

| fnOS CPU architecture | Release file | Core delivery |
| --- | --- | --- |
| Intel / AMD x86_64 | `Clash for fnos_<version>_x86_64.fpk` | Bundled `linux/amd64` |
| ARM64 / aarch64 | `Clash for fnos_<version>_arm64.fpk` | Bundled `linux/arm64` |
| Universal fnOS x86 / ARM | `Clash for fnos_<version>_all.fpk` | Detected and downloaded at runtime |

Requirements: **fnOS 1.1.3100 or later**, with unified application gateway; administrator access to install. Go binaries are statically compiled into the FPK; no Node.js or other runtime is required.

Starting with v1.3.0, `os_min_version=1.1.3100` declares the minimum OS version. Older packages omitting it do not imply support for older systems such as fnOS 1.1.26.

> [!TIP]
> Choose `all` if unsure of your architecture. Choose the architecture package for known `x86_64` or `aarch64` / `arm64` devices when you want first startup without a GitHub download.

## Installation

1. Open [Releases](https://github.com/chenpingonline/Clash-for-fnos/releases).
2. Download your architecture's `.fpk` or the universal `all` package without Core.
3. Open **fnOS → Application Center → Manual installation**.
4. Select the FPK and finish installation.
5. Open **Clash for fnos** from the desktop.

Use manual installation to upgrade an existing installation.

### Upgrade notes

- Profiles, managed configuration, backups and settings remain in the application's fnOS configuration/data directories.
- Explicitly saved ports, LAN access, global IPv6, Unified Delay, TUN and GEO preferences are stored in `core-user-settings.json`. Every profile application, including automatic updates/application, merges these preferences before validation; untouched fields follow the profile. Enabled DNS overrides also apply saved DNS/Hosts settings.
- Upgrades from versions before v1.0.0 do not convert the whole old configuration into preferences: those versions did not track where modifications came from. Save settings that should persist across profiles again. Direct YAML editing does not register preferences.
- The application no longer requires `nodejs_v22`.
- Upgrading with TUN enabled briefly releases and recreates its interface and routes. Check Core, TUN and exit connectivity afterward.

### SHA-256 verification

Local builds generate an `.fpk.sha256` alongside each x86_64, arm64 and all package. Release attachments depend on what was actually published. If the download includes a checksum file, verify in the same directory:

```bash
sha256sum -c "Clash for fnos_<version>_x86_64.fpk.sha256"
```

## Repository layout

This repository maintains fnOS packaging and host integration. `upstream.lock` pins shared application source to a full commit SHA.

```text
Clash-for-fnos/
├── fpk/
│   ├── app/ui/          # Desktop entry and icons
│   ├── cmd/             # Lifecycle scripts
│   ├── config/          # Privilege / resource configuration
│   ├── wizard/          # Installation / uninstallation wizard
│   ├── ICON.PNG
│   ├── ICON_256.PNG
│   └── manifest         # FPK version source
├── upstream.lock        # Shared repository, VERSION and full commit SHA
├── scripts/             # Pinned source retrieval, checks, builds and audits
├── docs/                # fnOS documentation and screenshots
├── CHANGELOG.md         # fnOS release notes
├── LICENSE
├── README.md
├── README.en.md
└── dist/                # Final packages and checksums; not committed
```

Shared `backend/`, `web/`, `resources/core/` and `assets/geodata/` live in Clash-Manager. Builds export the pinned commit into a temporary directory and compile it into the FPK. Installed applications do not fetch application source from GitHub at startup.

See [Backend architecture](https://github.com/chenpingonline/Clash-Manager/blob/master/backend/README.en.md).

## Building from source

### Build environment

Bash scripts support Linux, WSL2, GitHub Actions Linux runners and macOS. macOS needs GNU-compatible utilities, particularly `md5sum` and `sha256sum`, which are not installed by default.

Required commands: `bash`, `git`, `python3`, `cp`, `tar`, `gzip`, `sed`, `awk`, `mktemp`, `md5sum`, `sha256sum`. The first build needs access to GitHub and npm. The build machine needs **Python 3.12+**, **Node.js 22.12+**, npm and **Go 1.22+**. Node.js is only a frontend build dependency; it is not needed by the FPK.

### Single application version

The FPK version comes from `version` in `fpk/manifest`, using `major.minor.patch`. Update its matching `CHANGELOG.md` before packaging.

- `upstream.lock` records the shared source version and commit independently of the FPK version.
- Builds inject the FPK version and fnOS changelog into an isolated shared source export, synchronize its npm versions, and pass the FPK version to both Go binaries through linker flags.
- Builds do not modify the Clash-Manager checkout. Frontend build dependencies are not included in the FPK.
- Packaged `build-info.json` records the FPK version, shared version and full source SHA.

Scripts do not increment versions automatically. Increment the manifest patch version for each newly produced installable FPK; failed retries without an installable output do not require another increment.

### Development checks

Run from this repository:

```bash
./scripts/check.sh
```

Checks cover lifecycle script syntax, pinned-source retrieval tests, shared Go race tests and vet, plus frontend catalogs, types, tests and the fnOS build.

Production packages contain Vue assets, static Go binaries, configuration, GEO data and selected Core resources. Architecture packages contain one Web/Helper pair; `all` contains both and chooses at startup. No language runtime is required on fnOS.

### Get the source

```bash
git clone https://github.com/chenpingonline/Clash-for-fnos.git
cd Clash-for-fnos
chmod +x scripts/*.sh
```

### Architecture builds

| Package | Build command | Equivalent shortcut | Output in `dist/` |
| --- | --- | --- | --- |
| x86_64 | `./scripts/build-manual.sh x86` | `./scripts/build-x86.sh` | `Clash for fnos_<version>_x86_64.fpk` and `.fpk.sha256` |
| ARM64 | `./scripts/build-manual.sh arm` | `./scripts/build-arm.sh` | `Clash for fnos_<version>_arm64.fpk` and `.fpk.sha256` |
| Universal | `./scripts/build-manual.sh all` | `./scripts/build-all.sh` | `Clash for fnos_<version>_all.fpk` and `.fpk.sha256` |

Universal manifests use `platform=all`. They contain both x86_64 and ARM64 Go binaries, selected at startup, but no `mihomo-linux-*.gz`; Core is downloaded online. 32-bit x86 and ARMv7 are unsupported.

## Build process

`build-manual.sh` retrieves the pinned commit, builds an isolated source export and assembles the FPK in a temporary stage:

```text
upstream.lock SHA + shared VERSION → retrieve / validate / export source
→ inject FPK manifest version, fnOS CHANGELOG and icons → build Vue
→ copy fpk integration files and build assets into stage
→ write GEO assets and build-info.json
→ x86/arm: select shared resources/core/<arch> assets
  all: include an online-delivery marker without Core binaries
→ set staged platform → cross-compile Go Web and Helper
→ archive app/ as app.tgz → write MD5 to manifest checksum
→ generate FPK and SHA-256 files in dist/
```

All architectures share the same application source. Build and audit serially:

```bash
./scripts/build-manual.sh x86
./scripts/build-manual.sh arm
./scripts/build-manual.sh all
python3 scripts/audit-fpk.py
```

Automated checks and archive audits do not verify installation, upgrades, themes, TUN or networking on a real fnOS device.

## Adopting shared updates

Develop, validate and commit common features in Clash-Manager first. To adopt an update, specify its full 40-character commit SHA:

```bash
./scripts/update-upstream.sh <full-40-character-commit-SHA>
./scripts/check.sh
```

The script reads that commit's `VERSION` and updates `upstream.lock`. It does not follow master or latest automatically. Review and commit the lock and fnOS changes. For a new FPK, also update the manifest and changelog before building.

Git objects are cached in `.cache/upstream.git`. `CLASH_OFFLINE=1` disables Git network retrieval when source is cached; npm dependencies must also be cached. `CLASH_SHARED_SOURCE=/absolute/path/Clash-Manager` reads local Git source, requiring a clean checkout with HEAD equal to the pinned SHA.

See [Repository maintenance](docs/repository-maintenance.md). Git pushes, FPK publication and Docker publication are separate operations.

## Universal package Core downloads

The implementation references [Clash Verge Rev Core updates](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src-tauri/src/feat/core_upgrade.rs). Stable Core comes from [official Mihomo Releases](https://github.com/MetaCubeX/mihomo/releases); the manager reads the latest Release and selects for the running Linux architecture:

| Architecture | Preferred asset |
| --- | --- |
| `x86_64` / `amd64` | `mihomo-linux-amd64-v2-<version>.gz`, falling back to `mihomo-linux-amd64-<version>.gz` |
| `aarch64` / `arm64` | `mihomo-linux-arm64-<version>.gz` |

The Helper restricts download domains and file size, obtains SHA-256 from official Release metadata, verifies the file, decompresses it, runs `mihomo -v` and validates configuration before atomically replacing managed Core. Failed downloads/validation stop installation and show an error; retry after restoring connectivity.

## Updating bundled Core

In the shared Clash-Manager repository, architecture resources live in `resources/core/x86/` and `resources/core/arm/`. Each contains the official `mihomo-linux-<arch>-<version>.gz`, `bundled-core.json`, `EXPECTED_ASSET.txt` and `THIRD_PARTY_NOTICES.txt`.

Update the archive and its version, architecture, size and SHA-256 metadata in `bundled-core.json`; update filename/size/SHA-256 in `EXPECTED_ASSET.txt` and upstream/version notices in `THIRD_PARTY_NOTICES.txt`. Commit the resources in Clash-Manager, update `upstream.lock` in the fnOS repository, then rebuild both architecture packages.

> [!WARNING]
> Replacing only the archive without its verification metadata causes activation to fail.

## GEO data

Initial offline data from the shared repository's `assets/geodata/` is packaged at `fpk/app/geodata/Country.mmdb`, `geoip.dat` and `geosite.dat`. It is copied only on first startup when the target file is absent, preserving subsequent updates.

Settings show size, update time and sources for GeoIP, GeoSite, Country MMDB and ASN MMDB. Missing assets can be downloaded individually; the Helper requires HTTPS, limits size, validates format and writes atomically without replacing existing files. **Update now** uses Mihomo `/upgrade/geo`.

Managed mode supports scheduled updates. Settings are persisted after backup and `mihomo -t` validation. External mode is read-only and does not change external GEO files/settings.

## Configuration and data

| fnOS variable | Purpose |
| --- | --- |
| `TRIM_APPDEST` | Installed application runtime files |
| `TRIM_PKGETC` | Configuration, profile metadata and backups |
| `TRIM_PKGVAR` | Managed Core, logs and runtime data |

Original profile YAML and enhancements live in `${TRIM_PKGETC}/profiles/`. Rule states live in `${TRIM_PKGETC}/rule-state.json`, keyed by profile ID, so renaming does not lose them. Non-profile managed configuration uses a separate default record.

Privileged Helper operations include installing/starting managed Core, backing up/writing system configuration, applying TUN/network changes, managing system proxy variables and synchronizing fnOS icons. The Web service delegates these operations.

## Proxy environment variables

The native fnOS edition manages `/etc/environment`, `/etc/profile` and `/etc/bash.bashrc`, writing `http_proxy` / `HTTP_PROXY`, `https_proxy` / `HTTPS_PROXY` and `no_proxy` / `NO_PROXY`. It can follow Mixed Port automatically. Disabling removes only application-owned entries and preserves other system configuration.

> [!NOTE]
> Only programs that honor proxy environment variables use them. Docker containers, systemd services and other software may require their own configuration. TUN provides more transparent TCP/UDP routing.

## TUN and upgrade recovery

The quick toggle prepares configuration, validates with `mihomo -t`, changes runtime state, confirms Controller status and commits the transaction. It does not overwrite the entire network configuration. Failure attempts to restore the previous configuration and runtime state.

For managed upgrades, the Helper sends `SIGTERM` and waits for Mihomo to release its TUN interface/routes before forcing termination after a timeout. Startup reads persisted settings and reconciles TUN state. If recovery fails, it attempts a restart using the validated persisted configuration.

Default MTU is `1500`; `1400` may help with VPN, PPPoE or nested tunnels. Excluded networks accept IPv4/IPv6 CIDR for LAN or other virtual networks.

## FAQ

### Will it start a second Core if Mihomo is already installed?

Initial startup uses managed Core. Select external mode in Core settings to use an existing installation. Port conflicts show an error; the manager does not stop external processes.

### How can I change ports when Core cannot start?

Click **Change ports** in the dashboard conflict notice, then **Save** or **Save and start**. While managed Core is stopped, **Settings → Network and ports** can save startup ports after backup/validation without an online Controller. Controller address follows the new port. Errors retain the edit controls; SSH commands are under advanced troubleshooting. Docker also supports startup port variables in [Compose](https://github.com/chenpingonline/Clash-Manager/blob/master/docker/README.en.md).

### Can a new fnOS installation use it without installing Mihomo first?

Yes. Architecture FPKs include official Core and validate it locally before activation.

### Why does `127.0.0.1:<Mixed Port>` not reach the host from a container?

Container loopback points to the container itself. Use the host address or the appropriate Docker network configuration.

### How do proxy environment variables differ from TUN?

Variables affect programs that read them. TUN transparently routes traffic; the suitable approach depends on the application/network.

### Can I edit YAML directly?

Managed startup YAML is editable under Configuration, with validation, backups and application. External configurations are read-only. Use profile enhancements, overrides or scripts for changes that must survive profile application.

### Do disabled rules become enabled after profile updates or restarts?

Saved states are restored per profile after switching, reloads, updates/application and Core/application restarts. Matching uses type, content and policy; changed content or duplicate counts skip restoration. Save failures roll back the toggle; restoration failures retain records and retry.

### How do I diagnose a blank application window?

Confirm fnOS is at least **1.1.3100**. Read Web/Helper startup logs over SSH:

```bash
sudo tail -n 200 /var/apps/clash-for-fnos/var/clash-for-fnos.log
# Follow logs while stopping/starting the app in Application Center:
sudo tail -f /var/apps/clash-for-fnos/var/clash-for-fnos.log
```

Press `Ctrl+C` to stop following. The application Logs page displays Mihomo logs, which differ from startup logs. If startup looks normal, attach browser Console errors and failed Network paths/status codes.

### Does this project provide proxies or subscriptions?

No. It manages Mihomo only.

## Contributing

Issues and pull requests are welcome. Submit fnOS lifecycle, permissions, window integration and FPK changes here; shared Vue / Go, translations, Linux packages and Docker changes belong in [Clash-Manager](https://github.com/chenpingonline/Clash-Manager). Include OS version, CPU architecture, application/Core versions, managed/external mode and relevant screenshots or logs. Avoid exposing subscription URLs, Secrets or passwords. Translation guidelines are in [docs/i18n.md](docs/i18n.md).

## Acknowledgments

- [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo): Core
- [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat): GEO data
- [Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev): GUI design and terminology reference
- [fnOS developer documentation](https://developer.fnnas.com/): packaging, entry points and runtime conventions

Thanks to the upstream maintainers and contributors.

## License

Project source is [GPL-3.0-only](LICENSE). Distributed packages keep `licenses/LICENSE` inside the application; first installation has no license acceptance step. Third-party components retain their licenses: Mihomo is GPL-3.0-or-later. Versions, asset origins and notices are maintained in the shared repository at `resources/core/<arch>/THIRD_PARTY_NOTICES.txt` and packaged with Core.

## Docker and Linux packages

[Clash-Manager](https://github.com/chenpingonline/Clash-Manager) maintains Docker and Linux packages using the same shared application code as fnOS. This repository continues to maintain native FPKs.

- [Docker deployment, Compose and environment examples](https://github.com/chenpingonline/Clash-Manager/blob/master/docker/README.en.md)
- [Clash-Manager releases and installation](https://github.com/chenpingonline/Clash-Manager)
