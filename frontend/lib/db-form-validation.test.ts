import assert from "node:assert/strict";
import test from "node:test";

import { dbFormValidationTarget, validateDbForm, type DbFormShape } from "./db-form-validation.ts";

const base: DbFormShape = {
  name: "bookstore-db",
  database: "bookstore-db",
  app_ref: "",
  backup_enabled: false,
  backup_schedule: "daily",
  backup_retention: "7d",
};

test("validateDbForm passes the generated-name happy path", () => {
  assert.deepEqual(validateDbForm(base), []);
});

test("validateDbForm rejects the uppercase name a user types by hand", () => {
  const issues = validateDbForm({ ...base, name: "BookstoreDb" });
  assert.deepEqual(issues, [{ field: "name", reason: "invalid_chars" }]);
});

test("validateDbForm rejects an empty name", () => {
  const issues = validateDbForm({ ...base, name: "" });
  assert.deepEqual(issues, [{ field: "name", reason: "required" }]);
});

test("validateDbForm rejects a name over the postgres 63-byte limit", () => {
  const issues = validateDbForm({ ...base, name: "a".repeat(64) });
  assert.deepEqual(issues, [{ field: "name", reason: "too_long" }]);
});

test("validateDbForm rejects a pg name starting with a digit", () => {
  const issues = validateDbForm({ ...base, database: "1db" });
  assert.deepEqual(issues, [{ field: "database", reason: "invalid_chars" }]);
});

test("validateDbForm names every broken field, not just the first", () => {
  const issues = validateDbForm({ ...base, name: "Плохо", database: "" });
  assert.deepEqual(issues.map((i) => i.field), ["name", "database"]);
});

test("validateDbForm ignores backup fields while backups are off", () => {
  assert.deepEqual(validateDbForm({ ...base, backup_schedule: "never" }), []);
});

test("validateDbForm rejects a bad schedule when backups are on", () => {
  const issues = validateDbForm({ ...base, database: "bookstore", backup_enabled: true, backup_schedule: "never" });
  assert.deepEqual(issues, [{ field: "backup_schedule", reason: "invalid_value" }]);
});

test("dbFormValidationTarget carries field and reason", () => {
  assert.equal(
    dbFormValidationTarget([{ field: "name", reason: "invalid_chars" }]),
    "create_db_modal:validation_error:name:invalid_chars",
  );
});

test("dbFormValidationTarget falls back when handed an empty issue list", () => {
  assert.equal(dbFormValidationTarget([]), "create_db_modal:validation_error");
});
