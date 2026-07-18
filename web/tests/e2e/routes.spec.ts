import { expect, test, type Locator } from "@playwright/test";

/**
 * Route management e2e tests (English locale).
 *
 * Routes page depends on both providers and models existing
 * (design/domain-flows.md: Route.Providers ⊆ Model.Upstreams).
 * Self-contained — creates its own provider + model before each route test.
 *
 * Covers: unauthenticated redirect, list, create, edit (prefill), detail
 * modal, add/remove provider row interaction, delete.
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

/**
 * comboboxesFor locates the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select>) by the Select's own `name` prop —
 * rendered as a sibling `<input type="hidden" name={name}>` inside the same
 * <label>. Routes may render multiple provider rows sharing the same
 * name="route_provider_name" (route-provider-row.tsx), so index into the
 * result with `.nth(i)`/`.first()` to target a specific row.
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
  // OpenAPI spec marks only `name` as required (internal/admin/crud_provider.go).
  await selectCombo(comboboxesFor(modal, "adapter").first(), "openai");
  await modal.getByLabel("Base URL").fill("https://api.openai.com/v1");
  await modal.getByRole("button", { name: "Create" }).click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole("cell", { name })).toBeVisible();
}

async function createModel(
  page: import("@playwright/test").Page,
  alias: string,
  providerName: string,
) {
  await page.click('a:has-text("Models")');
  await expect(page).toHaveURL(/\/models$/);
  await page.getByRole("button", { name: "Create model" }).click();
  const modal = page.getByRole("dialog", { name: "Create model" });
  await modal.getByLabel("Alias *").fill(alias);
  await modal.getByRole("button", { name: "Add upstream" }).click();
  await selectCombo(comboboxesFor(modal, "upstream_provider").first(), providerName);
  await modal.locator('input[name="upstream_model"]').fill("gpt-4o");
  await modal.getByRole("button", { name: "Create model" }).click();
  await expect(modal).not.toBeVisible();
  await expect(page.getByRole("cell", { name: alias })).toBeVisible();
}

test("unauthenticated visit to /routes redirects to login", async ({
  page,
}) => {
  await page.goto("/routes");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → navigate to routes → see list", async ({ page }) => {
  await login(page);
  await page.click('a:has-text("Routes")');
  await expect(page).toHaveURL(/\/routes$/);
  await expect(page.getByRole("heading", { name: "Routes" })).toBeVisible();
});

test("login → create route with 1 provider → see it → delete it", async ({
  page,
}) => {
  const providerName = `e2e-route-prov-${Date.now()}`;
  const modelAlias = `e2e-route-m-${Date.now()}`;

  await login(page);

  // Self-contained: create provider + model.
  await createProvider(page, providerName);
  await createModel(page, modelAlias, providerName);

  // Navigate to routes.
  await page.click('a:has-text("Routes")');
  await expect(page).toHaveURL(/\/routes$/);

  // Create route.
  await page.getByRole("button", { name: "Create route" }).click();
  const modal = page.getByRole("dialog", { name: "Create route" });
  await expect(modal).toBeVisible();

  // Select model_alias.
  await selectCombo(comboboxesFor(modal, "model_alias"), modelAlias);
  // Select strategy.
  await selectCombo(comboboxesFor(modal, "strategy"), "Priority");
  // Add provider row.
  await modal.getByRole("button", { name: "Add provider" }).click();
  await selectCombo(comboboxesFor(modal, "route_provider_name").first(), providerName);

  await modal.getByRole("button", { name: "Create route" }).click();
  await expect(modal).not.toBeVisible();

  // Verify created route appears.
  await expect(
    page.getByRole("cell", { name: modelAlias }),
  ).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(modelAlias) });
  await expect(row.getByText("Priority")).toBeVisible();

  // Delete.
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await expect(confirmDialog).toBeVisible();
  await confirmDialog.getByRole("button", { name: "Delete" }).click();
  await expect(
    page.getByRole("cell", { name: modelAlias }),
  ).toHaveCount(0);
});

test("login → create route → edit it", async ({ page }) => {
  const providerName = `e2e-route-prov-e-${Date.now()}`;
  const modelAlias = `e2e-route-me-${Date.now()}`;

  await login(page);
  await createProvider(page, providerName);
  await createModel(page, modelAlias, providerName);

  await page.click('a:has-text("Routes")');
  await expect(page).toHaveURL(/\/routes$/);

  // Create with "weighted" strategy.
  await page.getByRole("button", { name: "Create route" }).click();
  const createModal = page.getByRole("dialog", { name: "Create route" });
  await selectCombo(comboboxesFor(createModal, "model_alias"), modelAlias);
  await selectCombo(comboboxesFor(createModal, "strategy"), "Weighted");
  await createModal.getByRole("button", { name: "Add provider" }).click();
  await selectCombo(comboboxesFor(createModal, "route_provider_name").first(), providerName);
  await createModal.locator('input[name="route_provider_weight"]').fill("5");
  await createModal.getByRole("button", { name: "Create route" }).click();
  await expect(createModal).not.toBeVisible();

  // Verify "Weighted" pill appears.
  const row = page.getByRole("row", { name: new RegExp(modelAlias) });
  await expect(row.getByText("Weighted")).toBeVisible();

  // Edit: change strategy to "Round robin".
  await row.getByRole("button", { name: "Edit" }).click();
  const editModal = page.getByRole("dialog", { name: "Edit route" });
  await expect(editModal).toBeVisible();

  // model_alias is disabled + pre-filled in edit mode (route-form.tsx: a
  // disabled Input with no `name`, and a separate hidden input carrying the
  // submitted value). Assert the visible disabled field's value directly.
  await expect(editModal.getByLabel("Model alias *")).toHaveValue(modelAlias);

  // Change strategy.
  await selectCombo(comboboxesFor(editModal, "strategy"), "Round robin");
  // route-form.tsx's submit button text is generic tCommon("actions.edit") =
  // "Edit" in edit mode (not "Save" — only the Operator/Provider forms use
  // "Save"; Routes reuses the common "Edit" action label).
  await editModal.getByRole("button", { name: "Edit" }).click();
  await expect(editModal).not.toBeVisible();

  // Verify updated pill.
  await expect(row.getByText("Round robin")).toBeVisible();
});

test("login → view route detail modal", async ({ page }) => {
  const providerName = `e2e-route-prov-d-${Date.now()}`;
  const modelAlias = `e2e-route-md-${Date.now()}`;

  await login(page);
  await createProvider(page, providerName);
  await createModel(page, modelAlias, providerName);

  await page.click('a:has-text("Routes")');
  await expect(page).toHaveURL(/\/routes$/);

  // Create route.
  await page.getByRole("button", { name: "Create route" }).click();
  const modal = page.getByRole("dialog", { name: "Create route" });
  await selectCombo(comboboxesFor(modal, "model_alias"), modelAlias);
  await selectCombo(comboboxesFor(modal, "strategy"), "Session affinity");
  await modal.getByRole("button", { name: "Add provider" }).click();
  await selectCombo(comboboxesFor(modal, "route_provider_name").first(), providerName);
  await modal.getByRole("button", { name: "Create route" }).click();
  await expect(modal).not.toBeVisible();

  // View detail.
  const row = page.getByRole("row", { name: new RegExp(modelAlias) });
  await row.getByRole("button", { name: "View" }).click();
  const detailModal = page.getByRole("dialog", { name: "Route detail" });
  await expect(detailModal).toBeVisible();
  await expect(detailModal.getByText(modelAlias)).toBeVisible();
  // routes/client.tsx renders the human label t(`strategy.${strategy}`)
  // ("Session affinity"), not the raw API enum value ("session_affinity").
  await expect(detailModal.getByText(/session affinity/i)).toBeVisible();

  // Close detail.
  await detailModal.getByRole("button", { name: "Close" }).click();
  await expect(detailModal).not.toBeVisible();
});

test("create form: add/remove provider row interaction", async ({ page }) => {
  const providerName = `e2e-route-prov2-${Date.now()}`;
  const modelAlias = `e2e-route-m2-${Date.now()}`;

  await login(page);
  await createProvider(page, providerName);
  await createModel(page, modelAlias, providerName);

  await page.click('a:has-text("Routes")');
  await expect(page).toHaveURL(/\/routes$/);

  await page.getByRole("button", { name: "Create route" }).click();
  const modal = page.getByRole("dialog", { name: "Create route" });
  await expect(modal).toBeVisible();

  // No provider rows initially.
  await expect(comboboxesFor(modal, "route_provider_name")).toHaveCount(0);

  // Add two rows.
  await modal.getByRole("button", { name: "Add provider" }).click();
  await modal.getByRole("button", { name: "Add provider" }).click();
  await expect(comboboxesFor(modal, "route_provider_name")).toHaveCount(2);

  // Remove the first row; one remains.
  await modal.getByRole("button", { name: "Remove" }).first().click();
  await expect(comboboxesFor(modal, "route_provider_name")).toHaveCount(1);

  await modal.getByRole("button", { name: "Cancel" }).click();
  await expect(modal).not.toBeVisible();
});
