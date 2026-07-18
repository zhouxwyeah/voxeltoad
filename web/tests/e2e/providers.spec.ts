import { expect, test, type Locator } from "@playwright/test";
import { sealData } from "iron-session";

/**
 * Slice 0 e2e (design/frontend.md §9): drives the whole stack in a real browser
 * — browser → Next (RSC/Server Actions + encrypted-cookie session) → admin API.
 * Proves login, RSC list, Server Action create/delete, and the 401→login guard.
 *
 * Credentials come from the adminstack bootstrap (scripts/web-e2e.sh sets them);
 * defaults match cmd/adminstack.
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

// Unique provider name per run so repeated runs against the same DB don't clash.
const providerName = `e2e-prov-${Date.now()}`;

/**
 * comboboxFor locates the custom Select primitive
 * (web/src/components/ui/select.tsx: a button[role=combobox] + Popover +
 * Command list, NOT a native <select> / <input> — .fill()/.selectOption() do
 * not apply) by the Select's own `name` prop — rendered as a sibling
 * `<input type="hidden" name={name}>` inside the same <label>. Note the
 * "Type" field's Select uses name="_type_select" (its own hidden input),
 * distinct from the outer name="type" hidden input the form submits
 * (providers/create-form.tsx) — target the Select's name, not the submitted
 * field name.
 */
function comboboxFor(container: Locator, selectName: string): Locator {
  return container.locator(`label:has(input[name="${selectName}"])`).getByRole("combobox");
}

/**
 * selectCombo picks an option from the custom Select primitive. Base UI's
 * Popover keeps its content mounted in a portal during the close transition
 * (`data-closed`), so a previously-opened Select sharing an option label
 * (e.g. "openai" appears in both the provider Type and Adapter dropdowns) can
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

test("unauthenticated visit to a dashboard route redirects to login", async ({
  page,
}) => {
  await page.goto("/providers");
  await expect(page).toHaveURL(/\/login$/);
});

test("login → create provider → see it → edit it → delete it", async ({ page }) => {
  // Login.
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');

  // Lands on providers.
  await expect(page).toHaveURL(/\/providers$/);
  await expect(page.getByRole("heading", { name: "Providers" })).toBeVisible();

  // --- Create (Modal) ---
  // Click "Create provider" to open the Modal.
  await page.getByRole("button", { name: "Create provider" }).click();
  const createModal = page.getByRole("dialog", { name: "Create provider" });
  await expect(createModal).toBeVisible();

  // Fill form inside the Modal.
  await createModal.getByLabel("Name *").fill(providerName);
  await selectCombo(comboboxFor(createModal, "_type_select"), "openai");
  await selectCombo(comboboxFor(createModal, "adapter"), "openai");
  await createModal.getByLabel("Base URL").fill("https://api.openai.com/v1");
  await createModal.getByLabel("API key ref").fill("env://E2E_KEY");
  await createModal.getByRole("button", { name: "Create" }).click();

  // Modal closes; row appears.
  await expect(createModal).not.toBeVisible();
  await expect(page.getByRole("cell", { name: providerName })).toBeVisible();

  // --- Edit (Modal) ---
  // Click "Edit" on the row to open edit Modal.
  const row = page.getByRole("row", { name: new RegExp(providerName) });
  await row.getByRole("button", { name: "Edit" }).click();
  const editModal = page.getByRole("dialog", { name: "Edit provider" });
  await expect(editModal).toBeVisible();

  // Name should be disabled; change type.
  await expect(editModal.getByLabel("Name *")).toBeDisabled();
  await selectCombo(comboboxFor(editModal, "_type_select"), "anthropic");
  await editModal.getByRole("button", { name: "Save" }).click();
  await expect(editModal).not.toBeVisible();

  // --- Delete (ConfirmModal) ---
  await row.getByRole("button", { name: "Delete" }).click();
  const confirmDialog = page.getByRole("dialog", { name: "Confirm Delete" });
  await expect(confirmDialog).toBeVisible();
  // Click confirm (destructive button).
  await confirmDialog.getByRole("button", { name: "Delete" }).click();

  // Row gone.
  await expect(page.getByRole("cell", { name: providerName })).toHaveCount(0);
});

test("sign out returns to login and protects dashboard", async ({ page }) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);

  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);

  // After sign-out, the dashboard is protected again.
  await page.goto("/providers");
  await expect(page).toHaveURL(/\/login$/);
});

// Regression: a stale session cookie (valid iron-session envelope, but the
// token inside is rejected by the admin API → 401 during RSC render) must
// redirect to /login, NOT throw a 500. RSC renders cannot modify cookies
// (only Server Actions / Route Handlers can), so onAuthExpired redirects to
// the /logout Route Handler which clears the cookie and bounces to /login.
// Before the fix, clearSession() was called from the RSC and Next threw
// "Cookies can only be modified in a Server Action or Route Handler" → 500.
test("stale session cookie (401 in RSC) redirects to login, not 500", async ({
  page,
  context,
}) => {
  const secret =
    process.env.SESSION_SECRET ?? "e2e-only-session-secret-min-32-chars-xxxxx";
  const sealed = await sealData(
    { token: "dead-token-the-admin-will-reject", email: "x@x", role: "super-admin" },
    { password: secret },
  );
  const origin = process.env.WEB_BASE_URL ?? "http://127.0.0.1:3000";
  await context.addCookies([
    {
      name: "voxeltoad_admin_session",
      value: sealed,
      url: origin,
    },
  ]);

  const pageErrors: string[] = [];
  page.on("pageerror", (e) => pageErrors.push(String(e)));

  await page.goto("/providers");
  // The /logout Route Handler clears the cookie and bounces to /login.
  await expect(page).toHaveURL(/\/login$/);
  expect(pageErrors.join("\n")).not.toContain(
    "Cookies can only be modified in a Server Action or Route Handler",
  );
});
