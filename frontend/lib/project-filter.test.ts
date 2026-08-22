import assert from "node:assert/strict";
import test from "node:test";
import { partitionProjects } from "./project-filter.ts";
import type { Project } from "./types.ts";

function project(name: string, appCount: number, displayName?: string): Project {
  return {
    id: `id-${name}`,
    name,
    display_name: displayName ?? name,
    app_count: appCount,
  } as Project;
}

test("populated projects come before empty ones, most apps first", () => {
  const { populated, empty } = partitionProjects(
    [project("e2e-b", 0), project("internal", 3), project("platform", 33), project("e2e-a", 0)],
    "",
  );
  assert.deepEqual(
    populated.map((p) => p.name),
    ["platform", "internal"],
  );
  assert.deepEqual(
    empty.map((p) => p.name),
    ["e2e-a", "e2e-b"],
  );
});

test("filter matches slug and display name, case-insensitively", () => {
  const projects = [
    project("prj-9f2c", 33, "Платформа"),
    project("agent-sandbox", 2, "Agent Sandbox"),
    project("e2e-junk", 0),
  ];
  assert.deepEqual(
    partitionProjects(projects, "платфор").populated.map((p) => p.name),
    ["prj-9f2c"],
  );
  assert.deepEqual(
    partitionProjects(projects, "9F2C").populated.map((p) => p.name),
    ["prj-9f2c"],
  );
  assert.deepEqual(
    partitionProjects(projects, "SANDBOX").populated.map((p) => p.name),
    ["agent-sandbox"],
  );
  const none = partitionProjects(projects, "nothing-like-this");
  assert.equal(none.populated.length, 0);
  assert.equal(none.empty.length, 0);
});

test("filter keeps matching empty projects instead of hiding them", () => {
  const { populated, empty } = partitionProjects(
    [project("e2e-junk", 0), project("platform", 33)],
    "e2e",
  );
  assert.deepEqual(populated, []);
  assert.deepEqual(
    empty.map((p) => p.name),
    ["e2e-junk"],
  );
});

test("a project without app_count is treated as empty, not dropped", () => {
  const legacy = { id: "x", name: "legacy", display_name: "legacy" } as Project;
  const { populated, empty } = partitionProjects([legacy], "");
  assert.deepEqual(populated, []);
  assert.deepEqual(
    empty.map((p) => p.name),
    ["legacy"],
  );
});

test("input order is not mutated", () => {
  const projects = [project("b", 0), project("a", 5)];
  partitionProjects(projects, "");
  assert.deepEqual(
    projects.map((p) => p.name),
    ["b", "a"],
  );
});
