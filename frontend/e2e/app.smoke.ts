import { test, expect } from "@playwright/test";

/**
 * Unauthenticated smoke: proves the harness can reach the target and the app
 * shell renders without a client crash. Runs against E2E_BASE_URL (a public
 * landing, or the console which redirects to Keycloak) or the local dev server.
 */
test("app shell loads without crashing", async ({ page }) => {
  const response = await page.goto("/", { waitUntil: "domcontentloaded" });
  expect(response, "navigation returned a response").not.toBeNull();
  expect(response!.status(), "top-level document is not a 5xx").toBeLessThan(500);

  await expect(page).toHaveTitle(/.+/);
  await expect(page.locator("body")).not.toBeEmpty();
});
