import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright Test config for the console e2e suite.
 *
 * Target selection:
 *   - E2E_BASE_URL set  -> run against that URL (staging/prod), no local server.
 *   - E2E_BASE_URL unset -> Playwright boots `next dev` on :3000 and tests that.
 *
 * Projects:
 *   - smoke  : unauthenticated, no dependencies. Runnable anywhere.
 *   - setup  : logs into Keycloak once and writes the storage state.
 *   - authed : reuses that storage state; carries the flows that need a session.
 *
 * Auth follows Playwright's own storageState pattern (a setup project + a saved
 * session file), not a hand-rolled login helper.
 */

const baseURL = process.env.E2E_BASE_URL || "http://localhost:3000";
const storageState = "e2e/.auth/state.json";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "smoke",
      testMatch: /.*\.smoke\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "setup",
      testMatch: /auth\.setup\.ts/,
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "authed",
      testMatch: /.*\.authed\.ts/,
      dependencies: ["setup"],
      use: { ...devices["Desktop Chrome"], storageState },
    },
  ],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: "npm run dev",
        url: "http://localhost:3000",
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      },
});
