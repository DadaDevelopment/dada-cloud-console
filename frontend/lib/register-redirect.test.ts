import { strict as assert } from "node:assert";
import { test } from "node:test";
import { registerQueryParams } from "./register-redirect.ts";

test("registerQueryParams yandex hints the broker and skips prompt=create", () => {
  assert.deepEqual(registerQueryParams("yandex"), { kc_idp_hint: "yandex" });
});

test("registerQueryParams email asks for the registration form", () => {
  assert.deepEqual(registerQueryParams("email"), { prompt: "create" });
});
