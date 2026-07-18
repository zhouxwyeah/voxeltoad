# ADR-0020: Control Panel front-end auth topology — Next.js BFF holds the operator token

- Status: Accepted
- Date: 2026-07-02
- Builds on ADR-0017 (RBAC: operator auth via opaque, server-side-revocable
  session tokens), ADR-0019 (control-plane read/ops APIs, OpenAPI-first with a
  code-generated TS admin client + contract tests).

## Context

ADR-0019 delivered the management-plane (admin) HTTP API, an authoritative
OpenAPI 3 spec (`docs/openapi/admin.yaml`), and a **code-generated** TypeScript
admin client (`sdk/typescript/src/admin.ts`) that is exercised by contract tests
against a live admin server (`cmd/adminstack` + `scripts/adminstack-test.sh`).
The next phase is the Control Panel UI, and the team asked whether the UI should
talk to the admin API directly, or whether an extra server layer is warranted.

The security-relevant fact is **what an operator session token can do**: it is a
high-privilege credential (a super-admin token can rewrite global config, create
operators, top up quotas). ADR-0017 already made it an **opaque, server-side,
instantly-revocable** token (a `sessions` row, not a JWT) — which is exactly the
shape that suits an httpOnly cookie or server-side custody, not JS-readable
storage.

Three topologies were considered for how the browser obtains and carries that
token:

1. **Browser → admin, `Authorization: Bearer`.** Simplest, but the token lives
   in JS-readable storage (localStorage / memory) and is exfiltratable via XSS.
   Unacceptable for a credential this powerful.
2. **Browser → admin, httpOnly+SameSite session cookie.** Safe against XSS
   token theft, but requires changing the admin plane (`login` to `Set-Cookie`,
   `authnMiddleware` to read the cookie, and a CSRF mechanism).
3. **Browser → Next.js server → admin (BFF).** The operator token is held
   **only** on the Next.js server; the browser holds only a framework session
   cookie (httpOnly). The Next↔admin hop stays `Authorization: Bearer` over the
   internal network.

The front-end framework was decided to be **Next.js/Nuxt (SSR)**, so a server
layer already exists in the deployment. That reframes the question: the extra
layer is **not** an extra deployment unit to introduce — it is the framework's
own route-handler runtime. The classic reasons to reject a bespoke BFF
(aggregation needs, hiding the API on a private network) either don't apply
(admin is simple CRUD; network reachability is a topology concern solved by
placing admin on an internal network/VPN) or are satisfied for free by SSR.

## Decision

### 1. The operator token is held server-side (BFF pattern), never in the browser

`Browser → Next.js route handler → (generated admin client + Bearer) → admin`.

- The browser authenticates to the **Next.js server** with the framework's
  httpOnly+SameSite session cookie (JS-unreadable). The operator session token
  from `POST /auth/login` is stored server-side (Next session) and **never sent
  to the browser**.
- The Next.js server↔admin hop reuses the existing `Authorization: Bearer`
  scheme. That hop is server-to-server on the internal network, where a bearer
  token is appropriate; `authnMiddleware` is unchanged.

### 2. The generated admin client is used server-side only

The code-generated client (`@voxeltoad/gateway-sdk/admin`) runs **only** in
Next.js route handlers / server components. The browser reaches admin
capabilities **indirectly**, through thin Next route handlers that proxy to the
admin API. The OpenAPI spec remains the single source of truth; its generated
types are reused on the server side, so the proxy layer is type-checked against
the contract (the ADR-0019 contract guarantee still holds where the client runs).

Trade-off accepted: the browser does not get end-to-end typed calls into admin;
each admin endpoint the UI needs gets a small Next-side proxy handler. (A future
option, if the churn bites, is to generate a second typed layer for the
browser↔Next boundary — explicitly out of scope now.)

### 3. The admin plane is an internal service; CORS becomes removable

Because the browser never calls admin directly, admin does not need to be a
CORS-enabled, browser-facing origin. The configurable CORS added in ADR-0019
(`admin.Options.AllowedOrigins`, `corsMiddleware`) and the `AllowedOrigins`
wiring in `cmd/adminstack` become **dead code** under this topology.

**This ADR records that intent but does NOT perform the removal.** The cleanup
is deferred to when the front-end lands, to avoid deleting a path that the
current `adminstack` contract test still relies on (it sets an allowed origin).

### 4. No native cookie/CSRF change to the admin plane

Topology 2's admin-side changes (`Set-Cookie` login, cookie-reading
`authnMiddleware`, CSRF token) are **not** pursued. Under the BFF pattern the
token never enters the browser, so the XSS-theft threat those changes address is
already eliminated at the boundary. The admin plane keeps its simple Bearer
auth. (The `credentials` option and cookie-security note already present in
`sdk/typescript/src/admin.ts` are harmless server-side and may be trimmed when
the UI lands.)

## Consequences

- **No new deployment unit.** The BFF is the Next.js route-handler runtime that
  already ships with the chosen framework — not a separately-built/-deployed
  service.
- **High-privilege operator tokens never reach browser JS** → not
  XSS-exfiltratable. This is the primary security win and the reason the
  topology was chosen.
- **Contract stays single-source-of-truth** where it matters: the generated
  client is consumed server-side, still type-checked against `admin.yaml`; the
  `sdk-codegen-check` CI gate still guards drift.
- **Cost:** each admin endpoint the UI uses needs a thin Next-side proxy route;
  the browser↔Next boundary is not auto-typed from the spec (accepted).
- **Deferred cleanup (recorded, not done here):** remove `corsMiddleware`,
  `admin.Options.AllowedOrigins`, and the `cmd/adminstack` `AllowedOrigins`
  setting once the UI (and its adminstack usage) no longer needs a
  browser-facing origin.
- **No admin-plane auth change now.** `rbac.go` (opaque token + `sessions` table
  + instant revocation) is already well-suited to server-side custody; it is
  untouched.
- **Deferred (phase-2 / UI phase):** the Next.js UI itself, its route-handler
  proxies (the actual "BFF API"), and the CORS/CSRF cleanup above. This ADR is a
  decision record; no BFF code is written as part of it.
