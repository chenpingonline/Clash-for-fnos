# Go 后端迁移

`cmd/clash-for-fnos-web` 是 fnOS 当前对外的主服务。它以应用专用用户运行并监听公开 Unix Socket，现阶段负责：

- fnOS Gateway 路径归一化与入口重定向
- Vue/Vite 静态资源和 SPA fallback
- `GET /api/health`
- 通过内部 Unix Socket 转发尚未迁移的 `/api/*`

当前请求链：

```text
fnOS Gateway -> Go gateway -> Node compatibility service -> Root Helper -> Mihomo/system
```

Node 兼容服务只监听 `${TRIM_PKGVAR}/clash-for-fnos-node.sock`，不再直接暴露到 `app.sock`，也不再提供静态资源。

后续迁移顺序：

1. Mihomo Controller 的只读 API 与流式接口。
2. 设置、订阅和配置事务。
3. Core 更新、系统代理和本地配置发现。
4. Root Helper；完成后移除 `nodejs_v22` 安装依赖。

每一阶段都保持现有前端 API 路径和 JSON 契约，并在删除对应 Node 路由前补 Go 行为测试。
