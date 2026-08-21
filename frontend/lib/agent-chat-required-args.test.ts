import assert from "node:assert/strict";
import test from "node:test";

import { missingRequiredArgs } from "./agent-chat-required-args.ts";

test("createS3Bucket with only bucket_name stays approvable: the handler derives name from it", () => {
  const args = { name: "", bucket_name: "dating-service-assets", region: "ru-1" };
  const missing = missingRequiredArgs("createS3Bucket", args);
  assert.deepEqual(missing, []);
});

test("createS3Bucket with only name stays approvable", () => {
  const missing = missingRequiredArgs("createS3Bucket", { name: "dating-assets" });
  assert.deepEqual(missing, []);
});

test("createS3Bucket with both fields filled is approvable", () => {
  const args = { name: "dating-assets", bucket_name: "dating-service-assets", region: "ru-1" };
  const missing = missingRequiredArgs("createS3Bucket", args);
  assert.deepEqual(missing, []);
});

test("createS3Bucket missing both name and bucket_name is the only blocked shape", () => {
  const missing = missingRequiredArgs("createS3Bucket", { region: "ru-1" });
  assert.deepEqual(missing, ["name"]);
});

test("createEndpoint with a blank fqdn is blocked", () => {
  assert.deepEqual(missingRequiredArgs("createEndpoint", { fqdn: "   " }), ["fqdn"]);
});

test("createDatabase requires both name and database", () => {
  assert.deepEqual(missingRequiredArgs("createDatabase", { name: "orders" }), ["database"]);
  assert.deepEqual(missingRequiredArgs("createDatabase", { name: "orders", database: "orders_db" }), []);
});

test("createApp requires name only", () => {
  assert.deepEqual(missingRequiredArgs("createApp", { image: "nginx:latest" }), ["name"]);
  assert.deepEqual(missingRequiredArgs("createApp", { name: "web", image: "nginx:latest" }), []);
});

test("createProject requires slug", () => {
  assert.deepEqual(missingRequiredArgs("createProject", {}), ["slug"]);
  assert.deepEqual(missingRequiredArgs("createProject", { slug: "my-app" }), []);
});

test("connectGitRepo requires repo_full_name and app_name", () => {
  assert.deepEqual(missingRequiredArgs("connectGitRepo", { repo_full_name: "org/repo" }), ["app_name"]);
  assert.deepEqual(
    missingRequiredArgs("connectGitRepo", { repo_full_name: "org/repo", app_name: "web" }),
    [],
  );
});

test("restoreDatabase accepts either backup_id or backupId alias", () => {
  assert.deepEqual(missingRequiredArgs("restoreDatabase", { name: "orders" }), ["backup_id"]);
  assert.deepEqual(missingRequiredArgs("restoreDatabase", { name: "orders", backupId: "b-1" }), []);
});

test("an unknown tool is never blocked (fail-open, not fail-closed)", () => {
  assert.deepEqual(missingRequiredArgs("someBrandNewTool", {}), []);
  assert.deepEqual(missingRequiredArgs("someBrandNewTool", { anything: "" }), []);
});

test("a tool with no args object at all is not blocked when it has no known requirement", () => {
  assert.deepEqual(missingRequiredArgs("restartApp", null), []);
});
