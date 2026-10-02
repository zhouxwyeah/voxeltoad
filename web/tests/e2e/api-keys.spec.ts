import { expect, test, type Locator } from "@playwright/test";

const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";
const tenantName = `e2e-api-tenant-${Date.now()}`;
const taEmail = `e2e-ta-${Date.now()}@test`;

/**
 * comboboxFor locates the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select>) by the Select's own `name` prop —
 * rendered as a sibling `<input type="hidden" name={name}>` inside the same
 * <label>.
 */
function comboboxFor(container: Locator, selectName: string): Locator {
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

/**
 * Setup: create tenant + tenant-admin operator via super-admin, then login
 * as the tenant-admin to test the API keys page.
 */
test("tenant-admin: create API key → see it → revoke → gone", async ({
  page,
}) => {
  // --- Login as super-admin to create tenant + tenant-admin ---
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Create tenant
  await page.click('a:has-text("Tenants")');
  await page.getByRole("button", { name: "Create tenant" }).click();
  const tenantModal = page.getByRole("dialog", { name: "Create tenant" });
  await tenantModal.getByLabel("Name *").fill(tenantName);
  await tenantModal.getByRole("button", { name: "Create" }).click();
  await expect(tenantModal).not.toBeVisible();

  // Create tenant-admin operator
  await page.click('a:has-text("Operators")');
  await page.getByRole("button", { name: "Create operator" }).click();
  const opModal = page.getByRole("dialog", { name: "Create Operator" });
  await opModal.getByLabel("Email").fill(taEmail);
  await opModal.getByLabel("Password").fill("test-password-123");
  await selectCombo(comboboxFor(opModal, "_role_select"), "Tenant Admin");
  await selectCombo(comboboxFor(opModal, "tenant_id"), tenantName);
  await opModal.getByRole("button", { name: "Save" }).click();
  await expect(opModal).not.toBeVisible();

  // --- Sign out and login as tenant-admin ---
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.fill('input[name="email"]', taEmail);
  await page.fill('input[name="password"]', "test-password-123");
  await page.click('button[type="submit"]');

  // Should redirect to /api-keys (tenant-admin's first page).
  await expect(page).toHaveURL(/\/api-keys$/);

  // Dependencies are explicit; an owner group need not be the consumption group.
  await expect(page.getByRole("link", { name: "Create a consumption group first" })).toBeVisible();
  const ownerGroup = `e2e-owner-${Date.now()}`;
  const consumptionGroup = `e2e-consumer-${Date.now()}`;
  for (const name of [ownerGroup, consumptionGroup]) {
    await page.goto("/groups");
    await page.getByRole("button", { name: "Create Group" }).click();
    const modal = page.getByRole("dialog", { name: "Create Group" });
    await modal.getByLabel("Name *").fill(name);
    await modal.getByRole("button", { name: "Save" }).click();
    await expect(modal).not.toBeVisible();
  }
  const applicationName = `e2e-key-app-${Date.now()}`;
  await page.goto("/applications");
  await page.getByRole("button", { name: "Create Application" }).click();
  const appModal = page.getByRole("dialog", { name: "Create Application" });
  await appModal.getByLabel("Name *").fill(applicationName);
  await selectCombo(comboboxFor(appModal, "owner_group"), ownerGroup);
  await appModal.getByRole("button", { name: "Save" }).click();
  await expect(appModal).not.toBeVisible();
  await page.goto("/api-keys");

  // --- Create an API key ---
  // api_keys.key_id has a global UNIQUE constraint (not per-tenant), so use a
  // unique value to avoid collisions across test runs / tenants.
  const keyId = `e2e-key-${Date.now()}`;
  await page.getByRole("button", { name: "Create Key" }).click();
  const createModal = page.getByRole("dialog", { name: "Create API Key" });
  await createModal.getByLabel("Key ID *").fill(keyId);
  await expect(createModal.getByRole("button", { name: "Create Key" })).toBeDisabled();
  await selectCombo(comboboxFor(createModal, "group_id"), consumptionGroup);
  await selectCombo(comboboxFor(createModal, "application_id"), applicationName);
  await selectCombo(comboboxFor(createModal, "environment"), "prod");
  await createModal.getByRole("button", { name: "Create Key" }).click();

  // One-time plaintext reveal modal appears.
  const plaintextModal = page.getByRole("dialog", { name: "API Key Created" });
  await expect(plaintextModal).toBeVisible();
  await expect(plaintextModal.getByText("sk-")).toBeVisible();

  // Dismiss by clicking "I've Saved It".
  await plaintextModal.getByRole("button", { name: "I've Saved It" }).click();
  await expect(plaintextModal).not.toBeVisible();

  // Key appears in the list.
  await expect(page.getByRole("cell", { name: keyId })).toBeVisible();

  // --- Edit the API key ---
  const editRow = page.getByRole("row", { name: new RegExp(keyId) });
  await editRow.getByRole("button", { name: "Edit" }).click();
  const editModal = page.getByRole("dialog", { name: "Edit API Key" });
  await expect(editModal).toBeVisible();

  // key_id is disabled (identifier cannot change) and shows original value.
  const keyIdInput = editModal.getByLabel("Key ID *");
  await expect(keyIdInput).toBeDisabled();
  await expect(keyIdInput).toHaveValue(keyId);

  await expect(editModal.getByText("This identity is fixed.", { exact: false })).toBeVisible();
  await expect(comboboxFor(editModal, "group_id")).toHaveCount(0);
  await expect(comboboxFor(editModal, "application_id")).toHaveCount(0);
  await expect(comboboxFor(editModal, "environment")).toHaveCount(0);

  // Cancel closes without changes.
  await editModal.getByRole("button", { name: "Cancel" }).click();
  await expect(editModal).not.toBeVisible();
  await expect(page.getByRole("cell", { name: keyId })).toBeVisible();

  await page.getByRole("link", { name: "Unbound API keys", exact: true }).click();
  await expect(page).toHaveURL(/unbound=true/);
  await expect(page.getByRole("cell", { name: keyId })).toHaveCount(0);
  await page.getByRole("link", { name: "All API keys", exact: true }).click();
  await expect(page.getByRole("row", { name: new RegExp(keyId) }).getByRole("cell", { name: "prod", exact: true })).toBeVisible();

  // --- Revoke the key ---
  const row = page.getByRole("row", { name: new RegExp(keyId) });
  await row.getByRole("button", { name: "Revoke" }).click();
  const confirmModal = page.getByRole("dialog", { name: "Revoke" });
  await confirmModal.getByRole("button", { name: "Revoke" }).click();

  // Key disappears from the list.
  await expect(page.getByRole("cell", { name: keyId })).toHaveCount(0);
});
