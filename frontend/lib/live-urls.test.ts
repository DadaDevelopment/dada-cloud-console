import assert from "node:assert/strict";
import test from "node:test";
import { liveUrlHeadline, liveUrlOkShare, liveUrlStaleDominates } from "./live-urls.ts";

test("computes the ok share as a rounded percentage", () => {
  assert.equal(liveUrlOkShare({ checked: 42, ok: 30 }), 71);
  assert.equal(liveUrlOkShare({ checked: 0, ok: 0 }), null);
});

test("builds a headline with the ratio and percentage", () => {
  assert.equal(liveUrlHeadline({ checked: 42, ok: 30 }), "30 из 42 (71%)");
  assert.equal(liveUrlHeadline({ checked: 0, ok: 0 }), "нечего проверять");
});

test("flags when stale data dwarfs what was actually checked", () => {
  assert.equal(liveUrlStaleDominates({ checked: 42, stale: 3 }), false);
  assert.equal(liveUrlStaleDominates({ checked: 42, stale: 12 }), true);
  assert.equal(liveUrlStaleDominates({ checked: 0, stale: 5 }), true);
  assert.equal(liveUrlStaleDominates({ checked: 42, stale: 0 }), false);
});
