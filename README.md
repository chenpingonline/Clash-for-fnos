# Clash for fnOS

简体中文 | [English](README.en.md)

<img src="fpk/ICON_256.PNG" alt="Clash for fnOS" width="96" />

飞牛 fnOS 原生 Mihomo 管理应用。此仓库负责 **FPK 安装包、宿主集成与原生发布**；公共 Vue / Go / 多语言源码与 Docker 构建在 [Clash-Manager](https://github.com/chenpingonline/Clash-Manager) 维护。

[下载 FPK](https://github.com/chenpingonline/Clash-for-fnos/releases) · [操作手册](docs/user-guide.md) · [Docker 部署](https://github.com/chenpingonline/Clash-Manager/blob/master/docker/README.md) · [更新日志](CHANGELOG.md)

![界面示例](img.png)

## 功能与兼容

保留节点、订阅、规则、配置编辑、连接、日志、DNS、TUN、Core / GEO 管理，以及 fnOS 系统代理、原生目录授权、四种软件图标和 FPK 更新入口。打开 fnOS 应用直接进入主界面。界面支持简体中文、English 和跟随系统；语言入口仅位于设置页右上角，选择保存在浏览器中。

仓库拆分不改变 appname、Gateway 路径、Unix Socket、数据目录或生命周期脚本。共享程序仍提供 fnOS 能力适配；不复制两套业务实现。

## 项目结构

```text
Clash-for-fnos/
├── fpk/                 # manifest、生命周期、权限、向导、窗口入口与图标
├── upstream.lock        # 公共仓库、版本、完整 commit SHA
├── scripts/             # 获取固定源码、检查、FPK 构建与审计
├── docs/                # fnOS 文档与截图
├── CHANGELOG.md          # fnOS 更新记录
└── dist/                # 构建产物，不提交
```

公共源码在 `Clash-Manager/backend` 和 `Clash-Manager/web` 开发。构建时导出锁定 commit 到临时目录，注入本仓库 manifest 版本、更新日志与图标，编译后装入 FPK。安装后的应用不从 GitHub 拉取程序源码。

## 从源码构建

需要 Bash、Git、Python 3.12+、Go、Node.js / npm（满足公共仓库 web/package.json engines）、tar、gzip、md5sum 和 sha256sum。支持 Linux / WSL / macOS；macOS 需要 GNU 校验命令。首次构建需要 GitHub 和 npm 网络访问。

```bash
./scripts/check.sh
# 生成新的可安装 FPK 前，先递增 fpk/manifest 补丁版本并完成 CHANGELOG。
./scripts/build-manual.sh x86
./scripts/build-manual.sh arm
./scripts/build-manual.sh all
python3 scripts/audit-fpk.py
```

构建按顺序执行，不共享或修改另一仓库的工作目录。`x86` / `arm` 包含对应架构的 Mihomo；`all` 包含两套程序，首次启用时按设备架构在线下载 Core。所有产物和 SHA-256 校验文件位于 dist/。

## 更新公共版本

```bash
./scripts/update-upstream.sh <完整的40位commit-SHA>
./scripts/check.sh
```

脚本读取该提交的 VERSION 并更新 upstream.lock；不会自动跟随 master 或 latest。审查、验证后提交锁文件与 fnOS 变更。公共程序版本与 FPK 包版本独立；包内 build-info.json 记录两者与源码 SHA。

源码 Git 对象缓存在 `.cache/upstream.git`。源码已缓存时可用 `CLASH_OFFLINE=1` 防止 Git 网络获取；npm 依赖仍需已缓存。`CLASH_SHARED_SOURCE=/绝对路径/Clash-Manager` 可以从本地 Git 读取，但 HEAD 必须等于锁定 SHA，且源码必须干净；不会偷偷打包未提交修改。

具体分工见 [仓库维护](docs/repository-maintenance.md)。源码推送、FPK 发布与 Docker Hub 镜像发布独立执行。

## 验证边界与许可证

自动检查和包审计不代表实际 fnOS 安装、升级、宿主主题、TUN 或网络验证。真实设备验证仍需单独完成。

[GPL-3.0](LICENSE)。项目管理 Mihomo，不提供节点或订阅服务。
