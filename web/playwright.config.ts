import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright config for the Control Panel e2e (design/frontend.md §9, slice 0).
 *
 * The suite drives a REAL browser through the whole stack: browser → Next
 * (RSC/Server Actions, encrypted-cookie session) → admin API. The admin plane
 * and Next server are started by scripts/web-e2e.sh (which also sets ADMIN_URL
 * + SESSION_SECRET); this config assumes both are already running and only
 * points the browser at the Next server.
 *
 * BASE_URL defaults to the Next dev/start port. web-e2e.sh may override it.
 */
const baseURL = process.env.WEB_BASE_URL ?? "http://127.0.0.1:3000";

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
});
