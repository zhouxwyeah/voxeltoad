import { defineConfig } from "vitest/config";

// Unit tests for desktop-ui. Scope is intentionally minimal for now (pure
// helpers in src/lib); component rendering + browser e2e are deferred per
// design/desktop.md §11. Runs under `npm test` (vitest run, node env).
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
