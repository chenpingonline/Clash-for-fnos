# 服务端结构

Clash for fnos 保持 Node.js 22 零运行时依赖，同时将高风险逻辑从进程入口中分离。

```text
web/src/*.vue                     Vue 3 + TypeScript 前端（Vite 构建）
  -> server.js                    普通应用用户，Web API 与 Mihomo Controller
       -> lib/*                   可复用、可静态检查的纯逻辑
       -> privileged Unix Socket
            -> privileged-api.js root API 白名单与输入契约
                 -> privileged-helper.js
                                      Core、配置事务与系统操作
```

## 边界约束

- `server.js` 不直接写系统文件，也不直接启动 root 进程。
- `privileged-api.js` 是 root Helper 的唯一 HTTP 路由入口。新增高权限操作必须在这里显式注册并校验输入。
- `lib/` 中的纯逻辑启用 TypeScript `checkJs` 严格检查，并由 `node:test` 覆盖。
- `web/` 保存 Vue 单文件组件、类型与前端纯逻辑；构建产物写入 FPK 的 `server/public/`。
- FPK 构建会排除 `node_modules`、测试和类型检查配置，保持现有零运行时依赖部署方式。

## 开发命令

```bash
cd ../../../web && npm ci
cd ../fpk/app/server && npm ci && npm run check
```

涉及 Core、配置事务或系统环境变量的变更，应同时补充对应的纯逻辑测试，并在 fnOS 真机验证权限、进程启停和失败回滚。
