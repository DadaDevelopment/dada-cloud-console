import assert from "node:assert/strict";
import test from "node:test";

import { resolveAppTwin } from "./app-twin.ts";

test("resolveAppTwin builds a descriptor with the console route to the twin app page", () => {
  assert.deepEqual(
    resolveAppTwin({
      project_id: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      project_name: "Old Project",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
    }),
    {
      appName: "botfarm",
      projectId: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      projectName: "Old Project",
      repoFullName: "acme/botfarm",
      href: "/projects/9c9b6f9e-1111-4b1e-9a1a-000000000001/apps/botfarm",
    },
  );
});

test("resolveAppTwin returns null when project_id is missing", () => {
  assert.equal(
    resolveAppTwin({
      project_name: "Old Project",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
    }),
    null,
  );
});

test("resolveAppTwin returns null when app_name is missing", () => {
  assert.equal(
    resolveAppTwin({
      project_id: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      project_name: "Old Project",
      repo_full_name: "acme/botfarm",
    }),
    null,
  );
});

test("resolveAppTwin returns null when project_name is missing", () => {
  assert.equal(
    resolveAppTwin({
      project_id: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
    }),
    null,
  );
});

test("resolveAppTwin returns null when repo_full_name is missing", () => {
  assert.equal(
    resolveAppTwin({
      project_id: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      project_name: "Old Project",
      app_name: "botfarm",
    }),
    null,
  );
});

test("resolveAppTwin returns null when twin_of is absent", () => {
  assert.equal(resolveAppTwin(undefined), null);
  assert.equal(resolveAppTwin(null), null);
});

test("resolveAppTwin returns null for a non-object twin_of", () => {
  assert.equal(resolveAppTwin("acme/botfarm"), null);
  assert.equal(resolveAppTwin(42), null);
});

test("resolveAppTwin tolerates extra unknown fields", () => {
  assert.deepEqual(
    resolveAppTwin({
      project_id: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      project_name: "Old Project",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
      future_field: "whatever",
    }),
    {
      appName: "botfarm",
      projectId: "9c9b6f9e-1111-4b1e-9a1a-000000000001",
      projectName: "Old Project",
      repoFullName: "acme/botfarm",
      href: "/projects/9c9b6f9e-1111-4b1e-9a1a-000000000001/apps/botfarm",
    },
  );
});

test("resolveAppTwin returns null when fields are the wrong type", () => {
  assert.equal(
    resolveAppTwin({
      project_id: 123,
      project_name: "Old Project",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
    }),
    null,
  );
});

test("resolveAppTwin returns null for empty string fields", () => {
  assert.equal(
    resolveAppTwin({
      project_id: "",
      project_name: "Old Project",
      app_name: "botfarm",
      repo_full_name: "acme/botfarm",
    }),
    null,
  );
});
