# ADR-0044: 合并 model-catalog 到 models（单页 + 角色条件渲染）

- Status: Accepted
- Date: 2026-07-19
- Builds on ADR-0017 (RBAC), ADR-0033 (data-plane keys not bound to roles)
- SoT: `design/frontend.md` §5（角色权限矩阵）、`web/src/app/[locale]/(dashboard)/models/`（实现）

## Context

历史上有两个模型相关页面，**无任何文档记录**（design/frontend.md §5/§9 未提 model-catalog，ADR 0001-0042 无相关条目）：

- `/models`：super-admin 专用，表格视图 + 完整 CRUD。挂在 layout 的 `scopeKind === "global"` 块内 + `NAV_PERMS.provider` 守卫。
- `/model-catalog`：所有 authenticated operator 可见，只读卡片视图 + 搜索 + capability 过滤 + 独立详情页 `/model-catalog/[alias]`。挂在 "Both-scope" 分区，无权限守卫。

问题：

1. **用户感知"重叠"**——两个页面都列模型，入口分散，认知负担。
2. **tenant-admin 只能看 model-catalog**，super-admin 两个都用，交互模式割裂。
3. **前端 UI 与后端权限模型不对齐**：后端 `GET /api/v1/models` 挂在 `configReadGrp`（无 role gate，对 super-admin + tenant-admin 都开放，见 `internal/admin/crud_model.go:22-31` + `server.go:89`），前端却按角色拆成两套 UI。`POST/PATCH/DELETE` 才挂在 `writeGroup`（super-admin only）。
4. **设计文档缺口**：双页面设计意图从未被记录，是用户认为"功能重叠"的根因。

## Decision

1. **单一 `/models` 路由**，删除 `/model-catalog` 路由和菜单。
2. **统一卡片视图**（删除现有 `/models` 的表格视图），沿用 model-catalog 的卡片网格 + 搜索框 + capability chip 过滤。
3. **角色条件渲染**：
   - super-admin（`session.scopeKind === "global"`）：卡片视图 + "创建模型"按钮 + 卡片右上角编辑/删除按钮 + Modal。
   - tenant-admin：纯只读卡片视图，无任何 CRUD 按钮。
4. **保留独立详情页**，从 `/model-catalog/[alias]` 移到 `/models/[alias]`。super-admin 在详情页也可编辑/删除。
5. **layout 中 `/models` 保留在 super-admin 块的 providers 下方**（与原 `/models` 一致），由 `NAV_PERMS.provider` 守卫。tenant-admin 看不到菜单项，但仍可直接通过 URL 访问（后端 `GET /api/v1/models` 对所有 authenticated operator 开放，前端按 `canWrite=false` 渲染只读卡片视图）。
6. **取数策略**：拉全量 (`limit=1000`) + 客户端过滤（沿用 model-catalog 已验证模式），放弃 cursor 分页。理由：cursor 分页与客户端搜索/过滤不兼容（搜索必须扫全量）；models 是全局共享配置，数量级是几十而非几千。
7. **避免 tenant-admin 触发 403**：`/models/page.tsx` 仅在 `canWrite` 时调 `GET /api/v1/providers`（创建表单需要，但 endpoint 对 tenant-admin 是 403——挂在 `writeGroup`）。

## Consequences

正面：

- **入口单一**，消除"重叠"感知。
- **UI 与后端权限模型对齐**：GET 对两角色开放 → 两角色看同一页；写操作 super-admin only → CRUD 按钮按 scopeKind 条件渲染。
- **卡片视图信息密度更友好**：capability chip、起价、上下文长度一目了然。
- **补齐文档缺口**：本 ADR + frontend.md §5/§9 同步更新。

负面 / 约束：

- **拉全量策略**在模型数 >1000 时需重新评估（当前几十级，远未到上限）。
- **tenant-admin 看不到菜单项**（菜单在 super-admin 块内），但仍可直接通过 URL 访问 `/models` 和 `/models/[alias]`，看到只读卡片视图。创建按钮隐藏（`canWrite=false` 时不调 `GET /api/v1/providers`，避免 403）。
- **详情页 `/models/[alias]` 是仓库首个 `[alias]` 模式**——routes/providers/plugins 都是 Modal 编辑，无详情页。后续若要加详情页可参考此模式。
- **e2e 测试需改造**：`models.spec.ts` + `routes.spec.ts` 的 `createModel` helper 原基于表格视图（`getByRole("cell")`/`getByRole("row")`），改为卡片选择器。

## Alternatives considered

- **保持现状 + 补文档**：双页面设计实质正确（双受众），但用户的"重叠"感知不解决。
- **保留两路由，让 `/models` 也支持卡片视图**：super-admin 享受卡片 UI，但仍有两个入口，"重叠"感知不解决。
- **真合并但保留表格视图**：丢失 model-catalog 的搜索/过滤能力，且表格视图对只读浏览不友好。

## 参考

- 现状盘点：`web/src/app/[locale]/(dashboard)/models/` + `web/src/app/[locale]/(dashboard)/model-catalog/`
- 后端权限模型：`internal/admin/crud_model.go:22-31`（GET 无 gate）、`internal/admin/crud_model.go:33`（POST/PATCH/DELETE super-admin only）、`internal/admin/server.go:86-94`
- 相关 ADR：ADR-0017（RBAC）、ADR-0033（数据面 keys 不绑 role）
- 设计文档：`design/frontend.md` §5（角色权限矩阵）、§9（首版范围）
