import { expect, test, type Locator } from "@playwright/test";

/**
 * Operator management e2e tests (English locale, matching providers/tenants
 * spec convention — Chinese-locale coverage lives in locale.spec.ts).
 *
 * Operators page is super-admin only. We test:
 * 1. Unauthenticated redirect to login
 * 2. Login → navigate to operators → list is visible
 * 3. Create a tenant-admin operator → row appears → delete it
 * 4. Create a super-admin operator → row appears (no tenant select shown)
 * 5. Sign out protects /operators
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";
const operatorEmail = `e2e-op-${Date.now()}@test`;

/**
 * comboboxFor locates the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select>) by its FormData field `name` — the
 * component always renders `<input type="hidden" name={name}>` as a sibling
 * inside the same <label>. This is more robust than getByLabel(): once a
 * value is selected the combobox's accessible name becomes "<label
 * text><selected option text>" (label text + rendered content are
 * concatenated), so two fields whose labels/values share a word (e.g. "Role"
 * showing "Tenant Admin" vs the "Tenant" field itself) are NOT reliably
 * distinguishable via getByLabel — even with `exact: true`.
 */
function comboboxFor(container: Locator, fieldName: string): Locator {
  return container.locator(`label:has(input[name="${fieldName}"])`).getByRole("combobox");
}

/**
 * selectCombo picks an option from the custom Select primitive. Base UI's
 * Popover keeps its content mounted in a portal during the close transition
 * (`data-closed`), so a previously-opened Select sharing an option label can
 * still be present in the DOM when a second Select opens — scoping the option
 * search to the LAST (i.e. most-recently-opened) `[data-slot=popover-content]`
 * disambiguates which listbox is the active one.
 */
async function selectCombo(trigger: Locator, optionText: string) {
  await trigger.click();
  const popover = trigger.page().locator('[data-slot="popover-content"]').last();
  await popover.getByRole("option", { name: optionText, exact: true }).click();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
}

test("unauthenticated visit to /operators redirects to login", async ({
  page,
}) => {
  await page.goto("/operators");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → navigate to operators → see list", async ({ page }) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Navigate to operators via sidebar
  await page.click('a:has-text("Operators")');
  await expect(page).toHaveURL(/\/operators$/);
  await expect(page.getByRole("heading", { name: "Operators" })).toBeVisible();

  // The bootstrap super-admin should be in the list.
  await expect(page.getByRole("cell", { name: EMAIL })).toBeVisible();
});

test("login → create tenant-admin → see it → delete it", async ({ page }) => {
  const tenantName = `e2e-op-tenant-${Date.now()}`;

  // Login
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Ensure a tenant exists to pick from (self-contained; don't depend on
  // tenants.spec.ts having run first).
  await page.click('a:has-text("Tenants")');
  await expect(page).toHaveURL(/\/tenants$/);
  await page.getByRole("button", { name: "Create tenant" }).click();
  const tenantModal = page.getByRole("dialog", { name: "Create tenant" });
  await tenantModal.getByLabel("Name *").fill(tenantName);
  await tenantModal.getByRole("button", { name: "Create" }).click();
  await expect(tenantModal).not.toBeVisible();
  await expect(page.getByRole("cell", { name: tenantName })).toBeVisible();

  // Navigate to operators
  await page.click('a:has-text("Operators")');
  await expect(page).toHaveURL(/\/operators$/);

  // --- Create tenant-admin via Modal ---
  await page.getByRole("button", { name: "Create operator" }).click();
  const createModal = page.getByRole("dialog", { name: "Create Operator" });
  await expect(createModal).toBeVisible();

  // Fill form
  await createModal.getByLabel("Email").fill(operatorEmail);
  await createModal.getByLabel("Password").fill("test-password-123");

  // Select role: tenant-admin
  await selectCombo(comboboxFor(createModal, "_role_select"), "Tenant Admin");

  // Tenant dropdown should appear; select the tenant we just created.
  const tenantTrigger = comboboxFor(createModal, "tenant_id");
  await expect(tenantTrigger).toBeVisible();
  await selectCombo(tenantTrigger, tenantName);

  await createModal.getByRole("button", { name: "Save" }).click();
  await expect(createModal).not.toBeVisible();

  // Row appears
  await expect(page.getByRole("cell", { name: operatorEmail })).toBeVisible();

  // --- Edit the operator ---
  const editRow = page.getByRole("row", { name: new RegExp(operatorEmail) });
  await editRow.getByRole("button", { name: "Edit" }).click();
  const editModal = page.getByRole("dialog", { name: "Edit Operator" });
  await expect(editModal).toBeVisible();

  // Email is editable; change it.
  const newEmail = operatorEmail.replace("e2e-op-", "e2e-op-edited-");
  await editModal.getByLabel("Email").fill(newEmail);
  // Password is optional in edit mode; change it to verify the flow.
  await editModal.getByLabel(/New Password/).fill("edited-password-123");
  await editModal.getByRole("button", { name: "Save" }).click();
  await expect(editModal).not.toBeVisible();

  // Updated email appears in the list.
  await expect(page.getByRole("cell", { name: newEmail })).toBeVisible();
  // Old email is gone.
  await expect(
    page.getByRole("cell", { name: operatorEmail }),
  ).toHaveCount(0);

  // --- Delete ---
  const row = page.getByRole("row", { name: new RegExp(newEmail) });
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await expect(confirmDialog).toBeVisible();
  await confirmDialog.getByRole("button", { name: "Delete" }).click();

  // Row gone
  await expect(
    page.getByRole("cell", { name: operatorEmail }),
  ).toHaveCount(0);
});

test("login → create super-admin → see it (no tenant)", async ({ page }) => {
  const superAdminEmail = `e2e-sa-${Date.now()}@test`;

  // Login
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Navigate to operators
  await page.click('a:has-text("Operators")');
  await expect(page).toHaveURL(/\/operators$/);

  // --- Create super-admin via Modal ---
  await page.getByRole("button", { name: "Create operator" }).click();
  const createModal = page.getByRole("dialog", { name: "Create Operator" });
  await expect(createModal).toBeVisible();

  await createModal.getByLabel("Email").fill(superAdminEmail);
  await createModal.getByLabel("Password").fill("test-password-123");

  // Select role: super-admin (should be the default)
  await selectCombo(comboboxFor(createModal, "_role_select"), "Super Admin");

  // Tenant dropdown should NOT appear for a global-scope role.
  await expect(comboboxFor(createModal, "tenant_id")).toHaveCount(0);

  await createModal.getByRole("button", { name: "Save" }).click();
  await expect(createModal).not.toBeVisible();

  // Row appears
  await expect(
    page.getByRole("cell", { name: superAdminEmail }),
  ).toBeVisible();

  // Cleanup: delete the created super-admin
  const row = page.getByRole("row", { name: new RegExp(superAdminEmail) });
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await confirmDialog.getByRole("button", { name: "Delete" }).click();
  await expect(
    page.getByRole("cell", { name: superAdminEmail }),
  ).toHaveCount(0);
});

test("sign out returns to login and protects /operators", async ({ page }) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Navigate to operators
  await page.click('a:has-text("Operators")');
  await expect(page).toHaveURL(/\/operators$/);

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);

  // After sign-out, operators are protected.
  await page.goto("/operators");
  await expect(page).toHaveURL(/\/login$/);
});

test("create operator with custom role → login → verify permissions", async ({
  page,
}) => {
  const roleName = `e2e-custom-op-${Date.now()}`;
  const opEmail = `e2e-custom-op-${Date.now()}@test`;

  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Create a custom tenant-scoped role with limited permissions.
  await page.click('a:has-text("Roles")');
  await page.getByRole("button", { name: "Create Role" }).click();
  const roleModal = page.getByRole("dialog").first();

  await roleModal.getByPlaceholder("e.g. billing-viewer").fill(roleName);
  const scopeDiv = roleModal.locator("div").filter({ hasText: "Scope Kind" }).first();
  const scopeCombo = scopeDiv.locator('[role="combobox"]');
  await selectCombo(scopeCombo, "Tenant");
  await roleModal.getByText("Read usage stats").click();
  await roleModal.getByRole("button", { name: "Create" }).click();
  await expect(roleModal).not.toBeVisible();

  // Create a tenant for the operator.
  const tenantName = `e2e-custom-t-${Date.now()}`;
  await page.click('a:has-text("Tenants")');
  await page.getByRole("button", { name: "Create tenant" }).click();
  const tenantModal = page.getByRole("dialog", { name: "Create tenant" });
  await tenantModal.getByLabel("Name *").fill(tenantName);
  await tenantModal.getByRole("button", { name: "Create" }).click();
  await expect(tenantModal).not.toBeVisible();

  // Create operator with the custom role.
  await page.click('a:has-text("Operators")');
  await page.getByRole("button", { name: "Create operator" }).click();
  const opModal = page.getByRole("dialog", { name: "Create Operator" });
  await opModal.getByLabel("Email").fill(opEmail);
  await opModal.getByLabel("Password").fill("test-password-123");
  await selectCombo(comboboxFor(opModal, "_role_select"), roleName);
  await selectCombo(comboboxFor(opModal, "tenant_id"), tenantName);
  await opModal.getByRole("button", { name: "Save" }).click();
  await expect(opModal).not.toBeVisible();

  // Sign out and login as the custom-role operator.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.fill('input[name="email"]', opEmail);
  await page.fill('input[name="password"]', "test-password-123");
  await page.click('button[type="submit"]');

  // With only usage.read permission, should redirect to /usage.
  await expect(page).toHaveURL(/\/usage/);

  // Navigation: should NOT see providers/operators/tenants/roles (global-only).
  await expect(page.getByText("Providers", { exact: true })).toHaveCount(0);
  await expect(page.getByText("Operators", { exact: true })).toHaveCount(0);

  // Navigation: should see usage (has usage.read).
  await expect(page.getByText("Usage")).toBeVisible();
});
