<div align="center">

<img src="fpk/ICON_256.PNG" alt="Clash for fnos" width="128" />

# Clash for fnos

**运行在 fnOS 上的原生 Mihomo / Clash 管理器**

通过 fnOS 桌面直接管理 Mihomo Core、代理节点、订阅配置、规则、连接、日志、TUN 与系统代理环境变量。

[![Release](https://img.shields.io/github/v/release/chenpingonline/Clash-for-fnos?display_name=tag)](https://github.com/chenpingonline/Clash-for-fnos/releases)
![fnOS](https://img.shields.io/badge/fnOS-x86__64%20%7C%20ARM64-2ea44f)
[![Mihomo](https://img.shields.io/badge/Core-Mihomo-6f42c1)](https://github.com/MetaCubeX/mihomo)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

[下载 Releases](https://github.com/chenpingonline/Clash-for-fnos/releases) · [问题反馈](https://github.com/chenpingonline/Clash-for-fnos/issues) · [Mihomo](https://github.com/MetaCubeX/mihomo)

</div>

---

## 项目简介

Clash for fnos 是为 **飞牛 fnOS** 设计的 Mihomo 管理应用，目标是在 NAS 上提供一个无需频繁 SSH、无需手工修改 YAML 的图形化管理入口。

应用支持两种 Core 工作模式，并提供离线架构包与在线通用包：

- **Manager 托管模式**：系统未检测到现有 Mihomo 时，架构专用包使用内置 Core；`all` 通用包按运行平台从官方 Release 下载 Core。两种方式都会校验 SHA-256 后启用。
- **External 模式**：检测到用户已经安装或运行 Mihomo 时，优先连接现有 Core，不自动安装第二份 Mihomo。

> [!IMPORTANT]
> Clash for fnos 是 Mihomo 的管理工具，**不提供代理节点、订阅服务或任何网络线路**。请自行准备合法可用的 Mihomo 配置或订阅。

---

## 功能

| 模块 | 功能 |
| --- | --- |
| 仪表盘 | 查看 Mihomo 状态、实时上传/下载流量、当前端口、LAN、IPv6、TUN 等运行信息 |
| 代理节点 | 查看代理组与节点、切换节点、延迟测试、保存代理组选择 |
| 订阅配置 | 添加远程订阅、导入本地 YAML、更新订阅、应用配置、自动更新与自动应用 |
| 配置文件 | 查看当前生效配置、编辑托管配置、应用配置、同步启动配置、配置备份与恢复 |
| 规则 | 查看当前规则与 Rule Providers，支持单独或批量更新规则集 |
| 连接 | 查看当前活动连接、上传/下载统计，支持关闭单个连接或全部连接 |
| 日志 | 实时查看 Mihomo 日志、按日志级别筛选、查看历史日志与清空日志 |
| 网络设置 | 管理 Mixed / HTTP / SOCKS / Redir / TProxy 端口、Allow LAN、IPv6 等参数 |
| DNS 与解析 | 分类管理 Mihomo DNS、解析服务器、Fake IP、域名策略、回退过滤与 Hosts 映射；修改后自动备份、校验并应用，失败自动回滚 |
| TUN | 管理 TUN 开关及相关参数，用于系统级透明流量接管 |
| 环境变量 | 管理 `/etc/environment`、`/etc/profile`、`/etc/bash.bashrc` 中的代理环境变量 |
| Core 管理 | 自动检测本机 Mihomo、支持内置或按架构下载 Core、在线检查/更新、备份与失败回滚 |
| GEO 数据 | 安装包内置 `Country.mmdb`、`geoip.dat`、`geosite.dat`，托管模式支持在线更新 |
| 软件图标 | 支持多套 fnOS 桌面/窗口图标切换 |
| 应用更新 | 支持接入 GitHub Releases 进行版本检测，FPK 升级仍由 fnOS 应用中心负责 |

---

## Mihomo 工作模式

### Manager 托管模式

当系统没有检测到可用的 Mihomo 时：

1. 检测当前 Linux CPU 架构。
2. 架构专用包选择内置 Core；`all` 通用包从官方 GitHub Release 选择对应资产。
3. 校验 Core 的文件大小和 SHA-256。
4. 将 Core 安装到 Clash for fnos 自己的数据目录。
5. 准备托管配置和 GEO 数据。
6. 启动 Mihomo，并自动读取 Controller、Secret、Mixed Port 等运行参数。

架构专用包首次启用不依赖在线下载 Mihomo；`all` 通用包体积更小，但首次启用必须能访问 GitHub。

### External 模式

如果系统已经存在 Mihomo，Clash for fnos 会优先使用已有 Core：

- 不自动覆盖用户已有 Mihomo。
- 不自动安装第二份 Core。
- 自动尝试识别 Mihomo 进程、配置文件、Controller 与 Secret。
- 继续提供节点、规则、连接、日志等可视化管理能力。

External 模式下，用户原有的 Mihomo 配置仍由用户自行维护；涉及系统配置写入的操作会尽量先备份再应用。

---

## 支持平台

| fnOS 设备架构 | Release 文件 | Core 获取方式 |
| --- | --- | --- |
| Intel / AMD x86_64 | `Clash for fnos_<version>_x86_64.fpk` | 内置 `linux/amd64` |
| ARM64 / aarch64 | `Clash for fnos_<version>_arm64.fpk` | 内置 `linux/arm64` |
| fnOS x86 / ARM 通用 | `Clash for fnos_<version>_all.fpk` | 不内置；运行时检测并下载 |

运行依赖：

- fnOS
- Node.js v22（FPK 已通过 `install_dep_apps` 声明依赖）
- 管理员权限用于安装应用

> [!TIP]
> 不确定设备架构时可以选择 `all` 通用包。已知是 `x86_64` 或 `aarch64` / `arm64` 且希望首次启动不依赖 GitHub 时，优先选择对应架构专用包。

---

## 安装

### 从 GitHub Releases 安装

1. 打开项目的 [Releases](https://github.com/chenpingonline/Clash-for-fnos/releases)。
2. 根据 NAS CPU 架构下载专用 `.fpk`，或下载不含 Core 的 `all` 通用包。
3. 进入 **fnOS → 应用中心 → 手动安装**。
4. 选择下载好的 FPK 并完成安装。
5. 从 fnOS 桌面打开 **Clash for fnos**。

升级已有版本时，可以直接使用 fnOS 的手动安装功能安装新版 FPK。

### SHA-256 校验

每个 Release 建议同时提供：

```text
Clash for fnos_<version>_x86_64.fpk
Clash for fnos_<version>_x86_64.fpk.sha256
Clash for fnos_<version>_arm64.fpk
Clash for fnos_<version>_arm64.fpk.sha256
Clash for fnos_<version>_all.fpk
Clash for fnos_<version>_all.fpk.sha256
```

Linux 下可以校验：

```bash
sha256sum -c "Clash for fnos_<version>_x86_64.fpk.sha256"
```

---

## 项目结构

本项目采用 **单仓库、多架构构建**，前端、后端、fnOS 配置和生命周期脚本完全共用，只将 Mihomo Core 按架构存放。

```text
Clash-for-fnos/
├── fpk/
│   ├── app/
│   │   ├── core/                  # 构建时写入当前架构的 Mihomo Core 元数据/资产
│   │   ├── geodata/               # Country.mmdb / geoip.dat / geosite.dat
│   │   ├── server/                # Node.js 后端、Privileged Helper、Web 前端
│   │   └── ui/                    # fnOS 桌面入口与图标
│   ├── cmd/                       # fnOS 生命周期脚本
│   ├── config/                    # fnOS privilege / resource 配置
│   ├── wizard/                    # fnOS 安装/配置向导资源
│   ├── ICON.PNG
│   ├── ICON_256.PNG
│   └── manifest
├── resources/
│   └── core/
│       ├── x86/                   # linux/amd64 Mihomo
│       └── arm/                   # linux/arm64 Mihomo
├── scripts/
│   ├── build-manual.sh            # 通用构建脚本
│   ├── build-x86.sh               # x86 快捷构建
│   ├── build-arm.sh               # ARM 快捷构建
│   └── build-all.sh               # 不含 Core 的通用包快捷构建
├── LICENSE
└── README.md
```

---

## 从源码构建

### 构建环境

当前构建脚本面向 Linux Shell 环境，推荐：

- Linux
- WSL2
- GitHub Actions Linux Runner

需要以下基础命令：

```text
bash
cp
tar
gzip
sed
awk
mktemp
md5sum
sha256sum
```

构建 FPK 本身不需要执行 `npm install`；Node.js v22 是 **FPK 在 fnOS 上运行时的依赖**。

### 统一版本号

应用版本只修改 `fpk/manifest` 中的 `version`（格式为 `主版本.次版本.补丁版本`）。

- 所有打包入口会自动同步暂存目录中的 npm 版本和页面资源缓存版本；FPK 文件名也读取 manifest。
- 后端健康检查、更新检查和版本显示读取同步后的 `package.json`，不再维护硬编码版本。
- `npm run check` 会自动同步源码中的派生文件；也可单独运行 `./scripts/sync-version.sh`。
- `package.json`、`package-lock.json` 的应用版本与页面 `?v=` 均为自动生成值，无需手动修改。Mihomo Core 和第三方依赖版本独立管理。

打包不会自动递增版本，也不会改写源码中的派生文件。

### 开发检查

服务端的类型检查和单元测试属于开发依赖，不会打入 FPK。首次运行前执行：

```bash
cd fpk/app/server
npm ci
npm run check
```

`npm run check` 会依次执行 TypeScript 的 `checkJs` 静态检查和 Node.js 内置测试。生产代码仍由 Node.js 22 直接运行，不需要在 fnOS 上安装 npm 依赖。

### 获取源码

```bash
git clone https://github.com/chenpingonline/Clash-for-fnos.git
cd Clash-for-fnos
chmod +x scripts/*.sh
```

### 构建 x86

```bash
./scripts/build-manual.sh x86
```

等价快捷命令：

```bash
./scripts/build-x86.sh
```

输出：

```text
dist/Clash for fnos_<version>_x86_64.fpk
dist/Clash for fnos_<version>_x86_64.fpk.sha256
```

### 构建 ARM64

```bash
./scripts/build-manual.sh arm
```

等价快捷命令：

```bash
./scripts/build-arm.sh
```

输出：

```text
dist/Clash for fnos_<version>_arm64.fpk
dist/Clash for fnos_<version>_arm64.fpk.sha256
```

### 构建 all 通用包

```bash
./scripts/build-all.sh
```

等价命令：

```bash
./scripts/build-manual.sh all
```

输出：

```text
dist/Clash for fnos_<version>_all.fpk
dist/Clash for fnos_<version>_all.fpk.sha256
```

该 FPK 的 manifest 使用 `platform=all`，包内不包含任何 `mihomo-linux-*.gz`。fnOS 官方定义的 `all` 同时支持 x86 与 ARM，并要求应用包不携带架构相关二进制。

---

## 构建流程

`build-manual.sh` 不会直接修改仓库中的公共源码，而是在临时目录中完成架构差异处理。

构建流程如下：

```text
fpk/ 公共源码
      │
      ├── 复制到临时 Stage
      │
      ├── x86 / arm：选择 resources/core/<arch>/ 并写入 Core
      │
      ├── all：移除所有 Mihomo Core，只写入在线获取标记
      │
      ├── 修改临时 manifest 的 platform
      │
      ├── 将 app/ 内容打包为 app.tgz
      │
      ├── 计算 app.tgz MD5 并写入 manifest checksum
      │
      ├── 生成 fnOS FPK
      │
      └── 生成 FPK SHA-256
```

因此 x86、ARM 与 all 不需要维护多套项目源码。

## all 通用包的 Core 下载来源

实现参考 [Clash Verge Rev 的 Core 更新代码](https://github.com/clash-verge-rev/clash-verge-rev/blob/dev/src-tauri/src/feat/core_upgrade.rs)：稳定版 Mihomo 从 [MetaCubeX/mihomo Releases](https://github.com/MetaCubeX/mihomo/releases) 获取。Manager 会读取官方 latest Release，根据 fnOS 运行时的 Linux CPU 架构选择资产：

| 运行时架构 | 首选 Release 资产 |
| --- | --- |
| `x86_64` / `amd64` | `mihomo-linux-amd64-v2-<version>.gz` |
| `i386`–`i686` / `x86` | `mihomo-linux-386-<version>.gz` |
| `aarch64` / `arm64` | `mihomo-linux-arm64-<version>.gz` |
| `armv7*` | `mihomo-linux-armv7-<version>.gz` |

运行时不会只凭文件名安装：Helper 会限制下载域名与最大文件大小，从 GitHub Release 元数据取得 SHA-256 digest，校验下载文件，解压后运行 `mihomo -v` 并验证配置，全部通过后才原子替换托管 Core。下载或校验失败会停止安装并在界面显示错误，可恢复网络后重试。

---

## 更新安装包内置 Mihomo Core

架构资源位于：

```text
resources/core/x86/
resources/core/arm/
```

每个架构目录包含：

```text
mihomo-linux-<arch>-<version>.gz
bundled-core.json
EXPECTED_ASSET.txt
THIRD_PARTY_NOTICES.txt
```

更换内置 Core 时，需要同步更新：

1. 官方 Mihomo `.gz` 资产。
2. `bundled-core.json` 中的版本、架构、大小和 SHA-256。
3. `EXPECTED_ASSET.txt` 中的文件名、大小和 SHA-256。
4. `THIRD_PARTY_NOTICES.txt` 中的版本与对应上游信息。

然后重新分别执行 x86 / ARM 构建。

> [!WARNING]
> 不要只替换 `.gz` 文件而不更新校验元数据。Manager 在启用安装包内 Core 前会校验资产，元数据不匹配会拒绝安装。

---

## GEO 数据

FPK 内置以下 GEO 数据作为托管模式首次启动的离线基础资源：

```text
fpk/app/geodata/Country.mmdb
fpk/app/geodata/geoip.dat
fpk/app/geodata/geosite.dat
```

托管模式默认启用 Mihomo GEO 自动更新策略。已有 GEO 文件不会在每次应用启动时被安装包强制覆盖。

External 模式不会主动修改外部 Mihomo 的 GEO 配置。

---

## 配置与数据

应用遵循 fnOS 的应用目录约定，主要使用以下环境变量：

| 环境变量 | 用途 |
| --- | --- |
| `TRIM_APPDEST` | 已安装应用运行文件 |
| `TRIM_PKGETC` | Clash for fnos 配置、订阅元数据、备份等 |
| `TRIM_PKGVAR` | Mihomo 托管 Core、运行日志及运行数据 |

需要系统级权限的操作由独立的 **Privileged Helper** 完成，例如：

- 托管 Mihomo Core 的安装与启动
- 系统配置文件备份/写入
- TUN / 网络相关配置应用
- 系统代理环境变量管理
- fnOS 应用入口图标同步

Web 服务本身无需直接承担所有 root 操作。

---

## 代理环境变量

Clash for fnos 可以管理：

```text
/etc/environment
/etc/profile
/etc/bash.bashrc
```

主要写入：

```text
http_proxy / HTTP_PROXY
https_proxy / HTTPS_PROXY
no_proxy / NO_PROXY
```

可以自动跟随 Mihomo 的 Mixed Port。关闭功能时，只移除 Clash for fnos 自己管理的内容，并保留用户其它系统配置。

> [!NOTE]
> 代理环境变量只对支持这些环境变量的程序有效。Docker 容器、systemd 服务或不读取代理环境变量的软件不一定会自动走代理。需要更完整的系统 TCP/UDP 透明接管时，应使用 TUN。

---

## 常见问题

### 已经安装了 Mihomo，还会再启动一份吗？

正常情况下不会。检测到现有 Mihomo 后会优先进入 External 模式，不自动安装第二份 Core。

### 全新的 fnOS 没有 Mihomo，可以直接使用吗？

可以。对应架构的 FPK 已携带官方 Mihomo Core，首次托管启用时会先进行本地校验，因此不需要先 SSH 安装 Mihomo。

### 为什么 Docker 容器里不能直接使用 `127.0.0.1:<Mixed Port>`？

容器中的 `127.0.0.1` 指向容器自身，不是 fnOS 宿主机。需要根据 Docker 网络模式使用宿主机地址，或自行配置合适的容器网络。

### 环境变量代理和 TUN 有什么区别？

环境变量代理只影响主动读取 `HTTP_PROXY` / `HTTPS_PROXY` 等变量的程序；TUN 用于更透明地接管系统流量，两者适用场景不同。

### 可以直接编辑 Mihomo YAML 吗？

托管模式支持查看、编辑、应用和备份配置。涉及运行配置的写入会尽量经过校验、备份以及失败恢复流程。

### 项目提供节点或订阅吗？

不提供。本项目只负责 Mihomo 管理。

---

## 开发与贡献

欢迎提交 Issue 和 Pull Request。

提交问题时建议同时提供：

- fnOS 版本
- CPU 架构（x86_64 / ARM64）
- Clash for fnos 版本
- Mihomo 版本
- Manager 托管模式 / External 模式
- 相关页面截图或日志

请避免在 Issue 中公开订阅 URL、Secret、密码等敏感信息。

---

## 致谢

本项目使用或参考了以下开源项目与资料：

- [MetaCubeX/mihomo](https://github.com/MetaCubeX/mihomo) — Mihomo Core
- [MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat) — GEO 数据
- [Clash Verge Rev](https://github.com/clash-verge-rev/clash-verge-rev) — Clash/Mihomo GUI 产品设计与交互参考
- [fnOS 开发者文档](https://developer.fnnas.com/) — fnOS FPK、应用入口与运行环境规范

感谢所有上游项目的维护者与贡献者。

---

## License

Clash for fnos 项目源码使用 [MIT License](LICENSE)。

安装包内包含的第三方组件继续遵循各自许可证：

- Mihomo Core：GPL-3.0-or-later
- 对应版本、资产来源和许可证信息见 `resources/core/<arch>/THIRD_PARTY_NOTICES.txt`

第三方组件的许可证不会因本项目使用 MIT License 而发生改变。

---

<div align="center">

如果这个项目对你有帮助，欢迎 Star ⭐

</div>
