/**
 * Unit tests for the `dada_vid` visitor cookie (lib/box-vid.ts).
 *
 * Run with Node's built-in test runner and type stripping — no dependency, and no
 * `npm ci` needed, which matters because the private @dada/* packages make an
 * install impossible outside the network that hosts them:
 *
 *   cd frontend && npm run test:unit
 *
 * These are the two properties worth pinning: an id is issued in the exact shape
 * the funnel expects, and a cookie value that is NOT an opaque id is refused
 * rather than stored. The second is the 152-ФЗ guard on the client side — the
 * backend enforces the same rule again on the way in.
 */

import test from "node:test";
import assert from "node:assert/strict";

import {
  BOX_VID_COOKIE,
  BOX_VID_MAX_AGE,
  boxVidSetCookie,
  isBoxLandingPath,
  isBoxVid,
  newBoxVid,
  readBoxVid,
// The explicit .ts extension is what Node's type-stripping loader needs. It is
// also why tsconfig.json excludes **/*.test.ts: without allowImportingTsExtensions
// the project type-check would reject the extension, and turning that flag on for
// the whole app to accommodate one test file is the wrong trade.
} from "./box-vid.ts";

test("newBoxVid issues an opaque UUID, different every time", () => {
  const a = newBoxVid();
  const b = newBoxVid();
  assert.ok(isBoxVid(a), `${a} should be a valid vid`);
  assert.ok(isBoxVid(b));
  assert.notEqual(a, b, "two visitors must not share an id");
});

test("isBoxVid accepts only opaque UUIDs", () => {
  assert.ok(isBoxVid("6f4a9a1e-6b1e-4f0a-9b9d-2c3b4a5d6e7f"));
  for (const bad of [
    "",
    undefined,
    null,
    "not-a-uuid",
    "6f4a9a1e6b1e4f0a9b9d2c3b4a5d6e7f",
    "6f4a9a1e-6b1e-4f0a-9b9d-2c3b4a5d6e7",
    // The cases that matter: anything personal must never pass as a visitor id.
    "someone@example.com",
    "alex",
    "+79991234567",
  ]) {
    assert.equal(isBoxVid(bad as string | undefined | null), false, `${String(bad)} must be rejected`);
  }
});

test("boxVidSetCookie carries the documented attributes", () => {
  const vid = newBoxVid();
  const cookie = boxVidSetCookie(vid, true);
  assert.ok(cookie.startsWith(`${BOX_VID_COOKIE}=${vid}`));
  assert.match(cookie, /Path=\//);
  assert.match(cookie, new RegExp(`Max-Age=${BOX_VID_MAX_AGE}`));
  assert.match(cookie, /SameSite=Lax/);
  assert.match(cookie, /HttpOnly/);
  assert.match(cookie, /Secure/);
  // 400 days is the ceiling browsers honour; a longer Max-Age would be silently
  // clamped and the constant would then describe something untrue.
  assert.equal(BOX_VID_MAX_AGE, 400 * 24 * 60 * 60);
  // Host-only: an anonymous marketing id is not broadcast across the fleet the
  // way the authenticated dada_uid deliberately is.
  assert.doesNotMatch(cookie, /Domain=/);
});

test("boxVidSetCookie omits Secure only for plain-http dev", () => {
  const insecure = boxVidSetCookie(newBoxVid(), false);
  assert.doesNotMatch(insecure, /Secure/);
});

test("readBoxVid finds the id among other cookies", () => {
  const vid = "6f4a9a1e-6b1e-4f0a-9b9d-2c3b4a5d6e7f";
  assert.equal(readBoxVid(`dada_uid=abc; ${BOX_VID_COOKIE}=${vid}; dada_src=door_box`), vid);
  assert.equal(readBoxVid(`${BOX_VID_COOKIE}=${vid}`), vid);
  assert.equal(readBoxVid(` ${BOX_VID_COOKIE} = ${vid} `), vid);
});

test("readBoxVid refuses a value that is not an opaque id", () => {
  // A tampered cookie must be replaced with a fresh id, not echoed into a funnel
  // record. Otherwise the vid column becomes whatever a caller decides to put in it.
  assert.equal(readBoxVid(`${BOX_VID_COOKIE}=someone@example.com`), undefined);
  assert.equal(readBoxVid(`${BOX_VID_COOKIE}=`), undefined);
  assert.equal(readBoxVid("dada_uid=abc"), undefined);
  assert.equal(readBoxVid(""), undefined);
  assert.equal(readBoxVid(null), undefined);
  // A cookie whose NAME merely ends in dada_vid is a different cookie.
  assert.equal(readBoxVid("not_dada_vid=6f4a9a1e-6b1e-4f0a-9b9d-2c3b4a5d6e7f"), undefined);
});

test("readBoxVid round-trips what boxVidSetCookie writes", () => {
  const vid = newBoxVid();
  const setCookie = boxVidSetCookie(vid, true);
  // A browser sends back only "name=value"; take the first attribute pair.
  assert.equal(readBoxVid(setCookie.split(";")[0]), vid);
});

test("the id is issued on the Box landing only", () => {
  assert.ok(isBoxLandingPath("/box"));
  assert.ok(isBoxLandingPath("/en/box"));
  for (const other of ["/", "/en", "/pricing", "/box/extra", "/boxes", "/en/boxes"]) {
    assert.equal(isBoxLandingPath(other), false, `${other} must not issue a visitor id`);
  }
});
