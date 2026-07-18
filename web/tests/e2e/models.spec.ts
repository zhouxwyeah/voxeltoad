import { expect, test, type Locator } from "@playwright/test";

/**
 * Model management e2e tests (English locale, matching providers/tenants/
 * operators spec convention).
 *
 * Models page depends on providers existing (design/domain-flows.md §3):
 * "无 provider → 提示需先创建 provider"。Self-contained — creates its own
 * provider rather than depending on providers.spec.ts having run first
 * (same fix applied to operators.spec.ts for tenants).
 *
 * We test:
 * 1. Unauthenticated redirect to login
 * 2. Login → navigate to models → list is visible
 * 3. Create a model with 0 upstreams → row appears
 * 4. Create a model with 1 upstream (incl. pricing) → row + pill appears →
 *    delete it
 * 5. Add/remove upstream row interaction
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

/**
 * comboboxesFor locates ALL instances of the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select>) sharing the given `name` — the models
 * form renders one upstream row per candidate with the SAME
 * name="upstream_provider" on every row (see upstream-row.tsx), so index into
 * the result with `.nth(i)` to target a specific row.
 */
function comboboxesFor(container: Locator, selectName: string): Locator {
  return container.locator(`label:has(input[name="${selectName}"])`).getByRole("combobox");
}

/**
 * selectCombo picks an option from the custom Select primitive. Base UI's
 * Popover keeps its content mounted in a portal during the close transition,
 * so scope the option search to the LAST (most-recently-opened) popover to
 * avoid ambiguity with a still-closing sibling Select.
 */
async function selectCombo(trigger: Locator, optionText: string) {
  await trigger.click();
  const popover = trigger.page().locator('[data-slot="popover-content"]').last();
  await popover.getByRole("option", { name: optionText, exact: true }).click();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
}

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);
}

async function createProvider(
  page: import("@playwright/test").Page,
  name: string,
) {
  await expect(page.getByRole("heading", { name: "Providers" })).toBeVisible();
  await page.getByRole("button", { name: "Create provider" }).click();
  const modal = page.getByRole("dialog", { name: "Create provider" });
  await modal.getByLabel("Name *").fill(name);
  // adapter and base_url are required by the admin API even though the
  // OpenAPI spec marks only `name` as required (internal/admin/crud_provider.go)
  // — fill them so the create actually succeeds.
  await selectCombo(comboboxesFor(modal, "adapter").first(), "openai");
  await modal.getByLabel("Base URL").fill("https://api.openai.com/v1");
  await modal.getByRole("button", { name: "Create" }).click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole("cell", { name })).toBeVisible();
}

test("unauthenticated visit to /models redirects to login", async ({
  page,
}) => {
  await page.goto("/models");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → navigate to models → see list", async ({ page }) => {
  await login(page);
  await page.click('a:has-text("Models")');
  await expect(page).toHaveURL(/\/models$/);
  await expect(page.getByRole("heading", { name: "Models" })).toBeVisible();
});

test("login → create model with 0 upstreams → see it", async ({ page }) => {
  const providerName = `e2e-model-prov0-${Date.now()}`;
  const alias = `e2e-model-${Date.now()}`;

  await login(page);

  // Self-contained: the "Create model" button is hidden until at least one
  // provider exists (design/domain-flows.md §3 guidance state) — don't
  // depend on providers.spec.ts / another models test having run first.
  await createProvider(page, providerName);

  await page.click('a:has-text("Models")');
  await expect(page).toHaveURL(/\/models$/);

  await page.getByRole("button", { name: "Create model" }).click();
  const modal = page.getByRole("dialog", { name: "Create model" });
  await expect(modal).toBeVisible();
  await modal.getByLabel("Alias *").fill(alias);
  await modal.getByRole("button", { name: "Create model" }).click();
  await expect(modal).not.toBeVisible();

  await expect(page.getByRole("cell", { name: alias })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(alias) });
  await expect(row.getByText("No upstreams")).toBeVisible();
});

test("login → create model with 1 upstream + pricing → see it → delete it", async ({
  page,
}) => {
  const providerName = `e2e-model-prov-${Date.now()}`;
  const alias = `e2e-model-full-${Date.now()}`;

  await login(page);

  // Self-contained: create the upstream provider this test needs.
  await createProvider(page, providerName);

  // Navigate to models
  await page.click('a:has-text("Models")');
  await expect(page).toHaveURL(/\/models$/);

  await page.getByRole("button", { name: "Create model" }).click();
  const modal = page.getByRole("dialog", { name: "Create model" });
  await expect(modal).toBeVisible();
  await modal.getByLabel("Alias *").fill(alias);

  // Add one upstream row and fill it.
  await modal.getByRole("button", { name: "Add upstream" }).click();
  await selectCombo(comboboxesFor(modal, "upstream_provider").first(), providerName);
  await modal.locator('input[name="upstream_model"]').fill("gpt-4o");
  await modal.locator('input[name="upstream_prompt_price"]').fill("0.03");
  await modal
    .locator('input[name="upstream_completion_price"]')
    .fill("0.06");

  await modal.getByRole("button", { name: "Create model" }).click();
  await expect(modal).not.toBeVisible();

  await expect(page.getByRole("cell", { name: alias })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(alias) });
  await expect(row.getByText(/gpt-4o/)).toBeVisible();
  await expect(row.getByText(/0\.03\/0\.06 per 1M/)).toBeVisible();

  // Delete
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await expect(confirmDialog).toBeVisible();
  await confirmDialog.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByRole("cell", { name: alias })).toHaveCount(0);
});

test("create form: add/remove upstream row interaction", async ({ page }) => {
  const providerName = `e2e-model-prov2-${Date.now()}`;

  await login(page);

  // Self-contained: needed so the "Create model" button is shown.
  await createProvider(page, providerName);

  await page.click('a:has-text("Models")');
  await expect(page).toHaveURL(/\/models$/);

  await page.getByRole("button", { name: "Create model" }).click();
  const modal = page.getByRole("dialog", { name: "Create model" });
  await expect(modal).toBeVisible();

  // No rows initially.
  await expect(comboboxesFor(modal, "upstream_provider")).toHaveCount(0);

  // Add two rows.
  await modal.getByRole("button", { name: "Add upstream" }).click();
  await modal.getByRole("button", { name: "Add upstream" }).click();
  await expect(comboboxesFor(modal, "upstream_provider")).toHaveCount(2);

  // Remove the first row; one remains.
  await modal.getByRole("button", { name: "Remove" }).first().click();
  await expect(comboboxesFor(modal, "upstream_provider")).toHaveCount(1);

  await modal.getByRole("button", { name: "Cancel" }).click();
  await expect(modal).not.toBeVisible();
});
