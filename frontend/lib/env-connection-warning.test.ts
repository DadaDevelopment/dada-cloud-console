import assert from "node:assert/strict";
import test from "node:test";

import { isBareConnectionValue, looksLikeConnectionKey } from "./env-connection-warning.ts";

test("flags a bare host copied from the database page", () => {
  assert.equal(isBareConnectionValue("DATABASE_URL", "pg-router.databases.svc.cluster.local"), true);
});

test("accepts a full postgres DSN", () => {
  assert.equal(
    isBareConnectionValue("DATABASE_URL", "postgresql://app:secret@pg-router.databases.svc.cluster.local:5432/megafactory"),
    false
  );
});

test("flags a host:port pair with no scheme", () => {
  assert.equal(isBareConnectionValue("DATABASE_URL", "pg-router.databases.svc.cluster.local:5432"), true);
});

test("ignores keys unrelated to a connection", () => {
  assert.equal(isBareConnectionValue("BOT_TOKEN", "pg-router.databases.svc.cluster.local"), false);
});

test("ignores an empty value", () => {
  assert.equal(isBareConnectionValue("DATABASE_URL", ""), false);
});

test("accepts a redis DSN with a scheme", () => {
  assert.equal(isBareConnectionValue("REDIS_URL", "redis://default:secret@redis.databases.svc.cluster.local:6379/0"), false);
});

test("matches suffix-based custom keys too", () => {
  assert.equal(looksLikeConnectionKey("REPORTING_DB_DSN"), true);
  assert.equal(isBareConnectionValue("REPORTING_DB_DSN", "reporting.databases.svc.cluster.local"), true);
});

test("does not match a key that merely contains DSN mid-word", () => {
  assert.equal(looksLikeConnectionKey("REDSTONE"), false);
});
