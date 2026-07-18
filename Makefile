GO ?= go
GATEWAY_BIN := bin/gateway
ADMIN_BIN := bin/admin
SDK_DIR := sdk/typescript
WEB_DIR := web

# Pinned tool versions (keep in sync with .tool-versions).
GOVULNCHECK_VERSION := v1.1.4
GOLANGCI_LINT_VERSION := v1.62.2

.PHONY: help all build build-gateway build-admin run-gateway run-admin \
        devstack devstack-test adminstack adminstack-test stack-test-all \
        test test-race test-db cover test-e2e test-e2e-quick test-e2e-race fmt fmt-check vet lint audit \
        check-i18n check-i18n-keys check-ui \
        check-errors check-permissions \
        check-docs \
        arch-check tidy \
        sdk-install sdk-build sdk-test sdk-typecheck sdk-lint sdk-codegen-check \
        web-install web-build web-typecheck web-lint web-e2e web-dev start-stack start-gateway \
        docker docker-gateway docker-admin dev-deps dev-deps-down \
        ci ci-web ci-heavy clean

# Show available targets (grouped by the ## comments below).
help:
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: build

## ---- Go build ----
build: build-gateway build-admin ## Build both binaries into bin/

build-gateway:
	$(GO) build -o $(GATEWAY_BIN) ./cmd/gateway

build-admin:
	$(GO) build -o $(ADMIN_BIN) ./cmd/admin

run-gateway: ## Run the data plane (go run)
	$(GO) run ./cmd/gateway

run-admin: ## Run the management plane (go run)
	$(GO) run ./cmd/admin

# Dependency-free local data plane for manual curl/SDK testing: embedded
# PostgreSQL (no docker) + in-process mock upstream + static config (no admin).
# Guarded by the `devstack` build tag so it stays out of the normal build/CI.
# Pairs with scripts/devstack-test.sh. See cmd/devstack/main.go.
# NB: launched via scripts/devstack.sh (pre-built binary + process-group
# cleanup) rather than `go run` — `go run` orphans the embedded PostgreSQL on
# Ctrl-C. The wrapper stops devstack + PG cleanly on exit (GATEWAY_ALLOW_INSECURE_DEV=1
# is the ADR-0007 dev escape hatch).
devstack: ## Run a self-contained data plane for manual testing (no docker/admin)
	./scripts/devstack.sh

# End-to-end smoke test: starts devstack (embedded PG + mock upstream), drives
# curl requests, asserts status codes/bodies, and tears the stack down. Needs
# nothing else running. First run downloads a PostgreSQL binary (cached after).
devstack-test: ## Run the devstack e2e smoke test (auto start/stop)
	./scripts/devstack-test.sh

# Desktop personal gateway — three flows. The default (no-tag) build stays
# CGO-free and runs on every PR via `make test`; `desktop-build` is the macOS
# .app build (CGO_ENABLED=1, Wails CLI). See design/desktop.md §11,
# design/architecture.md §三入口依赖矩阵.
desktop-test: ## Run desktop gateway Go tests (wiring + store + server)
	go test ./cmd/desktop/...

desktop-e2e: ## Manual smoke test of the desktop gateway (build mock + desktop, curl, cleanup)
	./scripts/desktop-test.sh

desktop-web-dev: ## Start Go gateway (:8787) + Vite dev server (:5173) for desktop-ui hot reload
	./scripts/desktop-web-dev.sh

desktop-build: ## Build the macOS .app (requires Wails CLI: go install github.com/wailsapp/wails/v2/cmd/wails@latest)
	./scripts/build-desktop.sh darwin

desktop-build-windows: ## Build the Windows NSIS .exe (run on Windows; requires Wails CLI + NSIS: choco install nsis)
	./scripts/build-desktop.sh windows

desktop-build-windows-cross: ## Cross-compile Windows .exe from Linux/WSL2 (requires: sudo apt install mingw-w64 nsis)
	./scripts/build-desktop.sh windows-cross

# Guarded by the `adminstack` build tag so it stays out of the normal build/CI.
# Serves the real admin.Router (login, config CRUD, tenants, api-keys, quotas,
# usage, audit) backed by embedded PostgreSQL. Pairs with
# scripts/adminstack-test.sh. See cmd/adminstack/main.go.
# NB: launched via scripts/adminstack.sh (pre-built binary + process-group
# cleanup) rather than `go run` — `go run` orphans the embedded PostgreSQL on
# Ctrl-C. The wrapper stops adminstack + PG cleanly on exit.
adminstack: ## Run a self-contained management plane for manual testing (no docker)
	./scripts/adminstack.sh

# End-to-end contract test: starts adminstack (embedded PG), regenerates the TS
# admin client from the OpenAPI spec, drives real admin workflows through it
# (login → tenant → quota → usage/audit), then tears the stack down. Proves the
# generated client talks to a live server over HTTP (ADR-0019 contract loop).
adminstack-test: ## Run the Control Panel contract test via the generated client (auto start/stop)
	./scripts/adminstack-test.sh

# Data-plane SDK contract test: starts devstack (embedded PG + mock upstream),
# drives the VoxeltoadGateway (OpenAI-compatible) client through vitest, asserts
# chat completion shapes + usage + errors, then tears down. The mock upstream's
# behavior is dynamically controlled per-test via POST /__set on port 8091.
sdk-chat-e2e: ## Run the data-plane SDK contract test via VoxeltoadGateway client (auto start/stop)
	./scripts/devstack-sdk-e2e.sh

# All three stack tests against ONE shared embedded PostgreSQL. Builds each
# binary once and boots a single shared PG (cmd/testpg) instead of one per
# suite — this is what `make ci` runs. The three individual targets above are
# kept for running a single suite in isolation.
stack-test-all: ## Run all 3 stack tests (devstack/sdk-chat/adminstack) against one shared PG
	./scripts/stack-test-all.sh

## ---- Go tests (see design/unit-test.md) ----
# Race detector on by default: streaming, rate limiting, and config hot-reload
# are concurrency-sensitive.
test: test-race ## Run Go unit tests (race detector on)

test-race:
	$(GO) test -race ./...

# Tests that need a real PostgreSQL, behind the `dbtest` build tag. First run
# downloads & caches an embedded PostgreSQL binary (see design/unit-test.md).
test-db: ## Run DB-backed tests (embedded-postgres, dbtest tag)
	$(GO) test -race -tags=dbtest ./...

cover: ## Run tests with coverage report
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

# End-to-end / integration tests (see design/e2e.md). Default profile is
# all-mock upstreams; override with E2E_PROFILE_PATH for real providers.
# A single embedded-postgres is started once per package (test/e2e/main_test.go)
# and reused; each test resets state via TRUNCATE, so the whole suite runs in
# under a minute even with -race. Timeout kept generous for loaded CI machines.
test-e2e: ## Run E2E tests (e2e tag, mock upstreams by default)
	$(GO) test -race -tags=e2e -timeout=30m ./test/...

# Quick smoke: core happy-path + error-path E2E without race detector, selected
# by -run to cover non-streaming, streaming TTFT, routing, auth, rate-limit and
# billing settlement. Use during feature development for a fast sanity check
# before running the full suite (see design/e2e.md).
E2E_QUICK_RUN := TestCompat_NonStreamingShape|TestCompat_StreamingTTFT|TestNonStream_UpstreamError_RefundsQuota|TestClosedLoop_ChatCompletion|TestPerm_DisallowedModelForbidden|TestRateLimit_TenantRPMRejectsOverLimit|TestRouting_PriorityPicksFirst
test-e2e-quick: ## Run quick E2E smoke (no race, selected tests)
	$(GO) test -tags=e2e -timeout=5m -run "$(E2E_QUICK_RUN)" ./test/...

# Full E2E with race detector — same as test-e2e. Named explicitly so CI / heavy
# gate configs can reference it unambiguously.
test-e2e-race: test-e2e ## Alias: full E2E with race (same as test-e2e)

## ---- Quality gates ----
# fmt rewrites files in place (local use). fmt-check only verifies and fails if
# anything is unformatted — used by `ci` so the gate never silently edits files.
fmt: ## Format Go code in place
	gofmt -w .

fmt-check: ## Verify gofmt cleanliness (no writes; used by ci)
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "fmt-check: OK"

vet: ## Run go vet (default + dbtest + e2e build tags)
	$(GO) vet ./...
	$(GO) vet -tags=dbtest ./...
	$(GO) vet -tags=e2e ./...

# Requires golangci-lint (pinned in .tool-versions). Install:
#   go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
lint: ## Run golangci-lint (needs golangci-lint installed)
	golangci-lint run ./...

# Vulnerability scanning across both ecosystems. govulncheck is run via `go run`
# at a pinned version so no separate install is needed. The Node side uses
# npm audit (dev-tool advisories included).
audit: ## Scan Go + SDK dependencies for known vulnerabilities
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	cd $(SDK_DIR) && npm audit

# Architecture dependency check (see design/architecture.md): enforces the 4
# layering rules (proxy<->admin isolation, pkg/ free of internal/, no
# cross-adapter and no cross-plugin imports). Implemented in scripts/arch-check.sh.
arch-check: ## Enforce architecture layering rules
	@GO=$(GO) ./scripts/arch-check.sh

tidy: ## Run go mod tidy
	$(GO) mod tidy

## ---- TypeScript SDK ----
sdk-install: ## Install SDK dependencies
	cd $(SDK_DIR) && npm install

sdk-build: ## Build the SDK (tsup: ESM + CJS + d.ts)
	cd $(SDK_DIR) && npm run build

sdk-test: ## Run SDK unit tests (vitest)
	cd $(SDK_DIR) && npm test

sdk-typecheck: ## Type-check the SDK (tsc --noEmit)
	cd $(SDK_DIR) && npm run typecheck

sdk-lint: ## Lint the SDK (biome)
	cd $(SDK_DIR) && npm run lint

# Fails if the checked-in generated admin client is stale w.r.t. the OpenAPI
# spec — i.e. someone edited docs/openapi/admin.yaml but forgot to regenerate.
# Keeps the spec authoritative and the SDK/tests honest (ADR-0019).
sdk-codegen-check: ## Verify the generated admin client matches the spec
	cd $(SDK_DIR) && npm run codegen:check

## ---- Control Panel (web) ----
# Next.js App Router UI (design/frontend.md). The admin SDK is a file: dep, so
# web depends on the SDK dist — run sdk-build first. Like devstack/adminstack,
# web targets are manual/opt-in and NOT in `ci` (create-next-app + Playwright
# browser download are heavy).
web-install: sdk-build ## Install web dependencies (needs SDK dist)
	cd $(WEB_DIR) && npm install

web-dev: ## Start the web dev server for manual UI testing (needs adminstack on :8090)
	cp -n $(WEB_DIR)/.env.example $(WEB_DIR)/.env.local 2>/dev/null || true
	cd $(WEB_DIR) && npm run dev

# One-shot dev startup (adminstack + web). Builds adminstack, boots embedded PG,
# waits for health, then foregrounds Next.js dev. Ctrl-C stops everything.
# Replaces two-terminal workflow: `make adminstack` + `make web-dev`.
start-stack: ## Start adminstack + web-dev in one command (Ctrl-C to stop)
	./scripts/start-stack.sh

# Data plane against a running start-stack: reads adminstack's embedded-PG DSN
# out of its log, reproduces the adminstack dev KEK, then runs the gateway in
# the foreground. Ctrl-C stops ONLY the gateway; admin + web keep running.
# Pairs with scripts/start-gateway.sh. See cmd/gateway/main.go.
start-gateway: ## Start the data plane against a running start-stack (admin+web keep running)
	./scripts/start-gateway.sh

web-build: ## Build the web app (next build)
	cd $(WEB_DIR) && npm run build

web-typecheck: ## Type-check the web app (tsc --noEmit)
	cd $(WEB_DIR) && npm run typecheck

web-lint: ## Lint the web app (eslint)
	cd $(WEB_DIR) && npm run lint

web-test-unit: ## Run web unit tests (vitest; pure-function modules like lib/money.ts)
	cd $(WEB_DIR) && npm run test:unit

# End-to-end slice-0 test: starts adminstack (embedded PG + admin) + the Next
# server, drives a real browser (Playwright) through login → create/list/delete
# provider → logout, then tears everything down. First run downloads a PG binary
# + Playwright chromium (cached after).
web-e2e: ## Run the Control Panel slice-0 e2e (auto start/stop)
	./scripts/web-e2e.sh

## ---- Aggregate web ----
# Aggregate web quality gates (requires web-install first; used in CI, not in
# `ci`). Keeping them separate so the default gate stays fast and npm-free.
# check-i18n verifies en/zh locale key alignment (prevents parallel worktrees
# from silently diverging message keys); jq is a dev-tool prerequisite.
ci-web: check-i18n check-i18n-keys check-ui web-typecheck web-lint web-test-unit web-build ## Run web quality gates (i18n + typecheck + lint + unit tests + build)

check-i18n: ## Verify locale key alignment across all locales (en is baseline)
	@./scripts/check-i18n.sh

check-i18n-keys: ## Verify every static t("key") call resolves to a locale message
	@node ./scripts/check-i18n-keys.mjs

# check-ui enforces design-system.md §1.2 hard rules (hex literals, rainbow
# scales, dark: variants, native dialogs/selects, emoji icons, scaffold
# assets) so the visual language can't drift; allowlists are shrink-only.
check-ui: ## Verify UI hard rules (design-system.md §1.2, allowlists shrink-only)
	@./scripts/check-ui.sh

## ---- Packaging ----
docker: docker-gateway docker-admin ## Build both Docker images

docker-gateway:
	docker build -f deploy/Dockerfile.gateway -t voxeltoad/gateway:dev .

docker-admin:
	docker build -f deploy/Dockerfile.admin -t voxeltoad/admin:dev .

## ---- Local dev dependencies (PostgreSQL only) ----
dev-deps: ## Start local PostgreSQL (Docker)
	./scripts/dev-deps.sh up

dev-deps-down: ## Stop and remove local PostgreSQL
	./scripts/dev-deps.sh down

## ---- Aggregate ----
# Full local verification mirroring what CI would run.
# check-errors validates the internal/apperr catalog (unique codes, valid HTTP
# statuses, i18n keys exist in web/src/locales/en/errors/<domain>.json) so
# parallel worktrees can't silently diverge error codes or drop i18n keys.
ci: fmt-check vet lint arch-check test check-errors check-permissions check-docs check-frontend-permissions check-i18n check-ui sdk-codegen-check sdk-typecheck sdk-lint sdk-test sdk-build stack-test-all ## Run the full verification pipeline

check-errors: ## Verify internal/apperr catalog (unique codes, valid statuses, i18n keys present)
	@./scripts/check-errors.sh

check-permissions: ## Verify authz permission catalog (unique keys, format, alignment)
	@./scripts/check-permissions.sh

check-docs: ## Verify ADR index/filename consistency, migration-table mentions, and stale deferred wording
	@./scripts/check-docs.sh

check-frontend-permissions: ## Verify frontend nav permission strings match backend catalog
	@./scripts/check-frontend-permissions.sh

# Full heavy verification: DB tests, Docker image builds, vulnerability audit.
# Intended for main-branch CI / pre-release gates — not per-PR.
ci-heavy: test-db docker audit ## Run heavy CI checks (DB tests + Docker build + vuln scan)

clean: ## Remove build and coverage artifacts
	rm -rf bin coverage.out coverage.html
