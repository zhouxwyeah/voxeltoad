# voxeltoad — Control Panel（管理控制台前端）

voxeltoad 的运营后台 UI。基于 Next.js App Router（RSC + Server Actions），用于管理配置、租户、运营账号、API keys、配额充值，以及只读查看用量与审计。

## 技术栈

- **框架**: Next.js 16 (App Router) + React 19 + TypeScript
- **取数**: React Server Components（server 侧直调生成的 admin client）
- **写操作**: Server Actions（`'use server'`）
- **组件库**: shadcn/ui（源码式）+ TanStack Table（客户端表格壳）
- **会话**: 加密 httpOnly cookie（iron-session），operator token 不进浏览器 JS
- **国际化**: next-intl v4（en/zh）

详见 [`design/frontend.md`](../design/frontend.md)（技术架构与数据流）与 [`design/design-system.md`](../design/design-system.md)（视觉规范）。

## 本地开发

```bash
# 首次安装
make sdk-build      # 生成 SDK dist（file: 依赖）
make web-install    # 安装依赖 + Playwright chromium

# 启动开发服务器（:3000 + 热重载，需先在另一终端运行 make adminstack）
make web-dev
```

浏览器打开 `http://localhost:3000`，用 adminstack 凭据登录。

也可一键启动管理面 + 前端：

```bash
make start-stack    # adminstack(:8090) + web-dev(:3000)，Ctrl-C 一起停
```

## 测试

```bash
make web-e2e        # Playwright E2E（自动起 adminstack + web + 拆栈）
```

详见 [`design/e2e.md`](../design/e2e.md)。

## 目录

```
web/src/
  app/[locale]/(dashboard)/  各资源页面（providers/models/routes/plugins/tenants/...）
  i18n/            next-intl 配置 + 自动发现 namespace
  locales/{en,zh}/  按资源域分文件（含 errors/<domain>.json 后端错误码映射）
  lib/             session/admin/errors 工具（server-only）
  components/      UI 基元 + 共享组件
```

后端 API 契约见 [`docs/openapi/admin.yaml`](../docs/openapi/admin.yaml)（单一事实源），TS admin client 从该 spec 生成。
