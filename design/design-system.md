# Control Panel 设计语言（design/design-system.md）

> 运营控制台前端的**视觉单一事实源**：配色 token 的语义边界、基元与变体选用规则、
> 页面模板、交互状态、反馈模式、间距与排版节奏、已知缺口。人与 AI agent 共用。
>
> **边界**：本文件只管"何时用哪个 token / 变体 / 长什么样"。技术栈与架构见
> [design/frontend.md](frontend.md) §2/§11；业务流程、空状态/校验错误的**内容**（显示
> 什么文案、错误类型映射）见 [design/domain-flows.md](domain-flows.md) §1/§3/§4。本文件
> 不重复业务内容，不引入前端值级校验规则（domain-flows.md §4 末 L129）。

## 1. 设计原则

- **科技蓝白、light-only**：配色源自 `globals.css` 的 token，组件只用 token，不写死
  `gray-300`/`bg-black`/hex。换肤改 `globals.css` 一处。无 dark mode（蓝白 inherently light）。
- **token 先于组件，组件先于页面**：能复用基元就复用，能套模板就套，新写前先查 §3/§4。
- **Sentry 风格开发者控制台**：浅色侧栏、克制 hairline、muted 表头、hover 行、蓝色单一强调。
- **视觉密度中等**：内部运营工具，重信息密度，但留白要够（页面 p-8、列表 gap-6）。

## 2. Token 语义边界

token 定义在 [`web/src/app/globals.css`](../web/src/app/globals.css) L16-37，经 `@theme inline`
（L39-55）映射成 Tailwind 工具类（`bg-primary` / `text-muted-foreground` / `border-border` 等）。

| 类别 | token（工具类） | 值 | 用途 / 正例 | 反例 / 禁止 |
|---|---|---|---|---|
| Surface | `bg-background` | `#ffffff` | 主区底、卡片底（`layout.tsx:73`、`providers-table.tsx:62`） | 不要给主区加 `bg-white`（重复） |
| Surface | `bg-muted` | `#f7f9fc` | 侧栏底、表头底、登录页底（`layout.tsx:32`、`providers-table.tsx:66`、`login/page.tsx:16`） | 不要用于卡片内容区 |
| Text | `text-foreground` | `#0f172a` | 主文字、单元格内容（`providers-table.tsx:96`） | 不要给正文用 `text-black` |
| Text | `text-muted-foreground` | `#64748b` | 副标、表头、空状态、placeholder（`providers/page.tsx:45`、`providers-table.tsx:70,84`） | 不要用于主操作文字 |
| Line | `border-border` / `border-input` | `#e6eaf2` | 所有 hairline 分隔（侧栏右边、表格边、表单边） | 不要用 `border-gray-300` |
| Brand | `bg-primary` / `text-primary` | `#2456f6` | 主操作按钮、active nav 文字、品牌标记、focus ring | 不要用于次要操作 |
| Brand | `bg-accent` / `text-accent-foreground` | `#eef3ff` / `#2456f6` | hover 底（nav、表格行）、active nav 底 | 行 hover 用 `bg-accent/50`（半透明） |
| Feedback | `text-destructive` / `bg-destructive` | `#dc2626` | 删除按钮、错误提示、必填星号可借用 | 唯一的"危险/错误"色，勿作装饰 |

**已知缺口**：无 `success`/`warning`/`info` 语义色（见 §8）；无 spacing/radius/typography scale
token（当前散落在组件类名，见 §7）。

## 3. 基元与变体选用规则

基元在 [`web/src/components/ui.tsx`](../web/src/components/ui.tsx)（非 shadcn，token 驱动的轻封装）。

### Button（`ui.tsx:35-53`）

| 变体 | 何时用 | 现状正例 | 禁止 |
|---|---|---|---|
| `primary` | 每个表单/区域**唯一**的主操作（创建、提交、登录） | `create-form.tsx:39`、`login/page.tsx:69` | 一页不要多个 primary 抢焦点 |
| `outline` | 次要操作（分页、取消、次要筛选） | `providers-table.tsx:117`（Next page） | 不要用于删除 |
| `destructive` | 删除、撤销、不可逆危险操作 | **目标态**（见下偏离） | 不要用于普通确认 |
| `ghost` | 导航/工具栏里的次要文字按钮、无边界工具操作 | 登出按钮可考虑（现状 `layout.tsx:64-69` 用原生 button） | 不要用于表单提交 |

- 尺寸：`sm`（h-8）= 表格内/分页/紧凑工具栏；`md`（h-9）= 默认、表单提交。
- `href` 传参自动渲染 `<Link>`（导航用 primary/ghost outline 链接）。
- base 含统一 focus ring + disabled 态（`ui.tsx:17-18`），不要再自定义 outline。
- **表格操作列**:`size="sm"` 的多按钮必须包在 `<div className="flex items-center justify-end gap-1">` 里,避免在收缩列里换行(详见 §4.2「操作列容器」)。

### Input（`ui.tsx:56-81`）

- label 在上、input 居中、error 在下（红字 `text-xs text-destructive`）。
- 必填用 label 文本加 `*`（如 `label="Name *"`，`create-form.tsx:34`），不引前端值级校验
  （domain-flows.md §4 末 L129）。
- h-9，focus 用 `ring-2 ring-ring`，不要自定义 border-color。

### Card（`ui.tsx:84-98`）

- 白底 + `border-border` + `rounded-lg`。无 header/footer 分区（缺口，见 §8）。
- 用于表单容器（`create-form.tsx:28` 用 `p-4`）、详情卡（未来）。

### NavLink（`web/src/components/nav-link.tsx`）

- client 组件（需 `usePathname`）。active = `bg-accent text-accent-foreground`；
  inactive = `text-muted-foreground hover:bg-accent`。
- 仅在侧边栏用。新资源页上线时，在 `(dashboard)/layout.tsx:60` 加一行 `<NavLink href="/xxx">`。

### Modal（`web/src/components/modal.tsx`）

"use client" 组件（需 `useEffect` 控制 body scroll lock）。受控模式（`open`/`onClose`）。

- `Modal`：通用弹层。`size` = sm/md/lg（`max-w-sm`/`md`/`lg`），`title` 渲染标题栏，`children` 入内容区（`max-h-[60vh] overflow-y-auto`），可选 `footer` 渲染底部操作栏。遮罩 `bg-black/50` + Esc 关闭 + 遮罩点击关闭 + `role="dialog"` + `aria-modal="true"` + body scroll lock。
- `ConfirmModal`：基于 Modal 的薄封装。固定 `size="sm"`，`title` + `message` + destructive `onConfirm` 按钮 + outline `onCancel` 按钮。支持 `loading`（按钮文案变"Deleting…"）和 `error`（底部红块）。

> **替代原生 `confirm()`**：所有删除确认统一走 `ConfirmModal`。providers 页的 `confirm()` 和 `alert()`（`providers-table.tsx:47,50`）是待迁移的遗留实现。

**缺失基元**（见 §8）：Badge/Tag、Toast、Select、Textarea、Table 壳、EmptyState、
Skeleton/Spinner。需要时先查 §8 优先级，再决定自造 vs 引 shadcn。

## 4. 页面模板

每个模板配最小 JSX 骨架，铺新页时**复制骨架再改字段**，比反向工程现有页可靠。

### 4.1 列表页（RSC）— 参考 `providers/page.tsx:42-51`

```tsx
export default async function XxxPage({ searchParams }: { searchParams: Promise<{ cursor?: string }> }) {
  const { cursor } = await searchParams;
  let rows = []; let nextCursor = "";
  try {
    const client = await serverAdminClient();
    const page = unwrap(await client.GET("/api/v1/xxx", { params: { query: cursor ? { cursor } : {} } }));
    rows = page.data ?? []; nextCursor = page.next_cursor ?? "";
  } catch (err) { await onAuthExpired(err); }
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-8">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Xxx</h1>
        <p className="mt-1 text-sm text-muted-foreground">一句话描述这个资源。</p>
      </div>
      <CreateXxxForm />
      <XxxTable rows={rows} nextCursor={nextCursor} />
    </div>
  );
}
```

- 容器固定 `mx-auto max-w-5xl flex flex-col gap-6 p-8`；标题 `text-xl font-semibold` + 副标
  `mt-1 text-sm text-muted-foreground`。
- `export const dynamic = "force-dynamic"`（读 cookie/searchParams）。

### 4.2 表格（client）— 参考 `providers-table.tsx:61-122`

```tsx
<div className="overflow-hidden rounded-lg border border-border bg-background">
  <table className="w-full border-collapse text-sm">
    <thead>
      <tr className="border-b border-border bg-muted text-left">
        <th className="px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Name</th>
        {/* 更多列 */}
        <th className="px-4 py-2.5" />
      </tr>
    </thead>
    <tbody>
      {rows.length === 0 ? (
        <tr><td colSpan={N} className="px-4 py-10 text-center text-muted-foreground">No xxx yet.</td></tr>
      ) : rows.map(row => (
        <tr key={row.id} className="border-b border-border last:border-b-0 transition-colors hover:bg-accent/50">
          <td className="px-4 py-2.5 text-foreground">{row.name}</td>
          <td className="px-4 py-2.5 text-right">
            {/* 操作列:多个按钮必须用 flex 容器包裹,见下方 bullet */}
            <div className="flex items-center justify-end gap-1">
              <Button variant="outline" size="sm" onClick={() => onEdit?.(row)}>Edit</Button>
              <Button variant="destructive" size="sm" onClick={() => onDelete(row.name)}>Delete</Button>
            </div>
          </td>
        </tr>
      ))}
    </tbody>
  </table>
  {nextCursor && (
    <div className="flex justify-end border-t border-border px-4 py-3">
      <Button variant="outline" size="sm" onClick={goNext}>Next page</Button>
    </div>
  )}
</div>
```

- 表头 `bg-muted` + `text-xs uppercase tracking-wide text-muted-foreground`。
- 行 `hover:bg-accent/50`（半透明，比 nav 的实心更克制）。
- 删除按钮**统一用 `<Button variant="destructive" size="sm">`**。
- **操作列容器**:当一行有 ≥2 个按钮(编辑/删除、查看/编辑/删除等)时,**必须用 `<div className="flex items-center justify-end gap-1">` 包裹**,按钮之间用 `gap-1` 统一间距,不要用 `mr-1`/`mr-2` 手工打点。理由:操作列是收缩列(`<th className="w-0" />`),Button 的 `inline-flex` 在窄列里缺少 flex 容器时会被挤换行、变成上下堆叠;flex 容器默认 `nowrap` 保证稳定水平排列。正例:`providers-table.tsx:122-142`、`plugins-table.tsx`、`routes-table.tsx`。
- 空状态现状是纯文字（`providers-table.tsx:82-87`）；视觉模板（图标+CTA）见 §6 / §8。

### 4.3 表单（client）— 参考 `create-form.tsx:27-52`

```tsx
<Card className="p-4">
  <form ref={formRef} action={formAction} className="flex flex-wrap items-end gap-3">
    <Input name="name" label="Name *" required />
    {/* 更多字段 */}
    <Button type="submit" disabled={pending}>{pending ? "Creating…" : "Create xxx"}</Button>
    {state && !state.ok && (
      <p role="alert" className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{state.error}</p>
    )}
  </form>
</Card>
```

- 短表单用 `flex flex-wrap items-end gap-3` 横排；字段多/长表单用单列 `flex flex-col gap-4`（未来长表单分卡分组待定，§8）。
- 提交按钮 pending 时换文案 `Creating…/Signing in…`。
- **错误展示统一**：表单内联用 `bg-destructive/10 px-3 py-2 text-sm text-destructive` 圆角块
  （同 `login/page.tsx:64`），**不要**用纯红字行（`create-form.tsx:45` 现状待统一，见 §6/§8）。

### 4.4 登录页 — 参考 `login/page.tsx`

`min-h-screen` 居中 + `bg-muted`；`max-w-sm` 卡片；品牌头（`bg-primary` 圆角方块 + 标题）。
非登录页不要复用此布局。

### 4.5 侧边栏 — 参考 `layout.tsx:32-72`

`w-60 bg-muted border-r`；品牌标记 `bg-primary rounded-md`；NavLink 列；底部登出。新资源页
只需在 `layout.tsx:60` 加 `<NavLink>`，不改结构。

### 4.6 详情页

暂无实现。待首个详情页（如 provider 详情）落地时，在此补骨架。预计：`max-w-4xl` + `Card` 分区 +
`<Button variant="outline">` 返回列表。

## 5. 交互状态

| 状态 | 统一表达 | 来源 |
|---|---|---|
| hover（nav） | `hover:bg-accent hover:text-accent-foreground` | `nav-link.tsx:29` |
| hover（表格行） | `hover:bg-accent/50`（半透明，比 nav 克制） | `providers-table.tsx:93` |
| hover（primary 按钮） | `hover:bg-primary/90` | `ui.tsx:21` |
| hover（outline/ghost） | `hover:bg-accent hover:text-accent-foreground` | `ui.tsx:22,26` |
| focus（所有可交互） | `focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1` | `ui.tsx:18` |
| disabled | `disabled:opacity-50 disabled:pointer-events-none` | `ui.tsx:18` |
| loading（按钮） | `disabled` + 文案变 `…ing`（如 `Creating…`） | `create-form.tsx:40`、`login/page.tsx:70` |
| loading（页面/列表） | **缺口**：无 skeleton/spinner，RSC 无客户端 loading 态 | §8 |

**规则**：不要自定义 `:focus { outline: ... }`；统一用 ring。loading 态目前只到按钮级，页面级
skeleton 待补（§8）。

## 6. 反馈模式

> 内容文案（显示什么）见 domain-flows.md；本节只规定**长什么样**。

### 空状态
- 视觉模板（目标态）：图标（`text-muted-foreground`）+ 标题（`text-sm font-medium`）+ 副文
 （`text-xs text-muted-foreground`）+ CTA（`<Button variant="outline" size="sm">`）。
- 文案与 CTA 目标：严格遵循 domain-flows.md §3（L104-114）的"0 数据状态/操作引导"表，CTA
  链接目标遵循 §1 onboarding 依赖链（L11-33，如 model 列表空 → 链到 providers 页）。
- 现状：`providers-table.tsx:82-87` 仅纯文字 `No providers yet.`，无图标/CTA（§8 待补）。

### 错误

当前可落地规则：

| 场景 | 视觉 | 用法 |
|---|---|---|
| 表单内联错误（400 缺字段、429 锁定、唯一冲突） | `bg-destructive/10 rounded-md px-3 py-2 text-sm text-destructive` | 表单提交后，`state && !state.ok` 时渲染在提交按钮下方，内容取 `{state.error}`。正例 `login/page.tsx:64` |
| 401 认证过期 | 不走 UI，直接清 cookie + redirect `/login` | `onAuthExpired`（`errors.ts`）指向 `/logout` Route Handler |

```tsx
{/* 表单内联错误 —— 复制此片段，替换 state 来源即可 */}
{state && !state.ok && (
  <p role="alert" className="w-full rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
    {state.error}
  </p>
)}
```

**不引入**：前端值级校验（domain-flows.md §4 末 L129）。错误类型到展示位置的映射见
domain-flows.md §4（L118-127）。

**当前缺口**（均入 §8）：
- `create-form.tsx:45` 仍用纯红字行，待统一为红块。
- 行级/瞬时错误（`providers-table.tsx:50` 的 `alert()`）目标为 Toast。
- 409 引用冲突目标为 Modal；403 目标为 Toast 或局部"无权限"。

### 成功
- 现状：静默 `router.refresh()`（`create-form.tsx:23`）。无 Toast（§8 待补）。
- 目标：写操作成功可加轻量 Toast（`bg-accent text-accent-foreground`），但不强求——静默刷新
  对内部工具可接受。

### 确认
- 现状：原生 `confirm()`（`providers-table.tsx:47`）。
- 目标：统一 Modal（§8 待补）。在此之前，新页面沿用 `confirm()` 保持一致，不要混用。

## 7. 间距与排版节奏

当前尺度（散落在组件类名，未成 token）：

| 场景 | 尺度 | 来源 |
|---|---|---|
| 页面外边距 | `p-8` | `providers/page.tsx:42` |
| 列表垂直间距 | `gap-6` | `providers/page.tsx:42` |
| 表单卡内边距 | `p-4` | `create-form.tsx:28` |
| 登录卡内边距 | `p-6` | `login/page.tsx:45` |
| 表头/单元格 | `px-4 py-2.5` | `providers-table.tsx:70,96` |
| 空状态 | `px-4 py-10` | `providers-table.tsx:84` |
| 分页区 | `px-4 py-3` | `providers-table.tsx:116` |

排版层级：

| 层级 | 样式 | 用途 |
|---|---|---|
| 页标题 h1 | `text-xl font-semibold text-foreground` | 每个资源列表页顶部（`providers/page.tsx:44`） |
| 副标 | `mt-1 text-sm text-muted-foreground` | 页标题下一行描述 |
| 侧栏品牌 | `text-sm font-semibold` | `layout.tsx:50` |
| 侧栏品牌副 | `text-[11px] text-muted-foreground` | `layout.tsx:53` |
| 表头 | `text-xs font-semibold uppercase tracking-wide text-muted-foreground` | `providers-table.tsx:70` |
| 正文/单元格 | `text-sm text-foreground` | `providers-table.tsx:96` |
| h2 / h3 / 空状态标题 | **未定**（缺口，§8） | 待详情页/分区落地时补 |

**规则**：新页面沿用上表尺度，不要引入 `p-7`/`gap-4` 等未出现的中间值。spacing/radius/typography
scale 未成 token（§8），当前以"复用现有类名"为准。

## 8. 已知缺口与后续待办

按"缺失 token / 缺失基元 / 不一致待统一"三类组织（跨页复用，不按资源页登记）。每项标优先级
（P0=铺相关页前必须 / P1=显著改善体验 / P2=锦上添花）+ 触发条件。

### 缺失 token
| 项 | 优先级 | 触发条件 |
|---|---|---|
| `success`/`warning`/`info` 语义色 | ✅ 已实现（`globals.css`，P5 切片交付） | usage 趋势、quota 余额正/负、operator 状态需要 |
| spacing scale（`--space-*`） | P2 | 当前复用现有类名够用；长表单分组时再考虑 |
| radius scale（`--radius-*`） | P2 | 当前 `rounded-md`/`rounded-lg` 够用 |
| typography scale（`--text-*`） | P2 | h2/h3 落地时再考虑 |

### 缺失基元
| 项 | 优先级 | 触发条件 |
|---|---|---|
| Badge/Tag | ✅ 已实现（`web/src/components/ui/badge.tsx`） | operator role（super/tenant-admin）、api-key active/revoked、provider type |
| EmptyState | ✅ 已实现（`web/src/components/ui/empty-state.tsx`） | 空状态视觉模板（图标+标题+CTA）统一 |
| Toast | ✅ 已实现（sonner，`web/src/components/ui/toaster.tsx` + `web/src/lib/toast.ts`） | 行级错误、成功反馈、403 统一 |
| Dialog/Modal | ✅ 已实现（`web/src/components/modal.tsx`） | 通用弹层 + ConfirmModal，providers 页待迁移 confirm() |
| Skeleton/Spinner | ✅ 已实现（`web/src/components/ui/skeleton.tsx`）+ 路由级 `loading.tsx`（usage/audit/request-logs） | usage/audit 等重查询页的加载态 |
| Select | ✅ 已实现（`web/src/components/ui/select.tsx`，Command+Popover，非 radix）；已迁移 usage/audit 筛选器 | 剩余 9 处原生 `<select>` 见下方「不一致待统一」 |
| Textarea | P2 | 未来有长文本字段时（`web/src/components/ui/textarea.tsx` 已存在） |
| Table 壳（封装 §4.2） | P2 | 第 3+ 个资源页落地时考虑封装 |

### 不一致待统一
| 项 | 优先级 | 现状 | 目标 |
|---|---|---|---|
| 表单错误展示 | ✅ 已统一 | 所有表单均用 `role="alert"` + `bg-destructive/10` 红块（§6） | — |
| 剩余原生 `<select>` | P2 | 9 处表单 select 已迁 7 处（operators/providers/plugins/routes）；剩 2 处多行动态行：`models/upstream-row.tsx`、`routes/route-provider-row.tsx`（同名 hidden input + DOM 顺序 zip，迁移需提升父表单 state 为数组） | 后续迁移，需重构父表单 state 结构 |
| 删除按钮 | ✅ 已统一 | 6 处删除/revoke 按钮全部为 `<Button variant="destructive" size="sm">` + ConfirmModal 配套 | — |
| 操作列容器 | ✅ 已统一 | 5 张资源表(providers/plugins/routes/operators/models)的操作列多按钮均已用 `<div className="flex items-center justify-end gap-1">` 包裹;规范见 §4.2 | — |
| `confirm()`/`alert()` | ✅ 已清理 | 原生 confirm/alert 已移除，删除走 ConfirmModal，成功反馈走 Toast | — |
| loading 页面态 | ✅ 已实现 | usage/audit/request-logs 已加 `loading.tsx` skeleton；其余页仍是按钮文案 | 后续按需铺用 |
| 空状态视觉 | ✅ 已实现 | `<EmptyState>` 基元已交付 | — |
| groups 删除按钮文案 | P2 | `groups/table.tsx` 用 `tG("actions.delete")`，其余 5 处用 `tCommon("actions.delete")` | 可选统一为 `tCommon` |
| h2/h3/空状态标题层级 | P2 | 未定 | 详情页/分区落地时定 |

**shadcn 现状**：已按 frontend.md §11 `shadcn init` 完成（`components.json` style=base-nova，基于
`@base-ui/react`）。新增基元（Select/Toast/Skeleton）沿用 `web/src/components/ui/` 目录与 `cn()`
约定，不引入 radix；本地 `ui.tsx`（Button/Input/Card）保留不变。
