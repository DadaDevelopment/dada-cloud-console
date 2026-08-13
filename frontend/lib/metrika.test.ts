/**
 * Unit tests for first-touch attribution (lib/metrika.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * `computeAttribution` is pure (no DOM) and is pinned directly. `rememberAttribution`
 * needs `document`, which does not exist under plain Node, so these tests install a
 * minimal fake `document` with a real cookie-jar semantics (accumulate on write,
 * "name=value; name2=value2" on read) before each case.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { computeAttribution, rememberAttribution, SOURCE_COOKIE, MEDIUM_COOKIE, CAMPAIGN_COOKIE, REFERRER_COOKIE } from "./metrika.ts";

function installFakeDocument(opts: { hostname: string; search?: string; referrer?: string }): void {
  const jar = new Map<string, string>();
  const fakeDocument = {
    get cookie(): string {
      return Array.from(jar.entries())
        .map(([k, v]) => `${k}=${v}`)
        .join("; ");
    },
    set cookie(raw: string) {
      const [pair] = raw.split(";");
      const eq = pair.indexOf("=");
      const name = pair.slice(0, eq);
      const value = pair.slice(eq + 1);
      jar.set(name, value);
    },
    referrer: opts.referrer ?? "",
    location: {
      hostname: opts.hostname,
      search: opts.search ?? "",
    },
  };
  (globalThis as unknown as { document: typeof fakeDocument }).document = fakeDocument;
}

function readCookie(name: string): string | undefined {
  const raw = (globalThis as unknown as { document: Document }).document.cookie;
  const match = raw.split("; ").find((c) => c.startsWith(`${name}=`));
  return match ? decodeURIComponent(match.slice(name.length + 1)) : undefined;
}

test.afterEach(() => {
  delete (globalThis as { document?: unknown }).document;
});

test("computeAttribution: utm wins over referrer", () => {
  const result = computeAttribution({
    search: "?utm_source=newsletter&utm_medium=email&utm_campaign=aug",
    referrer: "https://google.com/search",
    hostname: "dada-tuda.ru",
  });
  assert.equal(result.source, "newsletter");
  assert.equal(result.medium, "email");
  assert.equal(result.campaign, "aug");
  assert.equal(result.ref, "https://google.com/search");
});

test("computeAttribution: referrer host used when no utm", () => {
  const result = computeAttribution({
    search: "",
    referrer: "https://ya.ru/search?text=dada",
    hostname: "dada-tuda.ru",
  });
  assert.equal(result.source, "ya.ru");
  assert.equal(result.medium, "");
  assert.equal(result.campaign, "");
  assert.equal(result.ref, "https://ya.ru/search?text=dada");
});

test("computeAttribution: direct when neither utm nor referrer", () => {
  const result = computeAttribution({ search: "", referrer: "", hostname: "dada-tuda.ru" });
  assert.equal(result.source, "direct");
  assert.equal(result.ref, "");
});

test("computeAttribution: same-origin referrer does not become the source", () => {
  const result = computeAttribution({
    search: "",
    referrer: "https://console.dada-tuda.ru/apps",
    hostname: "console.dada-tuda.ru",
  });
  assert.equal(result.source, "direct");
  assert.equal(result.ref, "");
});

test("computeAttribution: long values are truncated", () => {
  const longSource = "s".repeat(200);
  const longReferrer = "https://example.com/" + "r".repeat(400);
  const result = computeAttribution({
    search: `?utm_source=${longSource}&utm_medium=${"m".repeat(200)}&utm_campaign=${"c".repeat(200)}`,
    referrer: longReferrer,
    hostname: "dada-tuda.ru",
  });
  assert.equal(result.source.length, 64);
  assert.equal(result.medium.length, 64);
  assert.equal(result.campaign.length, 64);
  assert.equal(result.ref.length, 255);
});

test("computeAttribution: non-printable characters are stripped", () => {
  const result = computeAttribution({
    search: "?utm_source=ne\x00ws\x7Fletter",
    referrer: "",
    hostname: "dada-tuda.ru",
  });
  assert.equal(result.source, "newsletter");
});

test("rememberAttribution: writes all four cookies on first touch, direct included", () => {
  installFakeDocument({ hostname: "dada-tuda.ru" });
  rememberAttribution();
  assert.equal(readCookie(SOURCE_COOKIE), "direct");
  assert.equal(readCookie(MEDIUM_COOKIE), "");
  assert.equal(readCookie(CAMPAIGN_COOKIE), "");
  assert.equal(readCookie(REFERRER_COOKIE), "");
});

test("rememberAttribution: a second call does not overwrite the first", () => {
  installFakeDocument({ hostname: "dada-tuda.ru", search: "?utm_source=landing_a" });
  rememberAttribution();
  assert.equal(readCookie(SOURCE_COOKIE), "landing_a");

  installFakeDocument({ hostname: "dada-tuda.ru", search: "?utm_source=landing_b" });
  (globalThis as unknown as { document: { cookie: string } }).document.cookie = `${SOURCE_COOKIE}=landing_a`;
  rememberAttribution();
  assert.equal(readCookie(SOURCE_COOKIE), "landing_a");
  assert.equal(readCookie(MEDIUM_COOKIE), undefined);
});

test("rememberAttribution: captures a real landing utm on first touch", () => {
  installFakeDocument({ hostname: "dada-tuda.ru", search: "?utm_source=upload_landing&utm_medium=cpc&utm_campaign=aug26" });
  rememberAttribution();
  assert.equal(readCookie(SOURCE_COOKIE), "upload_landing");
  assert.equal(readCookie(MEDIUM_COOKIE), "cpc");
  assert.equal(readCookie(CAMPAIGN_COOKIE), "aug26");
});
