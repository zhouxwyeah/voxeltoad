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
  `gray-300`/`bg-black`/hex。换肤改 `globals.css` 一处。无 dark mode（蓝白 inherently light）
  ——源码中禁止出现 `dark:` 变体（`make check-ui` 门禁）。
- **token 先于组件，组件先于页面**：能复用基元就复用，能套模板就套，新写前先查 §3/§4。
- **Sentry 风格开发者控制台**：浅色侧栏、克制 hairline、muted 表头、hover 行、蓝色单一强调。
- **视觉密度中等**：内部运营工具，重信息密度，但留白要够（页面 p-8、列表 gap-6）。

### 1.1 设计参照系

品味对齐的锚点产品。做模糊决策时（间距给多少、层级怎么分、状态怎么表达），
先看它们怎么处理同类场景，再回到本文件的规则。

| 参照 | 学什么 | 不学什么 |
|---|---|---|
| **Sentry** | 控制台信息架构（浅色侧栏 + 白主区）；hairline 分隔而非投影；muted 表头 + hover 行的表格密度；克制的状态色（状态色只在状态位置出现） | 其营销页的渐变/插画风格 |
| **Linear** | 有限的排版阶数纪律（全站字号阶数 ≤ 7 档，见 §7）；间距节奏一致性（同一类容器同一种 padding）；hover/focus/transition 的精致统一；表单弹层操作栏固定底部的布局范式 | 深色主题倾向（本项目 light-only 不变） |
| **明确反例** | —— | Datadog/Grafana 式重型多色面板：大面积彩虹色块、每面板一个色相。本控制台全界面色相受控（见 §1.2 语义色克制） |

### 1.2 五条硬性规则（门禁可检）

以下规则由 `make check-ui` 强制执行（见 §9），违反即 CI 失败：

1. **图标唯一来源**：lucide-react。禁止 emoji/字符画图标（🔧 ⚠ ● ▾ ▸ ← 等），
   禁止内联手绘 SVG（品牌 logo 资产除外，见 branding.md）。
2. **品牌在场**：侧栏与登录页必须使用 `web/public/logo.svg`（完整版像素蘑菇，
   branding.md §2 适配矩阵），禁止占位图形。
3. **语义色克制**：全界面色相 = brand 蓝（primary）+ 中性灰阶 + 语义四色
   （destructive/success/warning/info）。禁止引入 Tailwind 彩虹色阶
   （`bg-blue-*` `text-emerald-*` `bg-purple-*` 等）；trace 等遗留彩虹区块见 §8 白名单，
   只减不增。图表只用品牌单色系 + 灰阶（§2 chart 规则）。
4. **任意值纪律**：字号/间距只取 §7 表格中的既有档位；`text-[10px]`/`text-[11px]`
   等 arbitrary values 须先入 §7 表再使用。
5. **四态完整**：列表/详情页交付时必须覆盖 loading / empty / error / forbidden
   四态（§5/§6 给出各态的标准形态），不允许"能跑就行"的裸页面。

### 1.3 适用范围

本文件的设计原则层（§1/§2 语义边界/§5 交互状态）同时约束 **web 控制台**（`web/`）与
**desktop-ui**（`desktop-ui/`，桌面个人网关 UI）。落地层（基元实现、页面模板）各自维护，
但 desktop-ui 的 token 值必须与 `web/src/app/globals.css` 保持一致（`desktop-ui/src/index.css`），
两端同一屏幕位置应呈现同一视觉语言。

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
| Feedback | `text-success` / `bg-success` | `#16a34a` | 成功/在线/启用状态、diff 新增行 | 不要用于普通文本 |
| Feedback | `text-warning` / `bg-warning` | `#d97706` | 警告提示、draining 等中间态 | 与 destructive 二选一，勿混用 |
| Feedback | `text-info` / `bg-info` | `#0891b2` | 信息提示（Badge info 变体） | 与 primary 区分：info 是状态，primary 是品牌 |

**chart 用色规则**：图表（recharts 等）只取品牌单色系 + 灰阶——主系列 `chart-1`
（= primary 蓝），辅助 `chart-2`（灰）、`chart-3`/`chart-4`（深灰/浅灰线）。SVG 属性里
直接写 `var(--primary)` / `var(--border)` 引用 token；**禁止 `hsl(var(--primary))`**
（token 是 hex 值，`hsl(#2456f6)` 是非法 CSS，会静默失效——2026-07 usage 图表事故）。
禁止给图表引入彩虹多色（§1.2-3）。

**已知缺口**：无 spacing/radius/typography scale token（当前散落在组件类名，见 §7）。

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

**布局契约**（2026-07 起）：面板是定高 flex 列——`max-h-[85vh]`，遮罩带 `p-4` 视口边距；
title 栏与 footer 永不滚动（`shrink-0`），body 是唯一滚动区。禁止把操作按钮写在长表单
末尾任其随滚动消失（Linear 式固定操作栏，见下）。

- `Modal`：通用弹层。`size` 语义：**sm**（max-w-sm）= 确认/提示；**md**（max-w-md）=
  简单表单（≤4 字段）与只读详情；**lg**（max-w-lg）= 标准表单；**xl**（max-w-2xl）=
  含双列网格/动态行重复器的复杂表单（model/route 适用）。`title` 渲染标题栏（关闭按钮为
  lucide `X`），`children` 入 body，`footer` 渲染底部固定操作栏。遮罩 `bg-black/50` +
  Esc 关闭 + 遮罩点击关闭 + `role="dialog"` + `aria-modal="true"` + body scroll lock。
- **表单 Modal 的操作栏**：表单的「取消/提交」必须固定在可视区底部。web 侧用
  `modalFormActionsClass`（modal.tsx 导出）套在表单末尾的操作行上——sticky 定位让按钮栏
  钉在 body 可视区底部，同时 pending/submit 状态与表单同组件（`useActionState` 不外泄）。
  正例：`providers/create-form.tsx`、`models/create-form.tsx`、`routes/route-form.tsx`。
  desktop-ui 同样用 `modalFormActionsClass`（`desktop-ui/src/components/ui/modal.tsx` 导出）；
  desktop-ui 的删除确认走 `desktop-ui/src/components/ui/confirm-modal.tsx`。
- `ConfirmModal`：基于 Modal 的薄封装，删除/危险操作确认的唯一入口。固定 `size="sm"`，
  `title` + `message` + destructive `onConfirm` + outline `onCancel`。`confirmLabel` 覆盖
  确认文案，`loadingLabel` 覆盖 loading 文案（默认 "Deleting…"，回滚等非删除场景必传），
  `error` 底部红块。正例：providers/routes 删除、roles 删除、config/history 回滚。

> **禁止**：原生 `confirm()`/`alert()`（`make check-ui` 拦截）；页面内手写第三套弹层
> （行内两步确认、自制 fixed 遮罩都算）。

### DetailField（`web/src/components/ui.tsx`）

只读详情展示的基元：label（`text-xs font-semibold uppercase tracking-wide
text-muted-foreground`）在上、value（`text-sm text-foreground`）在下。组合方式：单列
`flex flex-col gap-4` 或双列 `grid grid-cols-2 gap-x-6 gap-y-4`。正例：routes 详情
Modal（`routes/client.tsx`）。desktop-ui 有同名基元（`desktop-ui/src/components/ui/detail-field.tsx`，
API 一致：`<DetailField label>…</DetailField>`）。

**已交付基元速查**（详见 §8 表）：Button/Input/Card/DetailField（ui.tsx）、Modal/ConfirmModal、
NavLink、Badge、EmptyState、Toast（sonner）、Skeleton、Select、Textarea、MultiSelect。

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

- 短表单用 `flex flex-wrap items-end gap-3` 横排；字段多/长表单用单列 `flex flex-col gap-4`。
  **宿主在 Modal 中的表单**（providers/models/routes 范式）：字段区单列 `flex flex-col gap-4`，
  操作行固定底部——`<div className={modalFormActionsClass}>`（§3 Modal），不要 `pt-2` 了事。
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

返回导航（lucide `ArrowLeft` + outline sm Button，禁止 ← 字符）+ 标题区 + Card 分区
（长详情分卡）+ DetailField 呈现字段。正例骨架：

```tsx
<>
  <Button href="/xxx" variant="outline" size="sm">
    <ArrowLeft className="h-3.5 w-3.5" />
    {t("back")}
  </Button>
  <div className="flex flex-col gap-2">
    <h1 className="text-xl font-semibold text-foreground">{row.name}</h1>
    <p className="text-sm text-muted-foreground">{row.description}</p>
  </div>
  <Card className="grid grid-cols-2 gap-x-6 gap-y-4 p-4">
    <DetailField label={t("fields.type")}>{row.type}</DetailField>
    <DetailField label={t("fields.createdAt")}>{formatTime(row.created_at)}</DetailField>
  </Card>
</>
```

现状：request-logs/model-catalog 的详情页已用返回导航范式，但字段区仍手写；trace 三层
详情页结构特殊（主从布局），收敛排期见 §8。

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
| loading（页面/列表） | 路由级 `loading.tsx` + Skeleton（已铺 usage/audit/request-logs，其余页见 §8） | `usage/loading.tsx` |

**规则**：不要自定义 `:focus { outline: ... }`；统一用 ring。loading 态目前只到按钮级，页面级
skeleton 待补（§8）。

## 6. 反馈模式

> 内容文案（显示什么）见 domain-flows.md；本节只规定**长什么样**。

### 空状态
- 视觉模板（目标态）：图标（`text-muted-foreground`）+ 标题（`text-sm font-medium`）+ 副文
 （`text-xs text-muted-foreground`）+ CTA（`<Button variant="outline" size="sm">`）。
 基元：`web/src/components/ui/empty-state.tsx`。
- 文案与 CTA 目标：严格遵循 domain-flows.md §3（L104-114）的"0 数据状态/操作引导"表，CTA
 链接目标遵循 §1 onboarding 依赖链（L11-33，如 model 列表空 → 链到 providers 页）。
- 现状：EmptyState 已交付并在 model-catalog/usage/audit 落地；其余表格仍是纯文字空态，
 推广排期见 §8。

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
- 行级/瞬时错误统一走 Toast（sonner，`web/src/lib/toast.ts`）；原生 `alert()` 已清零
  （`make check-ui` 门禁拦截复发）。
- 409 引用冲突目标为 Modal；403 目标为 Toast 或局部"无权限"（ForbiddenNotice 已覆盖 ~12 页）。

### 成功
- 现状：写操作成功 = 轻量 Toast（`toast.success`，正例 providers/create-form.tsx）+
  `router.refresh()`。静默刷新仅限无感知的次要写入。

### 确认
- 删除/不可逆操作统一 `ConfirmModal`（§3）；原生 `confirm()` 已清零并入门禁。
  roles 的行内两步确认已收敛（2026-07）。

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
| 徽章/小标签 | `text-[10px]`（roles builtin、HotBadge）或 `text-[11px]`（model-card、侧栏品牌副）——仅此两档，禁止其他 arbitrary 字号 | `roles/client.tsx`、`model-card.tsx` |
| h2 / h3 / 空状态标题 | **未定**（缺口，§8） | 待详情页/分区落地时补 |

**规则**：新页面沿用上表尺度，不要引入 `p-7`/`gap-4` 等未出现的中间值。spacing/radius/typography
scale 未成 token（§8），当前以"复用现有类名"为准。

## 8. 已知缺口与后续待办

按"缺失 token / 缺失基元 / 不一致待统一"三类组织（跨页复用，不按资源页登记）。每项标优先级
（P0=铺相关页前必须 / P1=显著改善体验 / P2=锦上添花）+ 触发条件。**本节必须如实反映代码
现状**——自评与代码漂移是本文件曾经犯过的错误（2026-07 审计时"alert() 已清理 ✅"与
`roles/client.tsx` 实际不符），修改相关代码后必须回来更新本节。

### 缺失 token
| 项 | 优先级 | 触发条件 |
|---|---|---|
| `success`/`warning`/`info` 语义色 | ✅ 已实现（`globals.css`） | usage 趋势、quota 余额正/负、operator 状态需要 |
| spacing scale（`--space-*`） | P2 | 当前复用现有类名够用；长表单分组时再考虑 |
| radius scale（`--radius-*`） | P2 | 当前 `rounded-md`/`rounded-lg` 够用 |
| typography scale（`--text-*`） | P2 | h2/h3 落地时再考虑 |

### 缺失基元
| 项 | 优先级 | 触发条件 |
|---|---|---|
| Badge/Tag | ✅ 已实现（`web/src/components/ui/badge.tsx`） | roles scope、plugins enabled 已采用；其余手搓芯片见下表 |
| EmptyState | ✅ 已实现（`web/src/components/ui/empty-state.tsx`） | 空状态视觉模板（图标+标题+CTA）统一 |
| Toast | ✅ 已实现（sonner，`web/src/components/ui/toaster.tsx` + `web/src/lib/toast.ts`） | 行级错误、成功反馈、403 统一 |
| Dialog/Modal | ✅ 已实现（`web/src/components/modal.tsx`，含 `modalFormActionsClass`） | 通用弹层 + ConfirmModal |
| DetailField | ✅ 已实现（`web/src/components/ui.tsx`；desktop-ui 有同名基元） | 详情展示 label/value |
| Skeleton/Spinner | ✅ 已实现（`web/src/components/ui/skeleton.tsx`）+ 路由级 `loading.tsx`（usage/audit/request-logs） | usage/audit 等重查询页的加载态 |
| Select | ✅ 已实现（`web/src/components/ui/select.tsx`，Command+Popover，非 radix） | 剩余原生 `<select>` 见下方「不一致待统一」 |
| Textarea | P2 | 未来有长文本字段时（`web/src/components/ui/textarea.tsx` 已存在） |
| Table 壳（封装 §4.2） | P2 | 第 3+ 个资源页落地时考虑封装 |

### 不一致待统一
| 项 | 优先级 | 现状 | 目标 |
|---|---|---|---|
| 表单错误展示 | ✅ 已统一 | 所有表单均用 `role="alert"` + `bg-destructive/10` 红块（§6） | — |
| `confirm()`/`alert()`/`window.location.reload()` | ✅ 已清零（2026-07 复核） | 全站为零；roles 行内两步确认已收敛为 ConfirmModal | `make check-ui` 门禁防复发 |
| 删除按钮 | ✅ 已统一 | 删除/revoke 全部 `<Button variant="destructive" size="sm">` + ConfirmModal | — |
| 操作列容器 | ✅ 已统一 | 资源表操作列多按钮均用 `<div className="flex items-center justify-end gap-1">`（§4.2） | — |
| Modal 表单操作栏 | ✅ 已统一（2026-07） | providers/models/routes 表单操作行固定底部（`modalFormActionsClass`）；Modal 面板 `max-h-[85vh]` + title/footer 不滚动 | — |
| 剩余原生 `<select>` | P1 | 4 处（check-ui 白名单豁免中）：`trace/client.tsx`、`request-logs/client.tsx`、`usage/client.tsx`（均为筛选条）、`ui/pagination.tsx`（分页器页码）；动态行 2 处（models/upstream-row、routes/route-provider-row）复核已迁 `ui/select.tsx`（2026-07） | 全部迁 `ui/select.tsx` |
| trace 彩虹色收敛 | P1 | `web/src/components/trace/trace-categories.tsx` + `desktop-ui/src/components/trace/trace-categories.tsx` + `desktop-ui/src/components/trace/json-tree.tsx` 共 13 种色相硬编码（check-ui 白名单豁免中，只减不增）；两份 trace-categories 是分叉副本，收敛时评估合并 | 收敛到 §1.2-3 受控语义色板 |
| Badge 采用率 | P1 | roles/plugins 已用 Badge；其余手搓芯片散见 settings/model-card/model-catalog 等 | 手搓芯片全量收敛 Badge |
| EmptyState 采用率 | P1 | 仅 model-catalog/usage/audit 3 张表；providers/api-keys/request-logs/data-plane-nodes/roles/config-history 仍纯文字空态 | 全表格 EmptyState |
| 表格样式变体 | P1 | §4.2 标准之外仍有 3 个变体：`trace/client.tsx`（px-3 py-2 + hover/40 + 自带 Th）、`roles/client.tsx`（无 rounded 容器 + bg-muted/50 + py-3）、`config/history/client.tsx`（bg-muted/50 + py-2 非大写表头） | 收敛 §4.2 |
| i18n 硬编码文案 | P1 | `plugins-table.tsx` "Yes"/"No"/"global"；`trace-categories.tsx` 中文"行"；`ui/select.tsx`/`multi-select.tsx` 默认 placeholder 英文 | 全量走 locale key（check-i18n 已覆盖对齐） |
| loading 页面态 | P1 | 仅 usage/audit/request-logs 3 页有 `loading.tsx`；其余 15 页无 | 重查询页全铺 skeleton |
| 语言切换 UI | P2 | i18n 路由与双语文案齐备，无切换入口（`common.json` 的 language key 是死文案） | 侧栏底部加切换 |
| 全局 error.tsx / not-found.tsx | P2 | 全站缺失，异常掉 Next 默认页 | 按 §5/§6 形态补齐 |
| usage 页静默吞错 | P2 | `usage/page.tsx` catch 仅处理 401，非 401 错误渲染空表（分不清"没数据"与"后端挂了"） | 错误态按 §6 渲染 |
| overview 页弱类型 | P2 | `Record<string, unknown>` 强转渲染 | 对齐 SDK 生成类型 |
| groups 删除按钮文案 | P2 | `groups/table.tsx` 用 `tG("actions.delete")`，其余 5 处用 `tCommon("actions.delete")` | 可选统一为 `tCommon` |
| h2/h3/空状态标题层级 | P2 | 未定 | 详情页/分区落地时定 |

**shadcn 现状**：`web/src/components/ui/`（style=base-nova，基于 `@base-ui/react`）承载复合
基元——Select/MultiSelect 的依赖链是 `select.tsx`/`multi-select.tsx` → `command.tsx` →
`dialog.tsx`/`input-group.tsx` → `button.tsx`，**整条链均为活代码，不可删除**。
业务页面首选本地 `ui.tsx`（Button/Input/Card/DetailField）；shadcn 层用于本地层覆盖不了的
复合交互（下拉、多选、徽章、骨架屏）。新增基元沿用 `web/src/components/ui/` 目录与 `cn()`
约定，不引入 radix。

## 9. UI 门禁（make check-ui）

`scripts/check-ui.sh` 把 §1.2 的硬性规则变成 CI 可执行检查（grep-based，与 check-i18n 同风格），
扫描 `web/src` 与 `desktop-ui/src`：

| 规则 | 拦截模式 | 豁免 |
|---|---|---|
| 禁 hex 颜色字面量 | `#[0-9a-fA-F]{3,8}` 出现在 tsx | `globals.css`/`index.css` 等 CSS 文件不在扫描范围 |
| 禁彩虹色阶类 | `(bg\|text\|border)-(blue\|emerald\|orange\|purple\|amber\|rose\|cyan\|green\|red\|sky\|violet\|teal\|fuchsia\|indigo\|slate)-\d` | §8 白名单：两份 trace-categories.tsx + json-tree.tsx |
| 禁 `dark:` 变体 | `dark:` | 无（light-only） |
| 禁原生弹窗/刷新 | `alert(` `confirm(` `window.location.reload` | 无 |
| 禁 emoji/字符画图标 | 🔧 ⚠ ● ▾ ▸ ← 等 | 无 |
| 禁原生 `<select>` | `<select` | 现有 4 处（§8 已登记迁移项，只减不增）+ 基元内部实现（`web/src/components/ui/select.tsx`、`desktop-ui/src/components/ui/select.tsx`） |
| 禁脚手架资产 | `next.svg`/`vercel.svg` 引用 | 无 |

白名单机制：豁免清单内嵌在脚本中（文件级），对应 §8 已登记的收敛项；**只减不增**——
新增豁免必须同步在 §8 登记一行缺口，并在 PR 描述中说明收敛排期。
