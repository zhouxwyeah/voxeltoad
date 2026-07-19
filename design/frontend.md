# Control Panel 前端设计（design/frontend.md）

> 管理面（admin API, ADR-0019）的运营 UI。本文件是前端的单一事实来源：技术栈、
> 架构、数据/会话/金额/校验的处理约定,以及首版范围与推进路径。后端 API 契约见
> `docs/openapi/admin.yaml`(权威),鉴权拓扑见 ADR-0020。

## 0. 一句话定位

**内部运营型后台**(管理型,非实时监控大盘)。管理配置、租户、运营账号、API keys、
配额充值,只读用量与审计。可接受分钟级数据延迟。

## 1. 关键认知:不需要契约适配 BFF

"BFF" 是两件被混淆的事,先厘清(ADR-0020 的延伸):

| | 是什么 | 本项目 |
|---|---|---|
| Token-custody | 浏览器不碰 operator token,server 侧持有 | ✅ 需要——**Next server 本身即是**,非额外服务 |
| 契约适配 | 把后端 API 重塑成 UI 友好形态(聚合/改形状) | ❌ **不需要**——admin API 本就为 UI 设计(`{data,next_cursor}` 信封、keyset 游标、`{error:{message,type}}` typed error、角色隔离都在 spec 里) |

**结论**:不搭显式 REST 代理层。App Router 的 **RSC 取数 + Server Actions 写入** 模型下,
浏览器不发 HTTP 到"自己的 BFF",因此"手写 24 个无类型代理 / 契约断裂"这个问题**不发生**,
不是被解决,是不存在。生成的 admin client 仅在 server 侧运行,契约不断裂。

## 2. 技术栈(已锁定)

| 层 | 选型 | 理由 |
|---|---|---|
| 框架 | **Next.js (App Router)** | 自带 server 层 = token-custody BFF;RSC/Server Actions |
| 取数(读) | **React Server Components** | server 侧直接调生成的 admin client,首屏取数 |
| 写操作 | **Server Actions** (`'use server'`) | 类型跨 server↔browser 编译期绑定,无代理层 |
| 交互状态 | **URL searchParams** | 筛选/翻页/租户切换走 URL → 驱动 RSC 重渲染;无客户端数据层 |
| 组件库 | **shadcn/ui**(源码式,RSC 友好) | 非黑盒依赖,内部工具无需自造设计系统 |
| 表格 | **TanStack Table**(headless) | 资源页核心;配 URL 状态天然;排序/筛选/分页 UI 是 client 壳 |
| admin client | `@voxeltoad/gateway-sdk/admin`(生成) | **仅 server 侧**;spec 单一事实源 |
| 会话 | **加密 httpOnly cookie**(iron-session 类) | 后端 token 加密存 cookie;**不引 Redis**(守"只依赖 PG") |

**明确不引入**:客户端数据层(React Query/SWR)、服务端 session 存储(Redis)、
next-auth 框架、手写 Zod 校验、第二套浏览器侧生成类型。

## 3. 数据流

### 读(RSC)
```
Server Component (页面)
  → 从加密 cookie 取 operator token
  → createAdminClient({ baseUrl: ADMIN_URL, token })   // server 侧
  → GET /api/v1/... (Bearer, 内网跳)
  → 直接渲染;筛选/翻页/租户由页面的 searchParams 决定入参
```
交互(改筛选/翻页)= 改 URL → Next 重新渲染该 Server Component → 重新取数。
**无客户端 fetch,无 loading 态管理层。**

### 写(Server Action)
```
client 表单 → Server Action('use server')
  → 取 token → 生成 client POST/DELETE
  → 成功: revalidatePath(受影响列表) 刷新 RSC
  → 失败: 返回 typed error 给表单展示
```

### 边界澄清:什么是 server,什么是 client
- **server**:所有取数、所有 admin client 调用、token 访问。
- **client**(`'use client'`):表格交互壳(排序/筛选输入/分页按钮,通过 URL 状态回传)、
  表单输入控件、Server Action 的调用与结果展示。
- **没有**"实时推送/轮询"——盘点结论(§7)是本后台无真实时需求。

## 4. 会话与鉴权(ADR-0020 落地)

- 登录:表单 → Server Action → 生成 client `POST /auth/login` → 拿 operator token →
  **加密塞进 httpOnly+SameSite+Secure cookie**(iron-session 类),token 不进浏览器 JS。
- 每次 server 取数/写入从 cookie 解出 token,注入生成 client 的 Bearer。
- **权威可撤销层仍是后端 `sessions` 表**(12h TTL);Next cookie 只是"token 保管信封",
  不自建服务端 session 存储。
- 失效处理:后端 session 过期/被踢 → 第一个 admin 调用返回 **401** → Next 捕获 →
  清 cookie + 重定向登录。登出 = 清 cookie(可选:调用后端 session 撤销)。
- Next server↔admin 走 Bearer,是**内网 server-to-server**,Bearer 在此安全。
  → 因此 admin 转为内网服务后 CORS 可移除(ADR-0020,清理留到 UI 稳定)。

## 5. 角色:一套 UI + 角色守卫

后端两角色权限差异大,但做**一套代码**,导航/路由按角色显示与守卫。
**后端 403 是真防线,前端隐藏只为体验**(别把打不开的门画出来)。

| 资源/页面 | super-admin | tenant-admin |
|---|---|---|
| providers/routes/plugins(全局 config) | ✅ 增删列 | ❌ 隐藏(后端 403) |
| models(全局共享，GET 对两角色开放) | ✅ 卡片 + CRUD（菜单在 providers 下） | ✅ 卡片只读(菜单隐藏，URL 直达；后端 403 写) |
| tenants | ✅ 列表/创建/启停(可逆) | ❌ 隐藏 |
| operators | ✅ 列表/创建/删除 | ❌ 隐藏 |
| api-keys | ❌(它无租户) | ✅ 自租户增删列 |
| usage / usage-summary | ✅ 全局,**带租户切换器**(`?tenant=X`) | ✅ 仅自租户,**无切换器** |
| audit | ✅ 全局 | ✅ 仅自租户(含 super-admin 对本租户的操作) |
| quota 充值 | ✅ | ❌ |
| quota 余额读 | ✅ 任意 scope | ✅ 限自租户 scope |

**关键实现点**:同一"用量页"组件在两角色下取数入参不同——super-admin 带 `?tenant=X`,
tenant-admin 不带(后端自动 scope)。组件感知角色决定是否渲染租户切换器。

## 6. 金额处理:集中 money 模块(强制)

后端钱是 **int64 微单位**(MicroPerUnit=1_000_000)+ currency 字段。
**所有金额显示/输入只准走一个集中 money 模块**,禁止散落除/乘:
- `microToDisplay(micro, currency)` → 展示串(整数运算;按 currency 定小数位,如 JPY 无小数)。
- `displayToMicro(input, currency)` → int64(避免 `0.1*1e6` 浮点陷阱,用整数/字符串解析)。
- 覆盖:pricing(`prompt_per_1m`/`completion_per_1m`)、quota `balance`、topup `delta`、
  usage `cost`、summary 聚合。
- 散落处理 = 精度 bug 温床,评审直接打回。

## 7. 取数策略依据:无真实时需求(盘点结论)

- **配置类**:低频写、强一致。RSC 取数 + 写后 `revalidatePath`。无需实时。
- **用量**:`usage_records` 是数据面**异步**写入(AsyncRecorder buffer+flush,秒级滞后)——
  前端轮询再快也追不上异步管道。运营看用量是**分析行为**,手动刷新/可选 30-60s 轮询足矣。
- **审计**:append-only,是**搜索/取证**(筛选+翻页),非自动刷新的 feed。
- **配额余额**:热路径高频变,但运营看的是"充值时刻快照 + 手动刷新",不需盯着跳。
- 若未来要秒级监控大盘 → 走 Prometheus/Grafana,**不是** admin API 轮询(超出其设计目标)。

## 8. 表单校验:类型级 + 后端 400

- 前端用**生成的 TS 类型**保证请求**形状**(编译期)。
- **值级约束**(delta>0、email format、operator 的"tenant-admin 必带 tenant_id /
  super-admin 必不带")**不在前端重写**——靠后端 400 `{error:{message,type}}` 展示,
  避免与 spec 的规则**双写漂移**。
- 代价:值错要一次往返才知。对内部低频后台可接受;换来"校验规则单一事实源在后端"。
- typed error 展示复用生成 client 的 `AdminError`/`unwrap`(sdk 已提供)。

## 9. 首版范围与推进路径

**推进方式**:端到端垂直切片,逐个铺开。**先打通一个完整切片验证整条技术栈**
(登录 → 一个资源列表页 → 一个写操作 → typed error/刷新闭环),再横向复制到其余资源。

### 切片 0(技术栈验证,必须先做)
登录(Server Action + 加密 cookie)→ providers 列表(RSC + TanStack Table + URL 分页)→
创建 provider(Server Action + revalidate)→ 删除 → 401 跳登录。
跑通即证明:会话、RSC 取数、Server Action 写、角色守卫、生成 client server 侧调用、
money 模块(若涉及 pricing)全链路成立。

### 首版范围(后端 API 已就绪,可立刻做)
- 登录 / 布局 / 角色导航守卫
- providers / routes / plugins:列表 + 创建 + 编辑 + 详情 + 删除（2026-07 已补全 routes/plugins，操作集扩展到编辑+详情）
- models:卡片视图 + 搜索过滤 + 角色条件 CRUD（ADR-0044 合并 model-catalog）
- tenants:列表 + 创建 + 启停(可逆开关)
- operators:列表 + 创建 + 删除(super-admin)
- api-keys:列表 + 创建(明文一次性展示)+ 撤销(tenant-admin)
- quota:充值(super-admin)+ 余额读
- usage:列表 + summary 汇总(租户切换器按角色)
- audit:列表 + 筛选(resource_type/action/time)
- money 模块、会话模块、typed-error 展示

### "编辑现有 config" 的 UX 注意
后端支持两种编辑路径：
- **整体重提交（POST upsert）**：预填现有对象值 → 整体重新 POST，适用于大多数场景。
- **部分字段 PATCH**：`PATCH /api/v1/{resource}/{name}`，指针字段语义（`nil` = 不变），已落地（[ADR-0030](../../docs/adr/0030-config-patch-editing.md)，provider 为 pilot 覆盖全部 4 类资源）。

表单按字段级编辑需求选择路径；routes/plugins 编辑已落地。

## 10. 范围外(后端缺口,记录待议)

以下页面**依赖尚未实现的后端**,首版不做,后续讨论再推进(对应 ADR-0019 的 P2):

> 注:原 P2 缺口"config 真编辑(PATCH 部分字段)"已由 [ADR-0030](../../docs/adr/0030-config-patch-editing.md) 实现并完成 rollout(provider 为 pilot,覆盖全部 4 类资源);tenant 停用、API keys 撤销、groups CRUD 亦均已补齐。见 `design/domain-flows.md` 状态注记。

> tenant 停用已补齐:`PATCH /api/v1/tenants/{name} {enabled}`(可逆开关,super-admin only),前端 tenants 页已实现启用/停用按钮(见 `web/src/app/[locale]/(dashboard)/tenants/table.tsx`)。

推进这些前端页面前,需先补对应后端端点(沿用 ADR-0017/0019 模式),届时单独立计划讨论。

## 11. 目录约定（切片 0 已落地）

前端在 `web/`（Next.js App Router，TS + Tailwind + `src/` + `@/*` alias）。
生成的 admin client 从 `@voxeltoad/gateway-sdk/admin` 引入（`file:` 依赖，**仅 server 侧**）。

```
web/
  next.config.ts            # turbopack.root=.. + transpilePackages（file: 子路径解析）
  playwright.config.ts
  .env.example              # ADMIN_URL, SESSION_SECRET(≥32)
  src/
    lib/
      session.ts            # iron-session 加密 cookie（getSession/getToken/setSession/clearSession）；server-only
      admin.ts              # serverAdminClient()（token 注入）/ anonAdminClient()（登录用）；server-only
      errors.ts             # onAuthExpired（401→redirect /logout）/ toFormError；server-only
    components/
      ui.tsx                # 本地 UI 基元（Button/Input/Card，token 驱动，非 shadcn）；server-compatible
      nav-link.tsx          # 'use client' NavLink（usePathname 高亮当前 section）
    app/
      layout.tsx            # 根 layout（metadata）
      page.tsx              # / → redirect /providers
      globals.css           # 设计 tokens（科技蓝白配色）：:root CSS 变量 + @theme inline 映射 Tailwind 工具类
      login/
        page.tsx            # client 登录表单（useActionState）
        actions.ts          # loginAction（POST /auth/login → setSession → /providers）
      logout/
        route.ts            # Route Handler：clearSession + redirect /login（RSC 401 的清 cookie 落点）
      (dashboard)/
        layout.tsx          # 认证守卫（无 token→/login）+ 侧边栏 + 登出；force-dynamic；角色守卫接缝
        actions.ts          # logoutAction
        providers/
          page.tsx          # RSC 列表（读 searchParams cursor/limit）；force-dynamic
          providers-table.tsx  # 'use client' TanStack Table 壳 + 删除 + URL 分页
          create-form.tsx      # 'use client' 创建表单（useActionState + useEffect 重置/刷新）
          actions.ts           # createProvider / deleteProvider（Server Actions + revalidatePath）
  tests/e2e/providers.spec.ts # Playwright：登录→建→列→删→登出守卫 + 401-RSC 回归
```

**落地要点（切片 0 踩过并固化）**：
- **Turbopack + `file:` 依赖 + 子路径 exports**：需 `next.config.ts` 里 `turbopack.root` 上抬到仓库根 + `transpilePackages: ["@voxeltoad/gateway-sdk"]`，否则 `@voxeltoad/gateway-sdk/admin` 解析不到。
- **访问 cookie/env 的页面必须 `export const dynamic = "force-dynamic"`**（`(dashboard)/layout.tsx` 与 `providers/page.tsx`），否则 build 期预渲染会触发运行时依赖。
- **`SESSION_SECRET` 惰性读取**（在 `getSession()` 内，而非模块顶层），避免 build 期 page-data 收集时报错。
- **设计 tokens 而非散落颜色**：`globals.css` 的 `:root` 定义科技蓝白配色（`--primary: #2456f6` 等），经 `@theme inline` 映射成 `bg-primary`/`text-muted-foreground`/`border-border` 等工具类。组件只用 token，不写死 `gray-300`/`bg-black`——换肤只改 `globals.css`。无 dark mode（蓝白是 light-only；原 `prefers-color-scheme` 分支组件没用 token，已删）。
- **shadcn/ui 已按需 init（切片 0 后续）**：`npx shadcn@latest init` 完成，生成 `components.json`/`lib/utils.ts`，并添加 `popover`/`command`/`badge`/`dialog`/`input` 组件（用于 api-keys 页面的模型多选下拉框 `MultiSelect`）。原生 `ui.tsx` 的 `Button`/`Input`/`Card` 保留不变——目录路径不冲突（`@/components/ui.tsx` vs `@/components/ui/popover.tsx` 等）且 import 不冲突（`import { Button } from "@/components/ui"` vs `import { Button } from "@/components/ui/button"`）。新组件使用 shadcn；既有页面无需迁��。
- **RSC 渲染期不能改 cookie**：401 清 cookie 必须走 `/logout` Route Handler（`onAuthExpired` redirect 到它），不能在 RSC 里直接 `clearSession()`（Next 只允许 Server Action / Route Handler 改 cookie）。
- **`/me` 端点缺失**：登录响应仅 `{token}`,前端拿不到 role/email。切片 0 存 token 即可；角色导航守卫是 `(dashboard)/layout.tsx` 里的接缝,待后端补 current-operator 端点后启用。
- **e2e 端口冲突前置检查**：`scripts/web-e2e.sh` 的 `assert_port_free` 在启动每个服务前 curl 探测端口——若已被占用（如用户自己的 `make adminstack` / `next dev` 还在），立即报错退出，绝不静默跑到旧/dev 服务器上（曾因 `next start` EADDRINUSE 静默落到 dev server，dev 模式 `127.0.0.1` 跨域阻断 hydration 导致 onClick 假红）。
- 起法：`make web-e2e`（自动起 adminstack + web + Playwright + 拆栈）；本地开发 `make adminstack` 另起,`cd web && npm run dev`（先 `cp .env.example .env.local`）。

## 12. i18n 约定（前端国际化）

> 在切片 0 结束后作为独立任务铺开。已双语化（en/zh）12 个资源 namespace +
> 9 个错误域子文件（与后端 `internal/apperr/` 一一对应）。
> 后续新增资源页/错误域按此模板铺开，无需返工。

### 技术选型

[next-intl](https://next-intl.dev/) v4，理由：原生 RSC/Server Actions 支持、ICU 模板语法、
TS 类型安全、Next.js 16 + Turbopack 兼容、`getTranslations`(server)/`useTranslations`(client)
统一 API。

已在 `next.config.ts` 添加 `createNextIntlPlugin()`（`next-intl/plugin`）。

### 目录结构

```
web/src/
  i18n/
    routing.ts     # defineRouting + createNavigation（locale-aware Link/redirect）
    request.ts     # getRequestConfig：递归发现并加载所有 namespace JSON
  locales/
    en/            # 英文（默认）
    zh/            # 中文
    <locale>/
      errors/      # 后端错误码文案，按域分文件（auth/tenant/provider/...）
```

每个语言目录下按资源域分文件（`common.json` / `auth.json` / `providers.json` / ...
以及 `errors/<domain>.json`）。`request.ts` 用 `fs.readdirSync` 递归发现
namespace，新增资源页只需新增 `<resource>.json` 文件，**无需**改 `request.ts`。

### Locale JSON 约定

- **嵌套 JSON**（不用 flat key）：next-intl 的 `t("actions.signOut")` 按点号解析为
  `messages.actions.signOut`。
- key 命名：camelCase 层级，如 `form.name.label`、`columns.baseUrl`。
- ICU 变量模板：`"deleteConfirm": "Delete provider \"{name}\"?"`
- 所有 locale JSON 必须 **2 空格缩进多行**（`jq . --indent 2`）；单行 JSON 会让
  Git 整文件重写，破坏并行 worktree 合并。`make check-i18n` 校验 key 对齐但不
  校验格式，提交前自行确认。
- 后端错误码文案单独 `errors/<domain>.json`，与后端 `internal/apperr/` 域文件
  一一对应；`make check-errors` 校验后端每个错误码的 i18n key 都在对应文件中存在。

### 路由与语言检测

- URL 子路径：`/zh/providers`、`/zh/login`。默认英文不显示 `/en`（`localePrefix: "as-needed"`）。
- `middleware.ts` 使用 `next-intl/middleware` 的 `createMiddleware`，检测链：
  cookie 偏好 → Accept-Language header → 默认 `en`。
- 所有页面路由移至 `web/src/app/[locale]/...`。现有路径 `/providers` / `/login` 经
  middleware rewrite 保持兼容，无需改 e2e selector。

### Server Action / Client Component 适配

- Server Component：`import { getTranslations } from "next-intl/server"; const t = await getTranslations("providers");`
- Client Component：`import { useTranslations } from "next-intl"; const t = useTranslations("providers");`
- Server Action error：后端（`internal/apperr`）直接返回 i18n key 作为 error message
  （如 `"errors.tenant.tenantNotFound"`）。`mapBackendError()` 在 `lib/i18n-errors.ts`
  剥掉 `"errors."` 前缀，返回点路径（如 `"tenant.tenantNotFound"`）；客户端用
  `useTranslations("errors")("tenant.tenantNotFound")` 翻译。未知消息（不带
  `errors.` 前缀）原样展示，作为运营兜底。`FormResult.errorKey` 承载该路径。
- `FormResult` 已增加可选 `errorKey` 字段。
- locale-aware redirect：Server Action 中用 `getLocale()` + `redirect({ href, locale })`。

### 已知限制

- `errors` namespace 在客户端渲染时首次 `useTranslations("errors")` 可能返回 key（如
  `emailPasswordRequired`）而非翻译值——NextIntlClientProvider 序列化所有 namespace，但
  首次 render 可能 key 先于 translation 显示。后续可加 `defaultMessage` 或 preload 处理。

### 新资源页 i18n 模板

铺新资源页（如 models）时：
1. 创建 `web/src/locales/en/models.json` 和 `zh/models.json`，按 `$4` 的 JSX 骨架填 key。
2. `request.ts` 会从 `src/locales/en/` 自动发现 namespace（`fs.readdirSync`），无需手动登记。
3. 页面 RSC 用 `getTranslations("models")`；client 组件用 `useTranslations("models")`。
4. 跑 `make check-i18n` 确认 en/zh key 对齐。

### 并行 worktree 切分约定（降冲突）

i18n 文件是并行 worktree 的高频冲突点。约定如下：

- **一个 worktree 只动一个 namespace 文件**。例如做 providers 功能的分支只改
  `providers.json`，不要顺手动 `tenants.json` 或 `common.json`。
- **`common` / `errors` 跨域共享**，最后串行合并。如果多个 worktree 同时需要加 common
  key，合并前先 rebase main 再解一次冲突。
- **所有 locale JSON 必须 2 空格缩进多行**（`jq . --indent 2`）。单行 JSON 会整文件
  重写，Git 无法逐行合并。`make check-i18n` 会校验 key 对齐，但不校验格式，提交前自行
  确认。
- **新增 namespace 零代码改动**：`request.ts` 自动发现，不需要也不应该多人改同一行。

