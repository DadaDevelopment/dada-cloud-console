import assert from "node:assert/strict";
import test from "node:test";
import { isClassFixBuild } from "./build-classfix.ts";

test("a class_fix build is recognized", () => {
  assert.equal(isClassFixBuild("class_fix"), true);
});

test("a manual build is not a class_fix build", () => {
  assert.equal(isClassFixBuild("manual"), false);
});

test("a push build is not a class_fix build", () => {
  assert.equal(isClassFixBuild("push"), false);
});

test("a missing trigger is not a class_fix build", () => {
  assert.equal(isClassFixBuild(undefined), false);
  assert.equal(isClassFixBuild(null), false);
});
