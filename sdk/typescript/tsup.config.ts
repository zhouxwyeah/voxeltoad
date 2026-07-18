import { defineConfig } from "tsup";

export default defineConfig({
  // Two entry points, one per plane: the data-plane client (index) and the
  // management-plane admin client (admin). Kept separate so a consumer that
  // needs only one does not bundle the other's dependencies.
  entry: ["src/index.ts", "src/admin.ts"],
  format: ["esm", "cjs"],
  dts: true,
  clean: true,
  sourcemap: true,
  target: "es2022",
});
