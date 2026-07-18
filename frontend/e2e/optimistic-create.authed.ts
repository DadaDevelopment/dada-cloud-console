import { test, expect } from "@playwright/test";

/**
 * The guarantee this whole change exists for, exercised through the real browser:
 * ordering a database makes it appear immediately as a Pending card (the optimistic
 * seed), and it then advances to Ready on its own via the list poll -- no manual
 * refresh.
 *
 * This mutates real infrastructure (it provisions a managed Postgres), so it only
 * runs when explicitly opted in with E2E_MUTATE=1 against a disposable project
 * (E2E_PROJECT_ID). Never point it at a customer project.
 */
const projectId = process.env.E2E_PROJECT_ID;

test.describe("optimistic database create", () => {
  test.skip(!process.env.E2E_MUTATE || !projectId, "set E2E_MUTATE=1 and E2E_PROJECT_ID (disposable project)");

  test("new database shows instantly as Pending, then reaches Ready", async ({ page }) => {
    await page.goto(`/projects/${projectId}/databases`);

    await page.getByRole("button", { name: /Создать базу|Create database/i }).first().click();

    const nameInput = page.locator('input[pattern="[a-z0-9-]+"]').first();
    await expect(nameInput).toBeVisible();
    const dbName = await nameInput.inputValue();
    expect(dbName, "form pre-fills a generated name").toMatch(/^db-/);

    await page.getByRole("button", { name: /Создать|Create/i }).last().click();

    const card = page.getByText(dbName, { exact: false });
    await expect(card, "database appears optimistically within seconds").toBeVisible({ timeout: 5_000 });

    const pending = page.getByText(/Pending|Provisioning/i).first();
    await expect(pending, "starts in a non-terminal phase").toBeVisible({ timeout: 5_000 });

    await expect(
      page.getByText(/Ready/i).first(),
      "poll advances it to Ready without a manual reload",
    ).toBeVisible({ timeout: 180_000 });
  });
});
