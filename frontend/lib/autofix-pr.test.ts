/**
 * Unit tests for lib/autofix-pr.ts.
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * The property that matters: a completed auto-fix run that produced a pull
 * request must always surface exactly one link per distinct PR, newest first,
 * and nothing must surface for runs that produced no branch. The live case
 * this guards is freitorsk 2026-09-12 (app chirping-kolyaska): the agent
 * opened PR #1, the console never named it, and the user rebuilt the same
 * commit three times and deleted the app.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { selectAutofixPrs, AUTOFIX_PR_LIMIT } from "./autofix-pr.ts";
import type { CloudTask } from "./types.ts";

const PR_BASE = "https://example.com/pull/";

function task(over: Partial<CloudTask>): CloudTask {
  return {
    id: over.id ?? "t1",
    project_id: "p",
    environment_id: "e",
    app_name: "app",
    task_type: "autofix",
    status: over.status ?? "completed",
    pr_url: over.pr_url,
    artifacts: [],
    created_at: over.created_at ?? "2026-09-12T10:49:47Z",
    updated_at: over.created_at ?? "2026-09-12T10:49:47Z",
  };
}

test("the freitorsk case: a completed run with a PR is surfaced", () => {
  const prs = selectAutofixPrs([
    task({
      id: "27c0491d-58d0-4e62-9eb1-bc0f6d3b6629",
      pr_url: "https://github.com/freitorsk/chirping-kolyaska/pull/1",
    }),
  ]);
  assert.equal(prs.length, 1);
  assert.equal(prs[0].url, "https://github.com/freitorsk/chirping-kolyaska/pull/1");
});

test("a failed run produced no branch and must not be offered", () => {
  assert.deepEqual(selectAutofixPrs([task({ status: "failed", pr_url: "" })]), []);
});

test("a running run has nothing to link yet", () => {
  assert.deepEqual(selectAutofixPrs([task({ status: "running", pr_url: undefined })]), []);
});

test("a completed run with an empty or whitespace pr_url is not a link", () => {
  assert.deepEqual(selectAutofixPrs([task({ pr_url: "" }), task({ id: "t2", pr_url: "   " })]), []);
});

test("two rows reporting the same pull request collapse to one link", () => {
  const dup = "https://github.com/Poksno/fonbet_value/pull/54";
  const prs = selectAutofixPrs([
    task({ id: "a", pr_url: dup, created_at: "2026-08-11T23:21:07Z" }),
    task({ id: "b", pr_url: dup, created_at: "2026-08-11T23:21:13Z" }),
  ]);
  assert.equal(prs.length, 1);
  assert.equal(prs[0].id, "b");
});

test("newest run leads so the fix for the latest failure is first", () => {
  const prs = selectAutofixPrs([
    task({ id: "old", pr_url: PR_BASE + "1", created_at: "2026-08-01T00:00:00Z" }),
    task({ id: "new", pr_url: PR_BASE + "2", created_at: "2026-09-01T00:00:00Z" }),
  ]);
  assert.deepEqual(prs.map((p) => p.id), ["new", "old"]);
});

test("a wall of links is capped", () => {
  const many = Array.from({ length: AUTOFIX_PR_LIMIT + 4 }, (_, i) =>
    task({
      id: "t" + String(i),
      pr_url: PR_BASE + String(i),
      created_at: "2026-09-0" + String((i % 9) + 1) + "T00:00:00Z",
    }),
  );
  assert.equal(selectAutofixPrs(many).length, AUTOFIX_PR_LIMIT);
});

test("no tasks, undefined and null are all empty, never a throw", () => {
  assert.deepEqual(selectAutofixPrs([]), []);
  assert.deepEqual(selectAutofixPrs(undefined), []);
  assert.deepEqual(selectAutofixPrs(null), []);
});
