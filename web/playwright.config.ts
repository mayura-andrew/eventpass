// SPDX-License-Identifier: Apache-2.0
import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env["E2E_PORT"] ?? 8092);

// Runs against the demo (memory storage, mail catcher). Build the web app
// first (`npm run build`) so the Go binary embeds the current UI.
export default defineConfig({
  testDir: "e2e",
  fullyParallel: false,
  forbidOnly: !!process.env["CI"],
  retries: process.env["CI"] ? 1 : 0,
  reporter: process.env["CI"] ? [["github"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop", use: { ...devices["Desktop Chrome"] } },
    { name: "phone", use: { ...devices["Pixel 7"] } },
  ],
  webServer: {
    command: "go run ../cmd/eventpass serve --env-file /dev/null",
    url: `http://localhost:${PORT}/healthz`,
    reuseExistingServer: !process.env["CI"],
    timeout: 120_000,
    env: { EVENTPASS_DEMO: "1", PORT: String(PORT), PUBLIC_URL: `http://localhost:${PORT}`, LOG_LEVEL: "warn" },
  },
});
