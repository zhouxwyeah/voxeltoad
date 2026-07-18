import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// The desktop UI is served by the Go gateway from the built `dist/` directory
// (single origin with the /api/v1 read API). The relative base ("") keeps
// asset URLs root-relative so they resolve under the gateway port.
export default defineConfig({
  base: "",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // In `vite dev` (hot-reload) mode the SPA runs on :5173 while the Go
    // gateway (read API + data plane) runs on :8787. The SPA fetches /api/v1/*
    // and /v1/* via relative URLs (src/lib/api.ts) — without this proxy they
    // would hit :5173 and 404. Production sidesteps this by serving the built
    // SPA from the gateway itself (single origin). scripts/desktop-web-dev.sh
    // orchestrates both processes.
    proxy: {
      "/api/v1": "http://127.0.0.1:8787",
      "/v1": "http://127.0.0.1:8787",
    },
  },
});
