import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    globals: true,
    environment: "node",
    include: ["test/**/*.{test,spec}.ts"],
    // The live e2e suites share one admin plane and one PostgreSQL. Running
    // test files in parallel lets destructive operations in one file (e.g.
    // config rollback, which replaces the whole config) delete resources that
    // another file just created. Serialize files so results are deterministic.
    fileParallelism: false,
    coverage: {
      provider: "v8",
      include: ["src/**"],
    },
  },
});
