import assert from "node:assert/strict";
import test from "node:test";

import { isStarterRepo, STARTER_TEMPLATES } from "./starter-templates.ts";

test("isStarterRepo matches a starter repo exactly", () => {
  assert.equal(isStarterRepo("DadaDevelopment/dada-nextjs-starter"), true);
});

test("isStarterRepo matches regardless of case", () => {
  assert.equal(isStarterRepo("dadadevelopment/DADA-NEXTJS-STARTER"), true);
});

test("isStarterRepo rejects a user's own repo", () => {
  assert.equal(isStarterRepo("someuser/my-own-app"), false);
});

test("isStarterRepo rejects an empty or missing repo name", () => {
  assert.equal(isStarterRepo(""), false);
  assert.equal(isStarterRepo("   "), false);
  assert.equal(isStarterRepo(undefined), false);
  assert.equal(isStarterRepo(null), false);
});

test("STARTER_TEMPLATES has no duplicate keys", () => {
  const keys = STARTER_TEMPLATES.map((tpl) => tpl.key);
  assert.equal(keys.length, new Set(keys).size);
});
