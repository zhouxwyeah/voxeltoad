# API Definitions

权威契约（OpenAPI 3）位于 [`docs/openapi/admin.yaml`](../docs/openapi/admin.yaml)，作为 SDK 与前端的
**单一事实来源**（[ADR-0019](../docs/adr/0019-control-plane-read-ops-apis.md)）。TypeScript admin client
（`@voxeltoad/gateway-sdk/admin`）从该 spec 生成，React admin UI（`web/`）通过 `file:` 依赖引用。

改契约时先改 `docs/openapi/admin.yaml`，再 `make sdk-build` 重新生成 client（见
`design/architecture.md` 的「改对外 API 契约」条目）。
