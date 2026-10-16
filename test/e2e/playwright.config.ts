import { defineConfig, devices } from "@playwright/test";

const isCI = !!process.env.CI;

/**
 * E2E tests exercise provider-cloudnative-pg through the OpenEverest UI. The UI
 * is expected at EVEREST_URL (default http://localhost:8080), for example via
 * `kubectl -n everest-system port-forward svc/everest 8080:8080`.
 */
export default defineConfig({
  testDir: "./tests",
  testMatch: "**/*.e2e.ts",
  outputDir: "./test-results",

  // Database provisioning is slow and stateful: run tests one at a time.
  fullyParallel: false,
  workers: 1,

  forbidOnly: isCI,
  retries: isCI ? 1 : 0,

  // Creating and deleting a PostgreSQL cluster through the UI.
  timeout: 15 * 60 * 1_000,
  globalTimeout: 45 * 60 * 1_000,
  expect: { timeout: 15_000 },

  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "./playwright-report" }],
    ...(isCI ? [["github"] as const] : []),
  ],

  globalSetup: "./global-setup.ts",
  globalTeardown: "./global-teardown.ts",

  use: {
    baseURL: process.env.EVEREST_URL || "http://localhost:8080",
    storageState: ".auth/user.json",
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },

  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
