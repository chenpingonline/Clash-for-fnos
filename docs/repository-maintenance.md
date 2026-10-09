# 仓库维护 / Repository maintenance

公共功能在 Clash-Manager 修改；FPK 集成在 Clash-for-fnos 修改。运行平台适配保留在公共实现中，避免复制订阅、规则或系统事务代码。

1. 在公共仓库创建功能分支，运行 Go / 前端 / Docker 检查，再合并并推送 master。
2. 原生 FPK 需要采用更新时，在 fnOS 仓库运行 `./scripts/update-upstream.sh <完整 commit SHA>`，自动读取公共 VERSION 并更新 upstream.lock。
3. fnOS 运行 `./scripts/check.sh`；需要新安装包时先更新 manifest 和 CHANGELOG，再串行构建 x86 / arm / all。
4. 审查并提交 fnOS 的版本记录和宿主改动，推送 fnOS 仓库；GitHub 源码、FPK 发布和 Docker Hub 镜像发布分别执行。

构建只使用锁定的提交，不跟随 master 或 latest。标签用于展示，完整 SHA 用于确定源码。已安装 FPK 内含编译后的代码，启动不会从 GitHub 下载程序源码。

Shared features live in Clash-Manager; native packaging lives in Clash-for-fnos. The fnOS update-upstream script pins an explicit full commit SHA and reads its VERSION. Validate before adopting it. Build only that pinned source. Git pushes, FPK publication and Docker image publication are separate steps. Installed FPKs contain the compiled program and do not fetch source at startup.
