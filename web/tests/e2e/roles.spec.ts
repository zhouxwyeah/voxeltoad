import { expect, test, type Locator } from "@playwright/test";

const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

/**
 * selectCombo picks an option from the custom Select primitive, scoping to
 * the most-recently-opened popover.
 */
async function selectCombo(trigger: Locator, optionText: string) {
  await trigger.click();
  const popover = trigger.page().locator('[data-slot="popover-content"]').last();
  await popover.getByRole("option", { name: optionText, exact: true }).click();
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
}

test("unauthenticated visit to /roles redirects to login", async ({ page }) => {
  await page.goto("/roles");
  await expect(page).toHaveURL(/\/login/);
});

test("login → navigate to roles → see built-in roles", async ({ page }) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  await page.click('a:has-text("Roles")');
  await expect(page).toHaveURL(/\/roles$/);
  await expect(page.getByRole("heading", { name: "Roles" })).toBeVisible();

  // Built-in roles appear in the table.
  await expect(page.getByRole("cell", { name: "super-admin" })).toBeVisible();
  await expect(page.getByRole("cell", { name: "tenant-admin" })).toBeVisible();
});

test("login → create custom role → see it → delete it", async ({ page }) => {
  const roleName = `e2e-role-${Date.now()}`;

  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  await page.click('a:has-text("Roles")');
  await expect(page).toHaveURL(/\/roles$/);

  // Open create modal.
  await page.getByRole("button", { name: "Create Role" }).click();
  const createModal = page.getByRole("dialog").first();

  // Fill name (Input is bare <input>, not inside a <label>).
  await createModal.getByPlaceholder("e.g. billing-viewer").fill(roleName);

  // Select scope: Tenant. (Select is not inside a <label>, brute-force locate by
  // the sibling <label> text "Scope Kind" and find the combobox inside the same <div>).
  const scopeDiv = createModal.locator("div").filter({ hasText: "Scope Kind" }).first();
  const scopeCombo = scopeDiv.locator('[role="combobox"]');
  await selectCombo(scopeCombo, "Tenant");

  // Fill description.
  await createModal.getByPlaceholder("Optional description").fill("e2e test role");

  // Click the "usage" permission checkbox (label text from permission catalog).
  await createModal.getByText("Read usage stats").click();

  // Submit.
  await createModal.getByRole("button", { name: "Create" }).click();
  await expect(createModal).not.toBeVisible();

  // Role appears in the list.
  await expect(page.getByRole("cell", { name: roleName })).toBeVisible();

  // Edit: change description.
  const editRow = page.getByRole("row", { name: new RegExp(roleName) });
  await editRow.getByText("Edit").click();
  const editModal = page.getByRole("dialog").first();
  await expect(editModal).toBeVisible();

  // Name field is visible in edit modal (not disabled for custom roles).
  const nameInput = editModal.getByPlaceholder("e.g. billing-viewer");
  await expect(nameInput).toBeVisible();

  // Update description.
  const descInput = editModal.getByPlaceholder("Optional description");
  await descInput.clear();
  await descInput.fill("updated e2e description");

  // Add "audit" permission.
  await editModal.getByText("Read audit trail").click();

  await editModal.getByRole("button", { name: "Save" }).click();
  await expect(editModal).not.toBeVisible();

  // Delete the role.
  const deleteRow = page.getByRole("row", { name: new RegExp(roleName) });
  await deleteRow.getByText("Delete").click();
  // Confirm button appears.
  await deleteRow.getByText("Confirm").click();

  // Role disappears from the list.
  await expect(page.getByRole("cell", { name: roleName })).toHaveCount(0);
});

test("built-in role cannot be deleted", async ({ page }) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  await page.click('a:has-text("Roles")');
  await expect(page).toHaveURL(/\/roles$/);

  // super-admin row should NOT have a Delete button.
  const saRow = page.getByRole("row", { name: /super-admin/ });
  await expect(saRow.getByText("Delete")).toHaveCount(0);

  // tenant-admin row should NOT have a Delete button.
  const taRow = page.getByRole("row", { name: /tenant-admin/ });
  await expect(taRow.getByText("Delete")).toHaveCount(0);
});
