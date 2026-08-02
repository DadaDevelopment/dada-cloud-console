import assert from "node:assert/strict";
import test from "node:test";

import {
  REDACTED,
  confirmArgEntries,
  isSecretArgKey,
  redactArgValues,
} from "./agent-chat-redact.ts";

const LIVE_TOKEN = "7712345:AAF-nBqQx9Zk3tR1sample";

function rendered(entries: Array<[string, string]>): string {
  return entries.map(([k, v]) => `${k}: ${v}`).join("\n");
}

test("bulkSetEnvVars never prints a token or a password", () => {
  const args = {
    projectId: "p-1",
    appName: "tg-bot",
    vars: [
      { key: "TELEGRAM_BOT_TOKEN", value: LIVE_TOKEN },
      { key: "DB_PASSWORD", value: "hunter2" },
    ],
  };
  const summary =
    "Set 2 environment variable(s) on app tg-bot: TELEGRAM_BOT_TOKEN, DB_PASSWORD. Values are not shown here.";

  const out = rendered(confirmArgEntries(args, summary));

  assert.equal(out.includes(LIVE_TOKEN), false);
  assert.equal(out.includes("hunter2"), false);
  assert.match(out, /projectId: p-1/);
  assert.match(out, /appName: tg-bot/);
  assert.match(out, /TELEGRAM_BOT_TOKEN/);
  assert.match(out, new RegExp(REDACTED.replace(/[[\]]/g, "\\$&")));
});

test("a flat setEnvVar value is redacted too", () => {
  const out = rendered(
    confirmArgEntries({ appName: "api", key: "STRIPE_SECRET", value: "sk_live_9x8" }),
  );
  assert.equal(out.includes("sk_live_9x8"), false);
  assert.match(out, /key: STRIPE_SECRET/);
  assert.match(out, /appName: api/);
});

test("secrets nested arbitrarily deep are still caught", () => {
  const args = {
    spec: { auth: { basic: { password: "p@ss" } }, list: [{ apiKey: "ak-1" }] },
  };
  const out = rendered(confirmArgEntries(args));
  assert.equal(out.includes("p@ss"), false);
  assert.equal(out.includes("ak-1"), false);
});

test("a secret-named map key hides its value", () => {
  const out = rendered(confirmArgEntries({ vars: { TELEGRAM_BOT_TOKEN: LIVE_TOKEN } }));
  assert.equal(out.includes(LIVE_TOKEN), false);
  assert.match(out, /TELEGRAM_BOT_TOKEN/);
});

test("isSecretArgKey leaves the env-var name field visible", () => {
  assert.equal(isSecretArgKey("key"), false);
  assert.equal(isSecretArgKey("appName"), false);
  assert.equal(isSecretArgKey("projectId"), false);
  assert.equal(isSecretArgKey("keys"), false);
  assert.equal(isSecretArgKey("value"), true);
  assert.equal(isSecretArgKey("Value"), true);
  assert.equal(isSecretArgKey("api_key"), true);
  assert.equal(isSecretArgKey("apiKey"), true);
  assert.equal(isSecretArgKey("BOT_TOKEN"), true);
  assert.equal(isSecretArgKey("dbPassword"), true);
  assert.equal(isSecretArgKey("credentials"), true);
  assert.equal(isSecretArgKey("sshPrivateKey"), true);
});

test("redactArgValues keeps shape and non-secret scalars", () => {
  assert.deepEqual(redactArgValues({ n: 3, ok: true, nil: null, s: "plain" }), {
    n: 3,
    ok: true,
    nil: null,
    s: "plain",
  });
  assert.deepEqual(redactArgValues([{ value: "x" }, { name: "y" }]), [
    { value: REDACTED },
    { name: "y" },
  ]);
});

test("a long value the summary already spells out is not repeated", () => {
  const entries = confirmArgEntries(
    { appName: "web", image: "registry.dada/web@sha256:deadbeef" },
    "Update app web to registry.dada/web@sha256:deadbeef",
  );
  assert.deepEqual(entries, [["appName", "web"]]);
});

test("short identifiers survive summary dedupe", () => {
  const entries = confirmArgEntries({ appName: "web", projectId: "p-1" }, "Restart app web in p-1");
  assert.deepEqual(entries, [
    ["appName", "web"],
    ["projectId", "p-1"],
  ]);
});

test("empty and missing args produce nothing", () => {
  assert.deepEqual(confirmArgEntries(undefined), []);
  assert.deepEqual(confirmArgEntries({}), []);
  assert.deepEqual(confirmArgEntries({ a: null, b: undefined, c: "" }), []);
});
