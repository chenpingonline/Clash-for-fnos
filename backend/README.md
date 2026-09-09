# Go 后端架构

`cmd/clash-for-fnos-web` 是 fnOS 当前对外的主服务。它以应用专用用户运行并监听公开 Unix Socket，现阶段负责：

- fnOS Gateway 路径归一化与入口重定向
- Vue/Vite 静态资源和 SPA fallback
- `GET /api/health`
- Mihomo Controller 直连接口：Provider、连接、规则、代理组、延迟测试、运行配置和实时流量
- 策略组顺序读取、选择持久化，以及 Rule Provider 的 Direct 回退更新
- 首页状态聚合与 Controller 连通性测试
- 通过内部 Unix Socket 调用白名单 Go Root Helper

当前请求链：

```text
fnOS Gateway -> Go web service -> Go Root Helper -> Mihomo/system
```

两个 Go 进程通过私有 Unix Socket 通信；Web 服务使用应用专用用户运行，只有白名单 Root Helper 以 root 运行。

## v1.0.0 架构边界

- Go Web 以 fnOS 应用专用用户运行，负责静态资源、业务 API、任务调度、状态聚合和 Mihomo Controller 通信。
- Go Root Helper 以 root 运行，只接受私有 Unix Socket 上的白名单请求，负责 Core、配置事务、TUN/DNS、GEO、系统代理与图标等必要的高权限操作。
- 托管配置写入采用准备、校验、激活、运行状态确认、提交或回滚的事务流程。
- fnOS 停止应用时先结束 Web，再结束 Helper；Helper 负责等待托管 Mihomo 正常退出。启动时 Web 会重新同步 Controller 设置，并对账托管 TUN 运行态。
- 前端、后端、Go 构建版本及 FPK 文件名统一来自 `fpk/manifest`。

## 已完成的迁移阶段

迁移按共享状态和事务边界分为七个阶段，每个阶段均独立测试、提交：

1. **只读状态与日志**：迁移 Mihomo 日志采集、历史查询、清空和实时 SSE；补齐已迁移的首页状态与 Controller 测试。完成后 Node 不再持有长连接 Mihomo 数据流。
2. **设置与运行生命周期**：整体迁移 Manager 设置、Controller 自动发现、启动时选择恢复、启动初始化和关闭清理。该阶段统一接管 `settings.json`、`selected.json`，避免双进程缓存分叉。
3. **配置事务**：迁移原始配置、有效配置、校验、应用、启动配置同步、备份与回滚。高权限文件操作暂继续通过 Root Helper。
4. **订阅与导入任务**：迁移订阅增删改、下载、定时更新、应用任务、任务状态及本地配置扫描/导入；本地导入与订阅共用同一份 Profile 状态，因此在本阶段一并迁移，并复用第 3 阶段配置事务。
5. **系统与更新门面**：迁移系统状态、授权路径、网络设置、系统代理、应用图标、应用更新和 Core 更新的普通用户侧编排；高权限动作仍走 Helper Socket。
6. **Go Root Helper**：按配置事务、网络/TUN/DNS、Core 生命周期、系统代理、图标五个子模块替换 `privileged-helper.js`，保持独立 root 进程和显式白名单 API。
7. **移除 Node 运行时**：删除 Node 兼容服务、旧 JS 后端及对应依赖，简化启动/停止脚本，移除 manifest 中的 `nodejs_v22`，完成双架构构建和 fnOS 真机安装、升级、回滚验证。

每阶段完成条件：现有前端 API 路径和 JSON 契约不变；先补 Go 行为测试，再删除对应旧路由；通过 Go、Vue 与双架构构建检查；检查无误后形成单独提交。阶段 7 打包时按统一 manifest 版本源先将补丁版本号加一。

第 2 阶段已完成：Manager 设置读写、Controller 自动发现和启动时策略组选择恢复已迁入 Go；配置应用后的选择恢复归入第 3 阶段配置事务。

第 3 阶段已完成：配置读取、运行时应用、启动配置同步、备份恢复和失败回滚均由 Go 编排，高权限启动文件仍通过受限 Helper 操作。

第 4 阶段已完成：远程订阅、本地配置、自动更新、异步应用任务、授权目录扫描与导入状态均由 Go 独占维护，并复用 Go 配置事务完成应用。

第 5 阶段已完成：网络与 DNS 设置、系统状态、代理环境变量、授权路径、应用图标、应用更新及 Core 更新的普通用户侧编排均由 Go 提供；系统级修改仍经 Root Helper 白名单执行。

第 6 阶段已完成：独立 Go Root Helper 已覆盖配置事务、Core 启动与更新、网络/TUN/DNS、系统代理和图标白名单接口；内核更新由 Helper 再次核对官方 Release、大小、SHA-256 与可执行版本。

第 7 阶段已完成：Node 兼容服务、旧 JavaScript 后端与 `nodejs_v22` 运行依赖已移除，fnOS 生命周期只启动 Go Web 与 Go Root Helper；发布版本统一由 `fpk/manifest` 提供。
