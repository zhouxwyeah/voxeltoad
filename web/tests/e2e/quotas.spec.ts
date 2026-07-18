import { expect, test, type Locator, type Page } from "@playwright/test";

const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";
const tenantName = `e2e-qta-tenant-${Date.now()}`;
const taEmail = `e2e-qta-${Date.now()}@test`;

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
 * Setup: create tenant + tenant-admin via super-admin, login as tenant-admin.
 */
async function setupTenantAdmin(page: Page) {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  await page.click('a:has-text("Tenants")');
  await page.getByRole("button", { name: "Create tenant" }).click();
  const tenantModal = page.getByRole("dialog", { name: "Create tenant" });
  await tenantModal.getByLabel("Name *").fill(tenantName);
  await tenantModal.getByRole("button", { name: "Create" }).click();
  await expect(tenantModal).not.toBeVisible();

  await page.click('a:has-text("Operators")');
  await page.getByRole("button", { name: "Create operator" }).click();
  const opModal = page.getByRole("dialog", { name: "Create Operator" });
  await opModal.getByLabel("Email").fill(taEmail);
  await opModal.getByLabel("Password").fill("test-password-123");
  await selectCombo(comboboxFor(opModal, "role"), "Tenant Admin");
  await selectCombo(comboboxFor(opModal, "tenant_id"), tenantName);
  await opModal.getByRole("button", { name: "Save" }).click();
  await expect(opModal).not.toBeVisible();

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.fill('input[name="email"]', taEmail);
  await page.fill('input[name="password"]', "test-password-123");
  await page.click('button[type="submit"]');
}

test("tenant-admin: see own quota page with zero balance", async ({
  page,
}) => {
  await setupTenantAdmin(page);
  await expect(page).toHaveURL(/\/api-keys$/);

  // Navigate to quotas.
  await page.click('a:has-text("Quota")');
  await expect(page).toHaveURL(/\/quotas$/);

  // Should see the heading and scope.
  await expect(page.getByRole("heading", { name: "Quota" })).toBeVisible();
  await expect(page.getByRole("cell", { name: `tenant:${tenantName}` })).toBeVisible();

  // Should see the refresh button.
  await expect(page.getByRole("button", { name: "Refresh" })).toBeVisible();
});

test("tenant-admin: quotas page not visible to super-admin nav", async ({
  page,
}) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Super-admin nav should NOT show quotas link.
  await expect(page.locator('a:has-text("Quota")')).toHaveCount(0);
  await expect(page.locator('a:has-text("Groups")')).toHaveCount(0);
  await expect(page.locator('a:has-text("API Keys")')).toHaveCount(0);
});
