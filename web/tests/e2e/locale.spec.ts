import { expect, test } from "@playwright/test";

/**
 * i18n e2e: verifies locale routing and Chinese rendering.
 * design/frontend.md §12.
 */
const EMAIL = process.env.VOXELTOAD_ADMIN_EMAIL ?? "root@adminstack";
const PASSWORD = process.env.VOXELTOAD_ADMIN_PASSWORD ?? "adminstack-pass-123";

test("default locale renders English login", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Control Panel" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
});

test("/zh/login renders Chinese login", async ({ page }) => {
  await page.goto("/zh/login");
  await expect(page.getByRole("heading", { name: "控制面板" })).toBeVisible();
  await expect(page.getByRole("button", { name: "登录" })).toBeVisible();
  await expect(page.getByText("邮箱")).toBeVisible();
  await expect(page.getByText("密码")).toBeVisible();
});

test("Chinese login error shows translated message", async ({ page }) => {
  await page.goto("/zh/login");
  // Email/password inputs carry the `required` attribute (login/page.tsx),
  // so an empty submit is intercepted by native HTML5 validation and never
  // reaches loginAction — fill a non-empty, invalid email so the browser
  // lets the submit through and the server action's error path actually runs.
  await page.fill('input[name="email"]', `e2e-locale-${Date.now()}@test`);
  await page.fill('input[name="password"]', "wrong-password");
  await page.click('button[type="submit"]');
  // loginAction wraps the admin API 401 into a normal form error (invalid
  // credentials). The error alert is rendered inside the form, NOT the Next.js
  // built-in route announcer (#__next-route-announcer__) which also has
  // role="alert" but stays empty.
  const errorEl = page.locator('[role="alert"]:not(#__next-route-announcer__)');
  await expect(errorEl).toBeVisible({ timeout: 10000 });
  const text = await errorEl.textContent();
  console.log("[diag] zh login error text:", JSON.stringify(text));
  expect(text).toBeTruthy();
});

test("default English login → providers renders English UI", async ({
  page,
}) => {
  await page.goto("/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/providers$/);
  await expect(page.getByRole("heading", { name: "Providers" })).toBeVisible();
  await expect(
    page.getByText("Upstream LLM suppliers proxied by the gateway."),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create provider" }),
  ).toBeVisible();
});

test("Chinese login → /zh/providers renders Chinese UI", async ({ page }) => {
  await page.goto("/zh/login");
  await page.fill('input[name="email"]', EMAIL);
  await page.fill('input[name="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL(/\/zh\/providers$/);
  await expect(page.getByRole("heading", { name: "供应商" })).toBeVisible();
  await expect(
    page.getByText("网关代理的上游 LLM 服务。"),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "创建供应商" }),
  ).toBeVisible();
});
