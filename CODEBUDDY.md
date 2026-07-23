# CODEBUDDY.md

> 本文件是约束规范的**路由表**，本身不写具体规则。所有规则在 `design/` 下的专题指南中，作为人与 AI agent 共用的单一事实来源。

## 项目简介

voxeltoad —— 企业级大模型网关。Go 同构（数据面 + 管理面），对外提供 OpenAI 兼容 API，统一代理 OpenAI / Claude / 腾讯混元 / 智谱 / 任意 OpenAI 兼容供应商，并提供配额计费、限流熔断、审计、多租户等企业治理能力。

完整设计见 [docs/plans/2026-06-29-llm-gateway-design.md](docs/plans/2026-06-29-llm-gateway-design.md)。

## 领域词汇与决策记录

术语的精确含义见 [docs/glossary.md](docs/glossary.md)；重要设计决策（及其背景与取舍）见 [docs/adr/](docs/adr/)。改动涉及供应商/适配器/路由/密钥等概念时，先对齐术语与相关 ADR。

## Architecture

分层模型、依赖规则、「新增供应商/插件改哪些文件」对照表，遵循 [design/architecture.md](design/architecture.md)。

**门禁约束**：任何 PR 若新增/删除/修改 `cmd/` 入口或顶层目录（含前端工程），**必须同步更新 `design/architecture.md`**（Layer Model L4 + Directory Layout + §三入口依赖矩阵）。architecture.md 自称是分层与依赖的单一事实来源——保持同步是 PR 作者的责任，不是 reviewer 的责任。`make check-docs` 会校验 architecture.md Directory Layout 与实际目录一致、backtick 路径存在。

## Ingress Protocols (客户端入站协议)

客户端入站协议（OpenAI `/v1/chat/completions`、Anthropic `/v1/messages`）由 `internal/ingress/` 层（L2，与 `internal/adapter/` 对偶）翻译为 unified 模型，使数据面 dispatcher/billing/telemetry 不感知入站协议。新增入站协议改哪些文件见 architecture.md 的 Common Tasks 表，设计决策见 [ADR-0045](docs/adr/0045-anthropic-ingress-protocol.md)。

**门禁约束**：任何 PR 若新增 ingress codec 子目录（`internal/ingress/<name>/`）、修改 `Codec` 接口签名、或新增客户端路由（`r.Post(...)`），**必须同步更新 `design/architecture.md`**（L2 Layer Model + Directory Layout + Common Tasks 表）和 `docs/glossary.md`（新术语）。architecture.md 自称是分层与依赖的单一事实来源——保持同步是 PR 作者的责任，不是 reviewer 的责任。`make check-docs` 会校验 architecture.md Directory Layout 与实际目录一致。

## Unit Tests

测什么、不测什么、Go 测试模式（表驱动 / 协议适配 / 计费 / 限流），遵循 [design/unit-test.md](design/unit-test.md)。

## E2E Tests

mock 上游供应商、SSE 流式断言、profile YAML + 特征标志、TS SDK 契约测试、测试数据隔离与 Pitfalls，遵循 [design/e2e.md](design/e2e.md)。

## Observability

每条 LLM 请求必须记录的语义字段（model / token / TTFT / provider / cache / 拦截）与门禁约束，遵循 [design/observability.md](design/observability.md)。

## Frontend (Control Panel)

运营控制台前端技术栈与架构（Next.js App Router + RSC/Server Actions、加密 cookie 会话、集中 money 模块、角色守卫、首版范围与后端缺口），遵循 [design/frontend.md](design/frontend.md)。鉴权拓扑见 ADR-0020。

## Design System (Control Panel + Desktop UI)

Control Panel 视觉语言单一事实源（设计参照系、token 语义边界、基元与变体选用规则、页面模板含 JSX 骨架、交互状态、反馈模式、间距与排版节奏、已知缺口、UI 门禁），遵循 [design/design-system.md](design/design-system.md)。设计原则层（§1/§2/§5）同时约束 `web/` 与 `desktop-ui/`。

**门禁约束**：任何 PR 若新增/修改 UI 基元、设计 token 或页面模板（即触碰 `web/src/components/ui*`、`web/src/app/globals.css`、`desktop-ui/src/components/ui/`、`desktop-ui/src/index.css`），**必须同步更新 `design/design-system.md`**（对应基元小节、token 表、§8 缺口清单）。design-system.md 自称是视觉单一事实来源——保持同步是 PR 作者的责任，不是 reviewer 的责任。`make check-ui` 会校验 §1.2 硬性规则（token 硬编码、`dark:` 变体、原生弹窗/`<select>`、emoji 图标、脚手架资产），白名单豁免只减不增。

## Branding

品牌名 `voxeltoad` 的权威写法、命名由来、logo 造型与配色、资产清单（`web/public/logo*.svg`）、候选名归档，遵循 [design/branding.md](design/branding.md)。

## i18n & 错误码

国际化方案、locales 目录结构（含 `errors/<domain>.json` 子目录）、key 命名约定、
后端 `internal/apperr` 错误码 catalog 与前端 i18n key 对齐机制，遵循
[design/frontend.md §12](design/frontend.md)。错误码 catalog 校验见 `make check-errors`，权限点 catalog 校验见 `make check-permissions`，
locale key 对齐见 `make check-i18n`。业务流程与空状态/错误 UX 内容见
design/domain-flows.md。

## Domain Flows

后端强制的运营业务流程、实体生命周期与状态机、onboarding 依赖链、空状态/引导/校验错误 UX 约定，遵循 [design/domain-flows.md](design/domain-flows.md)。backend API 契约遵循 [docs/openapi/admin.yaml](docs/openapi/admin.yaml)。

## Roadmap

项目演进的单一事实来源（P0/P1/P2 及触发条件），遵循 [docs/roadmap.md](docs/roadmap.md)。

## Desktop (个人网关)

桌面个人网关的需求边界、SQLite 存储、K1 鉴权、与 cmd/gateway 的功能子集关系、编译期 canary 机制，遵循 [design/desktop.md](design/desktop.md)。架构决策见 ADR-0041。

## Database

表清单、分领域 ER 图（全局配置 / 租户作用域 / 运营）、软引用关系、设计决定（PG 不是完整性权威 / plugins 无 UNIQUE / audit_logs 无 FK / provider_credentials 加密落库由 ADR-0031 实现 / quotas.scope 扁平），遵循 [design/database.md](design/database.md)。schema 形状由 `internal/store/migrations/` 定义。

**门禁约束**：任何 PR 若新增/修改/删除表、列、索引或约束（即触碰 `internal/store/migrations/` 下的 SQL 文件），**必须同步更新 `design/database.md`**（对应表的 ER 图块、字段表、索引清单、字段计数、§1 表分类矩阵、头部迁移数量）。database.md 自称是单一事实来源——保持同步是 PR 作者的责任，不是 reviewer 的责任。

## 注意事项
 回答均使用中文
