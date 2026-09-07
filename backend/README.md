# Go 后端迁移

`cmd/clash-for-fnos-web` 是 fnOS 当前对外的主服务。它以应用专用用户运行并监听公开 Unix Socket，现阶段负责：

- fnOS Gateway 路径归一化与入口重定向
- Vue/Vite 静态资源和 SPA fallback
- `GET /api/health`
- Mihomo Controller 直连接口：Provider、连接、规则、代理组、延迟测试、运行配置和实时流量
- 策略组顺序读取、选择持久化，以及 Rule Provider 的 Direct 回退更新
- 首页状态聚合与 Controller 连通性测试
- 通过内部 Unix Socket 转发尚未迁移的 `/api/*`

当前请求链：

```text
fnOS Gateway -> Go gateway -> Node compatibility service -> Root Helper -> Mihomo/system
```

Node 兼容服务只监听 `${TRIM_PKGVAR}/clash-for-fnos-node.sock`，不再直接暴露到 `app.sock`，也不再提供静态资源。

## 剩余迁移计划

按共享状态和事务边界逐步迁移，每个阶段独立测试、提交，不允许 Go 与 Node 同时维护同一份内存状态：

1. **只读状态与日志**：迁移 Mihomo 日志采集、历史查询、清空和实时 SSE；补齐已迁移的首页状态与 Controller 测试。完成后 Node 不再持有长连接 Mihomo 数据流。
2. **设置与运行生命周期**：整体迁移 Manager 设置、Controller 自动发现、启动时选择恢复、启动初始化和关闭清理。该阶段统一接管 `settings.json`、`selected.json`，避免双进程缓存分叉。
3. **配置事务**：迁移原始配置、有效配置、校验、应用、启动配置同步、备份与回滚。高权限文件操作暂继续通过 Root Helper。
4. **订阅与导入任务**：迁移订阅增删改、下载、定时更新、应用任务、任务状态及本地配置扫描/导入；本地导入与订阅共用同一份 Profile 状态，因此在本阶段一并迁移，并复用第 3 阶段配置事务。
5. **系统与更新门面**：迁移系统状态、授权路径、网络设置、系统代理、应用图标、应用更新和 Core 更新的普通用户侧编排；高权限动作仍走 Helper Socket。
6. **Go Root Helper**：按配置事务、网络/TUN/DNS、Core 生命周期、系统代理、图标五个子模块替换 `privileged-helper.js`，保持独立 root 进程和显式白名单 API。
7. **移除 Node 运行时**：删除 Node 兼容服务、旧 JS 后端及对应依赖，简化启动/停止脚本，移除 manifest 中的 `nodejs_v22`，完成双架构构建和 fnOS 真机安装、升级、回滚验证。

每阶段完成条件：现有前端 API 路径和 JSON 契约不变；先补 Go 行为测试，再删除对应 Node 路由；通过 Go、Node（迁移期间）、Vue 全量检查；检查无误后形成单独提交。阶段 7 打包时按统一 manifest 版本源先将补丁版本号加一。

当前进度：第 1 阶段已完成，首页状态、Controller 测试、Mihomo 日志采集、历史筛选、清空和实时 SSE 均由 Go 处理。

第 2 阶段已完成：Manager 设置读写、Controller 自动发现和启动时策略组选择恢复已迁入 Go；配置应用后的选择恢复归入第 3 阶段配置事务，暂随事务保留在 Node。

第 3 阶段已完成：配置读取、运行时应用、启动配置同步、备份恢复和失败回滚均由 Go 编排，高权限启动文件仍通过受限 Helper 操作。
