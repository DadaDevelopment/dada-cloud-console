import assert from "node:assert/strict";
import test from "node:test";
import { paymentsEnvKeys, paymentsWebhooks } from "./payments-connection.ts";

test("paymentsWebhooks returns an empty list instead of throwing when webhooks is null (backlog 0402 crash)", () => {
  const connection = { webhooks: null };
  assert.doesNotThrow(() => paymentsWebhooks(connection));
  assert.deepEqual(paymentsWebhooks(connection), []);
});

test("paymentsWebhooks returns an empty list when the connection itself is null", () => {
  assert.deepEqual(paymentsWebhooks(null), []);
});

test("paymentsWebhooks passes through a populated list unchanged", () => {
  const connection = { webhooks: [{ id: "wh-1", event: "payment.succeeded" }] };
  assert.deepEqual(paymentsWebhooks(connection), [{ id: "wh-1", event: "payment.succeeded" }]);
});

test("paymentsEnvKeys returns an empty list instead of throwing when env_keys is null (backlog 0402 crash)", () => {
  const connection = { env_keys: null };
  assert.doesNotThrow(() => paymentsEnvKeys(connection));
  assert.deepEqual(paymentsEnvKeys(connection), []);
});

test("paymentsEnvKeys passes through a populated list unchanged", () => {
  const connection = { env_keys: ["YOOKASSA_OAUTH_TOKEN", "YOOKASSA_ACCOUNT_ID"] };
  assert.deepEqual(paymentsEnvKeys(connection), ["YOOKASSA_OAUTH_TOKEN", "YOOKASSA_ACCOUNT_ID"]);
});
