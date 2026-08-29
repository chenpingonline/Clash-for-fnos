# Clash for fnos v0.3.57
- 0.3.57 代码质量清理：不改变业务功能；统一项目版本号，移除未使用代码与冗余初始化，整理本地捕获异常和正则写法，并将前端 `$()` 查询别名更名为 `qs()`，避免 IntelliJ 误判为 jQuery。

- 修复软件图标切换仅应用中心生效的问题：切换时仅精确同步 Clash for fnos 自身在 fnOS `appcenter.app_service` 中注册的桌面入口图标路径；刷新桌面并重新打开窗口后，桌面与窗口标题栏会使用当前选择图标，不触碰其他应用。
- 重新优化 4 套软件图标的 64×64 / 256×256 小尺寸资源，提升主体占比、对比度与锐度，改善应用中心图标发糊。

- 侧边栏移除应用品牌图标/名称/版本块，导航菜单整体上移。
- 更新设置新增 Clash for fnOS 应用自身更新区，与 Mihomo Core 更新分开展示；当前未绑定公开发布源时明确提示通过 fnOS 应用中心或新版 FPK 更新，绑定 GitHub Releases 源后可在线检查版本。

- 设置页分组顺序调整为“网络与端口 → TUN 设置 → 环境变量设置 → 其他设置 → 更新设置”；侧边栏导航文字字重提升，增强可读性。

- 设置页“核心行为”折叠菜单更名为“其他设置”，描述改为“内核参数、策略选择与启动行为”；展开后的内部标题改为“内核行为”，相关功能逻辑保持不变。

- 设置页“高级设置”更名为“环境变量设置”，描述调整为系统登录与 Shell 代理环境变量管理；移除 fnOS 文件夹授权/权限诊断展示，展开后直接进入代理环境变量配置。

- 代理环境变量首次配置默认全部启用：总开关、自动跟随 Mixed Port、`/etc/environment`、`/etc/profile`、`/etc/bash.bashrc` 均默认开启；已存在的用户配置保持原值。
- “当前检测结果”改为单文件整行布局，代理变量值不再省略，宽屏下每个文件内部采用双列变量展示，窄屏自动切为单列。
- 新增“代理环境变量”管理：默认写入 `/etc/environment`，可选 `/etc/profile` 与 `/etc/bash.bashrc`；支持自动跟随 Mixed Port、NO_PROXY、自助关闭/清理和修改前备份。
- `/etc/environment` 使用纯 `KEY=value`，Shell 文件使用独立 `export` 管理块；关闭时只移除 Clash for fnos 管理内容，不覆盖用户其它系统配置。
- 修改 Mixed Port 后会自动同步代理环境变量；关闭 Mixed Port 时，启用“自动跟随”会暂时移除管理块并等待端口重新启用。
- 设置页重新分组：原“系统与文件权限”更名为“高级设置”；原“高级设置”拆分为独立的“TUN 设置”和“更新设置”，保留全部原有功能。

- 配置文件页面改成与日志页一致的单工作区：移除外层卡片/内层 YAML 框嵌套，仅保留一个滚动容器；纵向与横向共用同一滚动区域，底部横向滚动条固定在工作区底部。

- 配置文件页面改为单层滚动：只读 YAML 不再使用内部纵向滚动框，统一随右侧工作区滚动。

- 代理节点亮色 UI 改为更中性的 fnOS 白色面板体系，减少灰蓝嵌套底色。
- 订阅配置页面改为异步分区加载：页面外壳立即显示，本机扫描与配置列表并行后台加载。
- 日志页面移除外层卡片嵌套，直接显示工具栏与日志区域。

- 主题跟随改为优先读取 fnOS `DesktopConfig-1000.userPreference.theme`，实时同步飞牛亮/暗主题。
- 亮色侧边栏固定为 `#F3F3F3`，暗色侧边栏固定为 `#0C0C0C`。
- 修复托管 Mihomo 启动配置在 package 用户下被误判为“无读取权限”。
- 系统/托管 Mihomo 配置通过受限 privileged helper 读取，不扩大 Web 进程权限。
- 配置候选按 realpath 归一化去重，同一 config.yaml 只显示一次。
- 保留 fnOS 原生授权目录隔离与架构专用内置 Mihomo Core。


## Bundled GEO data

The FPK bundles `Country.mmdb`, `geoip.dat`, and `geosite.dat` under `app/geodata/`. On first managed-Core startup, missing files are copied into `${TRIM_PKGETC}/mihomo`, which is also the Mihomo `-d` HomeDir. Existing GEO files are preserved.


## Managed GEO update policy

Managed Mihomo keeps subscription/profile YAML snapshots unchanged, while the effective startup config is normalized to:

```yaml
geodata-mode: true
geo-auto-update: true
geo-update-interval: 24
geox-url:
  geoip: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat"
  geosite: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat"
  mmdb: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb"
  asn: "https://cdn.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/GeoLite2-ASN.mmdb"
```

The three bundled Clash Verge GEO files (`Country.mmdb`, `geoip.dat`, `geosite.dat`) remain the offline first-start fallback. Existing files are not overwritten by package startup; Mihomo refreshes GEO data online according to the policy above. External-mode Mihomo configs are not modified.

## 单仓库多架构构建

本仓库同时维护 fnOS x86 与 ARM 安装包。业务源码、前端、后端、配置和 UI 完全共用，仅 Mihomo Core 二进制及其校验元数据按架构存放：

```text
resources/core/
├── x86/   # linux/amd64 Mihomo
└── arm/   # linux/arm64 Mihomo
```

构建 x86：

```bash
./scripts/build-manual.sh x86
```

构建 ARM：

```bash
./scripts/build-manual.sh arm
```

也可以使用快捷脚本：

```bash
./scripts/build-x86.sh
./scripts/build-arm.sh
```

构建产物输出到 `dist/`。构建脚本会在临时目录中写入对应 `platform`、复制匹配架构的 Mihomo Core，并重新计算 `app.tgz` 的 MD5 checksum；不会修改仓库中的公共源码。
