import { test as setup, expect } from "@playwright/test";

/**
 * Auth setup: logs into the console through Keycloak once and saves the session
 * to e2e/.auth/state.json, which the `authed` project reuses. This is Playwright's
 * storageState pattern -- a setup project, not a per-test login helper.
 *
 * Requires E2E_USER / E2E_PASS (a dedicated e2e Keycloak user) and E2E_BASE_URL
 * pointing at a console instance. Skipped when those are absent, so the smoke
 * project still runs on its own.
 */
const storageState = "e2e/.auth/state.json";

setup("authenticate", async ({ page }) => {
  const user = process.env.E2E_USER;
  const pass = process.env.E2E_PASS;
  setup.skip(!user || !pass || !process.env.E2E_BASE_URL, "E2E_USER/E2E_PASS/E2E_BASE_URL not set");

  await page.goto("/");

  await page.locator("#username").waitFor({ state: "visible" });
  await page.locator("#username").fill(user!);
  await page.locator("#password").fill(pass!);
  await page.locator("#kc-login, button[type=submit]").first().click();

  await expect(page.locator("#username")).toHaveCount(0);
  await page.context().storageState({ path: storageState });
});
