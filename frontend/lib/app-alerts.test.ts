import assert from "node:assert/strict";
import test from "node:test";

import { getOperationalAppAlerts, type AppAlert } from "./app-alerts.ts";

const alerts: AppAlert[] = [
  { type: "crash", detected_at: "2026-08-04T00:00:00Z" },
  { type: "volume", detected_at: "2026-08-04T00:00:00Z" },
  { type: "url", detected_at: "2026-08-04T00:00:00Z" },
];

test("getOperationalAppAlerts keeps a public-route failure actionable", () => {
  assert.deepEqual(getOperationalAppAlerts(alerts).map((alert) => alert.type), ["crash", "volume", "url"]);
});
