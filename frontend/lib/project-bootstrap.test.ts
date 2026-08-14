/**
 * Unit tests for runDefaultProjectBootstrap in lib/project-bootstrap.ts - the
 * step ProjectProvider runs when a user's project list comes back empty.
 * Covers the bug this fixes: a failed ensureDefault() call used to be caught
 * silently (`catch { setProjects([]) }`), leaving the user on a blank
 * console with zero signal and no way to retry.
 *
 * Run with Node's built-in test runner and type stripping (no npm ci needed):
 *
 *   cd frontend && npm run test:unit
 */

import test from "node:test";
import assert from "node:assert/strict";

import { runDefaultProjectBootstrap } from "./project-bootstrap.ts";
import type { Project, CreateProjectResponse, ProjectsResponse } from "./types.ts";

function project(id: string): Project {
  return { id, slug: id, display_name: id } as Project;
}

test("ensureDefault success provisions the project and returns the refreshed list", async () => {
  const created: CreateProjectResponse = {
    project_id: "proj-1",
    default_environment_id: "env-1",
    org_id: "org-1",
    role: "owner",
  } as CreateProjectResponse;
  const refreshed: ProjectsResponse = { projects: [project("proj-1")] };

  let ensureDefaultCalls = 0;
  let listCalls = 0;
  const outcome = await runDefaultProjectBootstrap({
    ensureDefault: async () => {
      ensureDefaultCalls++;
      return created;
    },
    listProjects: async () => {
      listCalls++;
      return refreshed;
    },
  });

  assert.equal(ensureDefaultCalls, 1);
  assert.equal(listCalls, 1);
  assert.deepEqual(outcome, { status: "provisioned", projectId: "proj-1", projects: [project("proj-1")] });
});

test("ensureDefault failure with signup_closed sets the signupClosed outcome, not a generic error", async () => {
  const outcome = await runDefaultProjectBootstrap({
    ensureDefault: async () => {
      throw { status: 403, code: "signup_closed" };
    },
    listProjects: async () => ({ projects: [] }),
  });

  assert.deepEqual(outcome, { status: "signupClosed" });
});

test("ensureDefault failure with any other status/code sets a bootstrap error, not a generic empty catch", async () => {
  const outcome = await runDefaultProjectBootstrap({
    ensureDefault: async () => {
      throw { status: 500, code: "internal_error" };
    },
    listProjects: async () => ({ projects: [] }),
  });

  assert.deepEqual(outcome, { status: "error", failure: { status: 500, code: "internal_error" } });
});

test("a follow-up list() failure after a successful ensureDefault also surfaces as a bootstrap error", async () => {
  const outcome = await runDefaultProjectBootstrap({
    ensureDefault: async () => ({
      project_id: "proj-1",
      default_environment_id: "env-1",
      org_id: "org-1",
      role: "owner",
    }) as CreateProjectResponse,
    listProjects: async () => {
      throw { status: 503, code: undefined };
    },
  });

  assert.deepEqual(outcome, { status: "error", failure: { status: 503, code: undefined } });
});

test("retry re-invokes both API calls (each call to runDefaultProjectBootstrap is independent)", async () => {
  let ensureDefaultCalls = 0;
  const deps = {
    ensureDefault: async () => {
      ensureDefaultCalls++;
      if (ensureDefaultCalls === 1) throw { status: 500, code: "internal_error" };
      return {
        project_id: "proj-1",
        default_environment_id: "env-1",
        org_id: "org-1",
        role: "owner",
      } as CreateProjectResponse;
    },
    listProjects: async () => ({ projects: [project("proj-1")] }),
  };

  const first = await runDefaultProjectBootstrap(deps);
  assert.equal(first.status, "error");
  assert.equal(ensureDefaultCalls, 1);

  const second = await runDefaultProjectBootstrap(deps);
  assert.equal(second.status, "provisioned");
  assert.equal(ensureDefaultCalls, 2);
});
