// SPDX-License-Identifier: Apache-2.0
/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The Go server embeds dist/ and serves it. In development, run the Go
// server on :8080 and `npm run dev`; API and dev pages are proxied.
export default defineConfig({
  plugins: [react()],
  css: { modules: { localsConvention: "camelCaseOnly" } },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: false,
    // No inline scripts or data-URI modules: the CSP forbids them.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/dev": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    css: { modules: { classNameStrategy: "non-scoped" } },
  },
});
