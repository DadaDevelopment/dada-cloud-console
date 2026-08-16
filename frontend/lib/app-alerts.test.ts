import assert from "node:assert/strict";
import test from "node:test";

import {
  getOperationalAppAlerts,
  missingEnvVarKey,
  parseBadConnCauseLine,
  suggestSSLModeDisable,
  type AppAlert,
} from "./app-alerts.ts";

const alerts: AppAlert[] = [
  { type: "crash", detected_at: "2026-08-04T00:00:00Z" },
  { type: "volume", detected_at: "2026-08-04T00:00:00Z" },
  { type: "url", detected_at: "2026-08-04T00:00:00Z" },
];

test("getOperationalAppAlerts keeps a public-route failure actionable", () => {
  assert.deepEqual(getOperationalAppAlerts(alerts).map((alert) => alert.type), ["crash", "volume", "url"]);
});

test("parseBadConnCauseLine splits the live megafactory shape", () => {
  assert.deepEqual(parseBadConnCauseLine("DATABASE_URL=pg-router.databases.svc.cluster.local"), {
    key: "DATABASE_URL",
    value: "pg-router.databases.svc.cluster.local",
  });
});

test("parseBadConnCauseLine returns null when there is no cause_line", () => {
  assert.equal(parseBadConnCauseLine(undefined), null);
  assert.equal(parseBadConnCauseLine(""), null);
});

test("parseBadConnCauseLine returns null when there is no separator", () => {
  assert.equal(parseBadConnCauseLine("not-a-key-value-pair"), null);
});

test("parseBadConnCauseLine splits on the first = only", () => {
  assert.deepEqual(parseBadConnCauseLine("CONNECTION_STRING=host=weird"), {
    key: "CONNECTION_STRING",
    value: "host=weird",
  });
});

test("suggestSSLModeDisable appends sslmode=disable to the live megafactory DSN", () => {
  assert.equal(
    suggestSSLModeDisable("postgresql://svc-megafactory:secret@pg-router.databases.svc.cluster.local:5432/megafactory"),
    "postgresql://svc-megafactory:secret@pg-router.databases.svc.cluster.local:5432/megafactory?sslmode=disable",
  );
});

test("suggestSSLModeDisable preserves existing query params", () => {
  assert.equal(
    suggestSSLModeDisable("postgresql://svc:pw@host:5432/db?application_name=megafactory"),
    "postgresql://svc:pw@host:5432/db?application_name=megafactory&sslmode=disable",
  );
});

test("suggestSSLModeDisable is idempotent when sslmode=disable is already set", () => {
  const dsn = "postgresql://svc:pw@host:5432/db?sslmode=disable";
  assert.equal(suggestSSLModeDisable(dsn), dsn);
  assert.equal(suggestSSLModeDisable(suggestSSLModeDisable(dsn)), dsn);
});

test("suggestSSLModeDisable detects an existing sslmode case-insensitively and leaves it alone", () => {
  const dsn = "postgresql://svc:pw@host:5432/db?SSLMode=require";
  assert.equal(suggestSSLModeDisable(dsn), dsn);
});

test("missingEnvVarKey recovers the key from the live sevarateambot crash", () => {
  assert.equal(missingEnvVarKey("TELEGRAM_API_TOKEN"), "TELEGRAM_API_TOKEN");
});

test("missingEnvVarKey tolerates surrounding whitespace", () => {
  assert.equal(missingEnvVarKey("  STRIPE_SECRET_KEY\n"), "STRIPE_SECRET_KEY");
});

test("missingEnvVarKey refuses anything that is not a bare env var name", () => {
  assert.equal(missingEnvVarKey(undefined), null);
  assert.equal(missingEnvVarKey(""), null);
  assert.equal(missingEnvVarKey("   "), null);
  assert.equal(missingEnvVarKey("Не найден TELEGRAM_API_TOKEN в переменных окружения"), null);
  assert.equal(missingEnvVarKey("TELEGRAM_API_TOKEN=abc"), null);
  assert.equal(missingEnvVarKey("telegram_api_token"), null);
  assert.equal(missingEnvVarKey("9LIVES"), null);
});
