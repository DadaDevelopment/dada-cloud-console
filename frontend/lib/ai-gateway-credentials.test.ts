import assert from "node:assert/strict";
import test from "node:test";

import { buildCredentialUpdate } from "./ai-gateway-credentials.ts";

test("credential edit omits a blank replacement secret", () => {
  assert.deepEqual(buildCredentialUpdate({
    label: "  Sota reserve  ", apiBase: " https://sota.example/v1 ",
    apiKey: "   ", priority: "20",
  }), {
    label: "Sota reserve",
    api_base: "https://sota.example/v1",
    priority: 20,
  });
});

test("credential edit includes an explicitly entered replacement secret", () => {
  assert.deepEqual(buildCredentialUpdate({
    label: "", apiBase: "", apiKey: " new-secret ", priority: "0",
  }), {
    label: "",
    api_base: "",
    api_key: "new-secret",
    priority: 0,
  });
});

test("credential edit rejects an invalid priority", () => {
  assert.throws(() => buildCredentialUpdate({
    label: "", apiBase: "", apiKey: "", priority: "-1",
  }), /priority/i);
});
