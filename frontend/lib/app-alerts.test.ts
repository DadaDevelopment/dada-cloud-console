import assert from "node:assert/strict";
import test from "node:test";

import {
  alertChipAction,
  getOperationalAppAlerts,
  missingEnvVarKey,
  offersStartCommandFix,
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

test("offersStartCommandFix covers the entrypoint-import crash the platform's own launch line caused", () => {
  assert.equal(offersStartCommandFix("app_entrypoint_import"), true);
});

test("offersStartCommandFix keeps the CLI case it already served", () => {
  assert.equal(offersStartCommandFix("app_needs_args"), true);
});

test("offersStartCommandFix refuses every cause the start command cannot fix", () => {
  assert.equal(offersStartCommandFix("app_code"), false);
  assert.equal(offersStartCommandFix("platform_network"), false);
  assert.equal(offersStartCommandFix("resource_limit"), false);
  assert.equal(offersStartCommandFix("missing_env_var"), false);
  assert.equal(offersStartCommandFix(undefined), false);
});

test("alertChipAction sends a volume alert straight to Storage", () => {
  const alert: AppAlert = { type: "volume", detected_at: "2026-08-20T00:00:00Z" };
  assert.deepEqual(alertChipAction(alert), {
    labelKey: "apps.alerts.volume.cta",
    uxMarker: "apps_alert_chip:volume",
  });
});

test("alertChipAction sends a url alert to the logs it shares with the diagnosis text", () => {
  const alert: AppAlert = { type: "url", detected_at: "2026-08-20T00:00:00Z" };
  assert.deepEqual(alertChipAction(alert), {
    labelKey: "apps.alerts.url.cta",
    uxMarker: "apps_alert_chip:url",
  });
});

test("alertChipAction sends a missing_env_var crash to adding the variable, not just the logs", () => {
  const alert: AppAlert = { type: "crash", cause_kind: "missing_env_var", detected_at: "2026-08-20T00:00:00Z" };
  assert.deepEqual(alertChipAction(alert), {
    labelKey: "apps.alerts.crash.cause.missingEnvVar.cta",
    uxMarker: "apps_alert_chip:crash",
  });
});

test("alertChipAction sends both start-command crash causes to the same start-command lever", () => {
  const needsArgs: AppAlert = { type: "crash", cause_kind: "app_needs_args", detected_at: "2026-08-20T00:00:00Z" };
  const entrypointImport: AppAlert = {
    type: "crash",
    cause_kind: "app_entrypoint_import",
    detected_at: "2026-08-20T00:00:00Z",
  };
  assert.equal(alertChipAction(needsArgs).labelKey, "apps.alerts.crash.cause.needsArgs.cta");
  assert.equal(alertChipAction(entrypointImport).labelKey, "apps.alerts.crash.cause.needsArgs.cta");
});

test("alertChipAction sends a volume-shaped crash cause (bytes or inodes) to Storage", () => {
  const bytes: AppAlert = { type: "crash", cause_kind: "platform_storage", detected_at: "2026-08-20T00:00:00Z" };
  const inodes: AppAlert = {
    type: "crash",
    cause_kind: "platform_storage_inodes",
    detected_at: "2026-08-20T00:00:00Z",
  };
  assert.equal(alertChipAction(bytes).labelKey, "apps.alerts.volume.cta");
  assert.equal(alertChipAction(inodes).labelKey, "apps.alerts.volume.cta");
});

test("alertChipAction never invents a lever for a crash cause it cannot name", () => {
  const appCode: AppAlert = { type: "crash", cause_kind: "app_code", detected_at: "2026-08-20T00:00:00Z" };
  const empty: AppAlert = { type: "crash", detected_at: "2026-08-20T00:00:00Z" };
  assert.equal(alertChipAction(appCode).labelKey, "apps.alerts.crash.cta");
  assert.equal(alertChipAction(empty).labelKey, "apps.alerts.crash.cta");
});
