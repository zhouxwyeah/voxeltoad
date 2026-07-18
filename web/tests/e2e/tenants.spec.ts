import { expect, test } from "@playwright/test";

const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";
const tenantName = `e2e-tenant-${Date.now()}`;

test("unauthenticated visit to /tenants redirects to login", async ({
  page,
}) => {
  await page.goto("/tenants");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → create tenant → see it enabled → disable → re-enable", async ({
  page,
}) => {
  // Login
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  // Navigate to tenants via sidebar
  await page.click('a:has-text("Tenants")');
  await expect(page).toHaveURL(/\/tenants$/);
  await expect(page.getByRole("heading", { name: "Tenants" })).toBeVisible();

  // Create via Modal
  await page.getByRole("button", { name: "Create tenant" }).click();
  const createModal = page.getByRole("dialog", { name: "Create tenant" });
  await expect(createModal).toBeVisible();
  await createModal.getByLabel("Name *").fill(tenantName);
  await createModal.getByRole("button", { name: "Create" }).click();
  await expect(createModal).not.toBeVisible();

  // Row appears, enabled by default.
  const row = page.getByRole("row", { name: new RegExp(tenantName) });
  await expect(row).toBeVisible();
  await expect(row.getByText("Enabled")).toBeVisible();

  // Disable requires confirmation (it stops every API key under the tenant).
  await row.getByRole("button", { name: "Disable" }).click();
  const disableModal = page.getByRole("dialog", { name: "Disable tenant" });
  await expect(disableModal).toBeVisible();
  await disableModal.getByRole("button", { name: "Disable" }).click();
  await expect(disableModal).not.toBeVisible();
  await expect(row.getByText("Disabled")).toBeVisible();

  // Re-enable is reversible and needs no confirmation.
  await row.getByRole("button", { name: "Enable" }).click();
  await expect(row.getByText("Enabled")).toBeVisible();
});
