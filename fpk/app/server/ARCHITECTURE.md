# 服务端结构

Clash for fnos 正在按可回滚的方式将后端从 Node.js 迁移到 Go。Go 已作为 fnOS 对外主服务；尚未迁移的业务 API 通过内部 Unix Socket 交给 Node 兼容层，高权限操作仍由独立 Root Helper 执行。

```text
web/src/*.vue                     Vue 3 + TypeScript 前端（Vite 构建）
  -> Go gateway                   普通应用用户，对外 Unix Socket、静态资源与健康检查
       -> Node compatibility      普通应用用户，尚未迁移的 Web API 与 Mihomo Controller
            -> lib/*              可复用、可静态检查的纯逻辑
            -> privileged Socket
                 -> privileged-api.js root API 白名单与输入契约
                      -> privileged-helper.js
                                           Core、配置事务与系统操作
```

## 边界约束

- Go gateway 是 `app.sock` 的唯一服务端，只把 `/api/*` 代理到内部 Node Socket；Node 兼容层不对 fnOS Gateway 直接暴露。
- Go gateway 与 `server.js` 均以应用专用用户运行，不直接写系统文件，也不直接启动 root 进程。
- `privileged-api.js` 是 root Helper 的唯一 HTTP 路由入口。新增高权限操作必须在这里显式注册并校验输入。
- `lib/` 中的纯逻辑启用 TypeScript `checkJs` 严格检查，并由 `node:test` 覆盖。
- `web/` 保存 Vue 单文件组件、类型与前端纯逻辑；构建产物写入 FPK 的 `server/public/`。
- FPK 构建会交叉编译 Linux x86_64/arm64 Go 二进制；`all` 包同时携带两种架构并在启动时选择。
- 迁移期间 FPK 仍声明 `nodejs_v22`，因为 Node 兼容层和 Root Helper 尚未迁移。完成这两层后才能移除该依赖。

## 开发命令

```bash
cd ../../../web && npm ci
cd ../fpk/app/server && npm ci && npm run check
```

涉及 Core、配置事务或系统环境变量的变更，应同时补充对应的纯逻辑测试，并在 fnOS 真机验证权限、进程启停和失败回滚。
