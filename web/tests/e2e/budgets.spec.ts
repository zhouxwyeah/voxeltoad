import { expect, test, type Locator, type Page } from "@playwright/test";
import { createAdminClient, unwrap, unwrapJson, type AdminClient } from "@voxeltoad/gateway-sdk/admin";

const ADMIN_URL = process.env.ADMIN_URL ?? "http://127.0.0.1:8090";
const GATEWAY_URL = process.env.GATEWAY_URL ?? "http://127.0.0.1:12800";
const MOCK_CONTROL_URL = process.env.MOCK_CONTROL_URL ?? "http://127.0.0.1:8091";
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";
const TEST_PASSWORD = "budget-e2e-password-123";
const unique = () => `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

async function adminLogin(email = EMAIL, password = PASSWORD) {
  const anon = createAdminClient({ baseUrl: ADMIN_URL });
  const auth = unwrap(await anon.POST("/auth/login", { body: { email, password } }));
  return createAdminClient({ baseUrl: ADMIN_URL, token: auth.token });
}

async function browserLogin(page: Page, email = EMAIL, password = PASSWORD) {
  await page.goto("/login");
  await page.locator('input[name="email"]').fill(email);
  await page.locator('input[name="password"]').fill(password);
  await page.locator('button[type="submit"]').click();
  await expect(page).not.toHaveURL(/\/login$/);
}

function combo(container: Locator, name: string) {
  return container.locator(`label:has(input[name="${name}"])`).getByRole("combobox");
}

async function selectCombo(trigger: Locator, label: string) {
  await trigger.click();
  await trigger.page().locator('[data-slot="popover-content"]').last().getByRole("option", { name: label, exact: true }).click();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
}

async function createPolicy(client: AdminClient, tenant: string, name: string) {
  return unwrap(await client.POST("/api/v1/budgets", {
    params: { query: { tenant } },
    body: { name, scope_kind: "tenant", scope_ref: "", environment: "", period: "monthly", timezone: "UTC", currency: "usd", limit: 100_000_000, mode: "enforce", thresholds: [80, 100] },
  }));
}

test("budgets require login", async ({ page }) => {
  await page.goto("/budgets");
  await expect(page).toHaveURL(/\/login/);
});

test("global budget lifecycle preserves micro-units, requires tenant and checks stale versions", async ({ page }) => {
  const root = await adminLogin();
  const tenant = unwrap(await root.POST("/api/v1/tenants", { body: { name: `budget-ui-${unique()}` } }));
  const name = `monthly-${unique()}`;
  await browserLogin(page);
  await page.goto("/budgets");
  await expect(page.getByText("Select a tenant to view budgets.", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create budget", exact: true })).toHaveCount(0);
  expect((await root.GET("/api/v1/budgets")).response.status).toBe(400);

  await page.locator('input[name="tenant"]').fill(tenant.name);
  await page.getByRole("button", { name: "Apply tenant" }).click();
  await expect(page.getByText("No budget policies for this tenant.")).toBeVisible();
  await page.getByRole("button", { name: "Create budget", exact: true }).click();
  const create = page.getByRole("dialog", { name: "Create budget", exact: true });
  await create.getByLabel("Name", { exact: true }).fill(name);
  await create.getByLabel("Limit", { exact: true }).fill("12.345678");
  await expect(create.getByText(/Actual cost may exceed/)).toBeVisible();
  await create.getByRole("button", { name: "Save", exact: true }).click();
  await expect(create).not.toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(name) });
  await expect(row).toContainText("12.35 USD");
  let policy = unwrap(await root.GET("/api/v1/budgets", { params: { query: { tenant: tenant.name } } })).data[0];
  expect(policy.limit).toBe(12_345_678);

  await page.getByRole("button", { name: "Create budget", exact: true }).click();
  await create.getByLabel("Name", { exact: true }).fill(`invalid-${unique()}`);
  await create.getByLabel("Limit", { exact: true }).fill("1");
  await create.getByLabel("IANA timezone", { exact: true }).fill("Invalid/Timezone");
  await create.getByRole("button", { name: "Save", exact: true }).click();
  await expect(create.getByRole("alert")).toBeVisible();
  await expect(create.getByRole("alert")).not.toContainText("errors.");
  await create.getByRole("button", { name: "Cancel", exact: true }).click();

  await row.getByRole("button", { name: "Edit", exact: true }).click();
  let edit = page.getByRole("dialog", { name: "Edit budget", exact: true });
  await selectCombo(combo(edit, "enabled"), "Disabled");
  await edit.getByRole("button", { name: "Save", exact: true }).click();
  const confirmation = page.getByRole("dialog", { name: "Confirm budget update", exact: true });
  await expect(confirmation.getByText(/does not reset the period/)).toBeVisible();
  await confirmation.getByRole("button", { name: "Save", exact: true }).click();
  await expect(edit).not.toBeVisible();
  await expect(row).toContainText("Disabled");
  policy = unwrap(await root.GET("/api/v1/budgets/{id}", { params: { path: { id: policy.id }, query: { tenant: tenant.name } } }));
  expect(policy.limit).toBe(12_345_678);
  expect(policy.enabled).toBe(false);

  await row.getByRole("button", { name: "Edit", exact: true }).click();
  edit = page.getByRole("dialog", { name: "Edit budget", exact: true });
  await edit.getByLabel("Limit", { exact: true }).fill("20.00");
  unwrap(await root.PATCH("/api/v1/budgets/{id}", { params: { path: { id: policy.id }, query: { tenant: tenant.name } }, body: { version: policy.version, enabled: true } }));
  await edit.getByRole("button", { name: "Save", exact: true }).click();
  await confirmation.getByRole("button", { name: "Save", exact: true }).click();
  await expect(confirmation.getByRole("alert")).toBeVisible();
  await expect(confirmation).toBeVisible();
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click();
  await edit.getByRole("button", { name: "Cancel", exact: true }).click();

  await row.getByRole("link", { name: "Accounts", exact: true }).click();
  await expect(page.getByText("No period accounts yet.")).toBeVisible();
  await expect(page.getByText(/Reserved includes holds for unknown charges/)).toBeVisible();
  await page.getByRole("link", { name: "Reconciliation", exact: true }).click();
  await expect(page.getByText("No reservations match this status.")).toBeVisible();
  await expect(page.getByText(/Risk-released records still have unknown cost/)).toBeVisible();
  await selectCombo(combo(page.locator("main"), "status"), "Risk released · cost unknown");
  await expect(page).toHaveURL(/status=released_unknown/);
  await expect(page.getByText("No reservations match this status.")).toBeVisible();
  await selectCombo(combo(page.locator("main"), "status"), "All statuses");
  await expect(page.getByText("No reservations match this status.")).toBeVisible();
});

test("UI budget accounts and events reflect gateway settlement and reject exhausted spend", async ({ page, request }) => {
  const root = await adminLogin();
  const suffix = unique();
  const tenant = unwrap(await root.POST("/api/v1/tenants", { body: { name: `budget-flow-${suffix}` } }));
  const email = `budget-flow-${suffix}@test`;
  unwrap(await root.POST("/api/v1/operators", { body: { email, password: TEST_PASSWORD, role: "tenant-admin", tenant_id: tenant.id } }));
  const tenantClient = await adminLogin(email, TEST_PASSWORD);
  const owner = unwrap(await tenantClient.POST("/api/v1/groups", { body: { name: "owners" } }));
  const consumer = unwrap(await tenantClient.POST("/api/v1/groups", { body: { name: "consumers" } }));
  const application = unwrap(await tenantClient.POST("/api/v1/applications", { body: { name: "workload", owner_group: owner.name } }));
  expect(application.owner_group_id).toBe(owner.id);
  expect(application.owner_group_id).not.toBe(consumer.id);
  const key = unwrap(await tenantClient.POST("/api/v1/api-keys", {
    body: { key_id: `budget-key-${suffix}`, group_id: consumer.id, application_id: application.id, environment: "prod", allowed_models: ["chat"] },
  }));
  const quotaScope = `tenant:${tenant.name}`;
  unwrap(await root.POST("/api/v1/quotas/topup", { body: { scope: quotaScope, delta: 1_000_000, currency: "usd" } }));

  await browserLogin(page);
  await page.goto(`/budgets?tenant=${tenant.name}`);
  await page.getByRole("button", { name: "Create budget", exact: true }).click();
  const create = page.getByRole("dialog", { name: "Create budget", exact: true });
  const name = `gateway-${suffix}`;
  await create.getByLabel("Name", { exact: true }).fill(name);
  await selectCombo(combo(create, "scope_kind"), "Application + environment");
  await create.getByLabel("Scope reference ID", { exact: true }).fill(String(application.id));
  await expect(combo(create, "environment")).toHaveText("Production");
  await create.getByLabel("Limit", { exact: true }).fill("0.02");
  await create.getByLabel("Thresholds (%)", { exact: true }).fill("50,100");
  await create.getByRole("button", { name: "Save", exact: true }).click();
  await expect(create).not.toBeVisible();
  const policy = unwrap(await root.GET("/api/v1/budgets", { params: { query: { tenant: tenant.name } } })).data[0];
  expect(policy).toMatchObject({ name, scope_kind: "application_env", scope_ref: String(application.id), environment: "prod", limit: 20_000, mode: "enforce" });
  await page.getByRole("row", { name: new RegExp(name) }).getByRole("link", { name: "Accounts", exact: true }).click();
  await expect(page.getByText("No period accounts yet.")).toBeVisible();

  const content = `budget-settled-${suffix}`;
  const usage = { prompt_tokens: 10_000, completion_tokens: 10_000, total_tokens: 20_000 };
  const mock = await request.post(`${MOCK_CONTROL_URL}/__set`, { data: { content, usage } });
  expect(mock.status()).toBe(204);
  try {
    // The seeded chat prices are 1/2 micros per input/output token. Only the
    // output estimate is reserved: actual 30,000 micros may exceed the limit.
    const completion = {
      headers: { Authorization: `Bearer ${key.api_key}` },
      data: { model: "chat", max_tokens: 1, messages: [{ role: "user", content: "budget flow" }] },
    };
    const response = await request.post(`${GATEWAY_URL}/v1/chat/completions`, completion);
    expect(response.status(), await response.text()).toBe(200);
    expect(await response.json()).toMatchObject({ choices: [{ message: { content } }], usage });
    const accounts = () => tenantClient.GET("/api/v1/budgets/{id}/accounts", { params: { path: { id: policy.id } } });
    await expect.poll(async () => unwrap(await accounts()).data[0]?.committed).toBe(30_000);
    const account = unwrap(await accounts()).data[0];
    expect(account).toMatchObject({ limit: 20_000, committed: 30_000, reserved: 0, available: -10_000, currency: "usd" });

    await page.reload();
    await expect(page.getByRole("columnheader", { name: "Committed", exact: true })).toBeVisible();
    const accountRow = page.getByRole("row").filter({ has: page.getByRole("cell", { name: String(account.id), exact: true }) });
    await expect(accountRow.getByRole("cell").nth(2)).toHaveText("0.02 USD");
    await expect(accountRow.getByRole("cell").nth(3)).toHaveText("0.03 USD");
    await expect(accountRow.getByRole("cell").nth(4)).toHaveText("0.00 USD");
    await expect(accountRow.getByRole("cell").nth(6)).toHaveText("-0.01 USD");
    await expect(page.getByText("0.97 USD", { exact: true })).toBeVisible();
    await page.getByRole("link", { name: "Events", exact: true }).click();
    const thresholds = page.getByRole("row").filter({ has: page.getByRole("cell", { name: "Committed spend threshold", exact: true }) });
    await expect(thresholds).toHaveCount(2);
    await expect(thresholds.filter({ has: page.getByRole("cell", { name: "50%", exact: true }) })).toHaveCount(1);
    await expect(thresholds.filter({ has: page.getByRole("cell", { name: "100%", exact: true }) })).toHaveCount(1);
    await expect(page.getByRole("cell", { name: "Budget exceeded", exact: true })).toHaveCount(1);

    const rejected = await request.post(`${GATEWAY_URL}/v1/chat/completions`, completion);
    expect(rejected.status(), await rejected.text()).toBe(402);
    expect(await rejected.json()).toMatchObject({ error: { type: "insufficient_quota" } });
    expect(unwrap(await accounts()).data[0]).toMatchObject({ committed: 30_000, reserved: 0, available: -10_000 });
    expect(unwrap(await tenantClient.GET("/api/v1/quotas", { params: { query: { scope: quotaScope } } })).balance).toBe(970_000);
    const reservations = unwrap(await tenantClient.GET("/api/v1/billing-reservations")).data;
    expect(reservations).toHaveLength(1);
    expect(reservations[0]).toMatchObject({ status: "settled", estimate: 2, actual: 30_000, application_id: application.id, environment: "prod", group: consumer.name });
    const logs = () => tenantClient.GET("/api/v1/request-logs", { params: { query: { application_id: application.id, environment: "prod" } } });
    await expect.poll(async () => unwrapJson(await logs()).data?.length).toBe(2);
    expect(unwrapJson(await logs()).data).toEqual(expect.arrayContaining([
      expect.objectContaining({ request_id: rejected.headers()["x-request-id"], blocked_by: "billing", provider: "" }),
    ]));
    const billed = () => tenantClient.GET("/api/v1/usage", { params: { query: { application_id: application.id, environment: "prod" } } });
    await expect.poll(async () => unwrapJson(await billed()).data?.length).toBe(1);
    expect(unwrapJson(await billed()).data?.[0]).toMatchObject({ cost: 30_000, prompt_tokens: 10_000, completion_tokens: 10_000, group_name: consumer.name });

    // Refresh the browser after rejection: no duplicate settlement or threshold.
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(thresholds).toHaveCount(2);
    await expect(page.getByRole("cell", { name: "Budget exceeded", exact: true })).toHaveCount(1);
  } finally {
    expect((await request.post(`${MOCK_CONTROL_URL}/__reset`)).status()).toBe(204);
  }
});

test("tenant budget view ignores tenant override and cannot mutate or reconcile", async ({ page }) => {
  const root = await adminLogin();
  const suffix = unique();
  const tenant = unwrap(await root.POST("/api/v1/tenants", { body: { name: `budget-own-${suffix}` } }));
  const other = unwrap(await root.POST("/api/v1/tenants", { body: { name: `budget-other-${suffix}` } }));
  const ownPolicy = await createPolicy(root, tenant.name, `own-${suffix}`);
  const otherPolicy = await createPolicy(root, other.name, `other-${suffix}`);
  const email = `budget-tenant-${suffix}@test`;
  unwrap(await root.POST("/api/v1/operators", { body: { email, password: TEST_PASSWORD, role: "tenant-admin", tenant_id: tenant.id } }));
  const tenantClient = await adminLogin(email, TEST_PASSWORD);
  await browserLogin(page, email, TEST_PASSWORD);
  await page.goto(`/budgets?tenant=${other.name}`);
  await expect(page.getByRole("cell", { name: ownPolicy.name, exact: true })).toBeVisible();
  await expect(page.getByRole("cell", { name: otherPolicy.name, exact: true })).toHaveCount(0);
  await expect(page.locator('input[name="tenant"]')).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Create budget", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0);
  expect((await tenantClient.GET("/api/v1/budgets", { params: { query: { tenant: other.name } } })).response.status).toBe(403);
  const list = unwrap(await tenantClient.GET("/api/v1/budgets"));
  expect(list.data.map((policy) => policy.id)).toEqual([ownPolicy.id]);
  expect((await tenantClient.POST("/api/v1/budgets", { params: { query: { tenant: tenant.name } }, body: { name: "forbidden", scope_kind: "tenant", scope_ref: "", environment: "", period: "daily", timezone: "UTC", currency: "usd", limit: 1, mode: "enforce", thresholds: [] } })).response.status).toBe(403);
  expect((await tenantClient.PATCH("/api/v1/budgets/{id}", { params: { path: { id: ownPolicy.id }, query: { tenant: tenant.name } }, body: { version: ownPolicy.version, enabled: false } })).response.status).toBe(403);
  expect((await tenantClient.POST("/api/v1/billing-reservations/{id}/resolve", { params: { path: { id: "nonexistent-reservation" }, query: { tenant: tenant.name } }, body: { version: 1, action: "release_unknown", reason: "permission check", evidence: "no charge claim is made" } })).response.status).toBe(403);
  expect((await tenantClient.GET("/api/v1/budgets/{id}", { params: { path: { id: otherPolicy.id } } })).response.status).toBe(404);
});

test("global read-only budget role works without tenant catalog permission", async ({ page }) => {
  const root = await adminLogin();
  const suffix = unique();
  const tenant = unwrap(await root.POST("/api/v1/tenants", { body: { name: `budget-read-${suffix}` } }));
  const policy = await createPolicy(root, tenant.name, `read-only-${suffix}`);
  const role = unwrap(await root.POST("/api/v1/roles", { body: { name: `budget-reader-${suffix}`, scope_kind: "global", permissions: ["budget.read"] } }));
  const email = `budget-reader-${suffix}@test`;
  unwrap(await root.POST("/api/v1/operators", { body: { email, password: TEST_PASSWORD, role_id: role.id } }));
  await browserLogin(page, email, TEST_PASSWORD);
  await page.goto(`/budgets?tenant=${tenant.name}`);
  await expect(page.getByRole("cell", { name: policy.name, exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create budget", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0);
  const reader = await adminLogin(email, TEST_PASSWORD);
  expect((await reader.PATCH("/api/v1/budgets/{id}", { params: { path: { id: policy.id }, query: { tenant: tenant.name } }, body: { version: policy.version, limit: 1 } })).response.status).toBe(403);
});

test("operator without budget.read gets a localized forbidden page", async ({ page }) => {
  const root = await adminLogin();
  const suffix = unique();
  const role = unwrap(await root.POST("/api/v1/roles", { body: { name: `budget-denied-${suffix}`, scope_kind: "global", permissions: ["provider.read"] } }));
  const email = `budget-denied-${suffix}@test`;
  unwrap(await root.POST("/api/v1/operators", { body: { email, password: TEST_PASSWORD, role_id: role.id } }));
  await browserLogin(page, email, TEST_PASSWORD);
  await page.goto("/budgets");
  await expect(page.getByText("You do not have permission to read budgets.", { exact: true })).toBeVisible();
  await expect(page.locator("aside").getByRole("link", { name: "Budgets", exact: true })).toHaveCount(0);
  const restricted = await adminLogin(email, TEST_PASSWORD);
  expect((await restricted.GET("/api/v1/budgets")).response.status).toBe(403);
});
