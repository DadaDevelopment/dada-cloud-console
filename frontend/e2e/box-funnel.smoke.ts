import { test, expect } from "@playwright/test";

/**
 * Unauthenticated smoke for the Dada Box funnel instrumentation.
 *
 * It covers the one link in the chain that unit tests cannot reach: the `dada_vid`
 * cookie is issued by `proxy.ts` on the real first hit of /box, and it is the SAME
 * id on the second hit. If it were reissued per request, every funnel counter would
 * count page loads instead of people and the conversion denominator would be
 * meaningless — the exact failure this whole slice exists to fix.
 *
 * NOTE: only the marketing host issues the cookie. The console host serves /box
 * with a 200 and NO Set-Cookie (verified live 2026-07-30), so a 404 guard is not
 * enough: point `E2E_MARKETING_BASE_URL` at the marketing origin and the cookie
 * assertions run against an absolute URL there.
 */
const VID_COOKIE = "dada_vid";
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MARKETING_BASE_URL = process.env.E2E_MARKETING_BASE_URL?.replace(/\/$/, "");
const BOX_URL = MARKETING_BASE_URL ? `${MARKETING_BASE_URL}/box` : "/box";

test("the box landing issues a stable, opaque visitor id", async ({ page, context }) => {
  const response = await page.goto(BOX_URL, { waitUntil: "domcontentloaded" });
  expect(response, "navigation returned a response").not.toBeNull();
  expect(response!.status(), "landing is not a 5xx").toBeLessThan(500);
  test.skip(
    !MARKETING_BASE_URL && response!.status() === 404,
    "/box not served on this target",
  );

  const first = (await context.cookies()).find((c) => c.name === VID_COOKIE);
  expect(first, `${VID_COOKIE} is set on the first hit`).toBeDefined();
  expect(first!.value, "the id is an opaque UUID and nothing else").toMatch(UUID_RE);
  expect(first!.httpOnly, "nothing in the browser reads it, so it is HttpOnly").toBe(true);
  expect(first!.sameSite).toBe("Lax");
  // ~400 days, allowing for the seconds spent in this test.
  expect(first!.expires * 1000 - Date.now()).toBeGreaterThan(399 * 24 * 60 * 60 * 1000);

  await page.goto(BOX_URL, { waitUntil: "domcontentloaded" });
  const second = (await context.cookies()).find((c) => c.name === VID_COOKIE);
  expect(second!.value, "a returning visitor keeps the same id").toBe(first!.value);
});

test("the visitor id is not issued outside the box landing", async ({ page, context }) => {
  await context.clearCookies();
  const response = await page.goto("/", { waitUntil: "domcontentloaded" });
  expect(response!.status()).toBeLessThan(500);
  const vid = (await context.cookies()).find((c) => c.name === VID_COOKIE);
  expect(vid, "the home page has no funnel to attribute, so it sets no id").toBeUndefined();
});
