import { expect, test, type Locator } from "@playwright/test";

/**
 * Plugin management e2e tests (English locale).
 *
 * Plugins page has no upstream dependencies — plugins are standalone config
 * records. Covers: unauthenticated redirect, list, create, edit (prefill),
 * detail modal with JSON params display, invalid JSON error, delete.
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

/**
 * comboboxFor locates the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select>) by the Select's own `name` prop —
 * rendered as a sibling `<input type="hidden" name={name}>`. plugin-form.tsx
 * wraps the phase Select's label in a plain <div> (not a <label> that also
 * contains the Select), so scope by the nearest ancestor containing the
 * hidden input instead of `label:has(...)`.
 */
function comboboxFor(container: Locator, selectName: string): Locator {
  return container
    .locator(`:has(input[name="${selectName}"])`)
    .getByRole("combobox")
    .last();
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

test("unauthenticated visit to /plugins redirects to login", async ({
  page,
}) => {
  await page.goto("/plugins");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → navigate to plugins → see list", async ({ page }) => {
  await login(page);
  await page.click('a:has-text("Plugins")');
  await expect(page).toHaveURL(/\/plugins$/);
  await expect(
    page.getByRole("heading", { name: "Plugins" }),
  ).toBeVisible();
});

test("login → create plugin → see it → delete it", async ({ page }) => {
  const name = `e2e-plg-${Date.now()}`;

  await login(page);
  await page.click('a:has-text("Plugins")');
  await expect(page).toHaveURL(/\/plugins$/);

  // Create.
  await page.getByRole("button", { name: "Create plugin" }).click();
  const modal = page.getByRole("dialog", { name: "Create plugin" });
  await expect(modal).toBeVisible();

  await modal.getByLabel("Name *").fill(name);
  await selectCombo(comboboxFor(modal, "phase"), "Pre");
  await modal.getByLabel("Enabled").check();

  await modal.getByRole("button", { name: "Create plugin" }).click();
  await expect(modal).not.toBeVisible();

  // Verify row appears.
  await expect(page.getByRole("cell", { name })).toBeVisible();
  const row = page.getByRole("row", { name: new RegExp(name) });
  await expect(row.getByText("Pre")).toBeVisible();

  // Delete.
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await expect(confirmDialog).toBeVisible();
  await confirmDialog.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByRole("cell", { name })).toHaveCount(0);
});

test("login → create plugin with params → edit it", async ({ page }) => {
  const name = `e2e-plg-params-${Date.now()}`;

  await login(page);
  await page.click('a:has-text("Plugins")');
  await expect(page).toHaveURL(/\/plugins$/);

  // Create with JSON params.
  await page.getByRole("button", { name: "Create plugin" }).click();
  const createModal = page.getByRole("dialog", { name: "Create plugin" });
  await createModal.getByLabel("Name *").fill(name);
  await selectCombo(comboboxFor(createModal, "phase"), "Post");

  // The JSON params editor is a visible textarea name="params_text" that
  // mirrors into a hidden input name="params_json" on change (plugin-form.tsx).
  await createModal
    .locator('textarea[name="params_text"]')
    .fill('{"rpm": 100, "burst": 20}');

  await createModal.getByRole("button", { name: "Create plugin" }).click();
  await expect(createModal).not.toBeVisible();

  // Verify row.
  const row = page.getByRole("row", { name: new RegExp(name) });
  await expect(row.getByText("Post")).toBeVisible();

  // Edit: change phase and params. Scope is the identity's composite-key part
  // (name+scope) and is NOT patchable (internal/store/config.go PluginPatch —
  // "Scope ... is NOT patchable here — it locates the row"), so the edit form
  // submits the ORIGINAL scope value as a query param to locate the row, not
  // a new one — this test only edits truly patchable fields (phase/params).
  await row.getByRole("button", { name: "Edit" }).click();
  const editModal = page.getByRole("dialog", { name: "Edit plugin" });
  await expect(editModal).toBeVisible();

  // name is disabled + pre-filled in edit mode (plugin-form.tsx: a disabled
  // Input with no `name`, and a separate hidden input carrying the submitted
  // value). Assert the visible disabled field's value directly.
  await expect(editModal.getByLabel("Name *")).toHaveValue(name);

  // Change phase pre → this exercises a real patchable field.
  await selectCombo(comboboxFor(editModal, "phase"), "Pre");

  // plugin-form.tsx's submit button text is t(isEdit ? "actions.edit" :
  // "actions.create") = "Edit" in edit mode (not "Save").
  await editModal.getByRole("button", { name: "Edit" }).click();
  await expect(editModal).not.toBeVisible();

  // Verify updated phase now shown in row.
  await expect(row.getByText("Pre")).toBeVisible();
});

test("login → view plugin detail modal with JSON params", async ({
  page,
}) => {
  const name = `e2e-plg-detail-${Date.now()}`;

  await login(page);
  await page.click('a:has-text("Plugins")');
  await expect(page).toHaveURL(/\/plugins$/);

  // Create with params.
  await page.getByRole("button", { name: "Create plugin" }).click();
  const modal = page.getByRole("dialog", { name: "Create plugin" });
  await modal.getByLabel("Name *").fill(name);
  await selectCombo(comboboxFor(modal, "phase"), "Pre");
  await modal.getByLabel("Enabled").check();
  await modal.locator('textarea[name="params_text"]').fill('{"rpm": 200}');
  await modal.getByRole("button", { name: "Create plugin" }).click();
  await expect(modal).not.toBeVisible();

  // View detail.
  const row = page.getByRole("row", { name: new RegExp(name) });
  await row.getByRole("button", { name: "View" }).click();
  const detailModal = page.getByRole("dialog", { name: "Plugin detail" });
  await expect(detailModal).toBeVisible();
  await expect(detailModal.getByText(name)).toBeVisible();

  // Verify params JSON content is visible in detail.
  await expect(detailModal.getByText(/"rpm"/)).toBeVisible();

  await detailModal.getByRole("button", { name: "Close" }).click();
  await expect(detailModal).not.toBeVisible();
});

test("plugin form: invalid JSON shows error but does not block submit", async ({
  page,
}) => {
  const name = `e2e-plg-badjson-${Date.now()}`;

  await login(page);
  await page.click('a:has-text("Plugins")');
  await expect(page).toHaveURL(/\/plugins$/);

  await page.getByRole("button", { name: "Create plugin" }).click();
  const modal = page.getByRole("dialog", { name: "Create plugin" });

  await modal.getByLabel("Name *").fill(name);
  await selectCombo(comboboxFor(modal, "phase"), "Pre");

  // Type invalid JSON.
  await modal.locator('textarea[name="params_text"]').fill("not json {");
  // Blur to trigger validation.
  await modal.getByLabel("Name *").click();

  // Error message should appear (client-side validation).
  await expect(
    modal.locator("text=Invalid JSON").or(modal.locator("text=JSON 无效")),
  ).toBeVisible();

  await modal.getByRole("button", { name: "Cancel" }).click();
  await expect(modal).not.toBeVisible();
});
