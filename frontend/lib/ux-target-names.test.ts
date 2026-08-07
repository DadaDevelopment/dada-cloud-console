import test from "node:test";
import assert from "node:assert/strict";
import { templateUxName } from "./ux-target-names.ts";

test("templateUxName puts the placement ahead of the control", () => {
  assert.equal(templateUxName("onramp", "deploy", "n8n"), "tpl.onramp.deploy.n8n");
});

test("templateUxName keeps the three placements apart", () => {
  const names = (["onramp", "apps-empty", "git-import"] as const).map((p) =>
    templateUxName(p, "deploy", "n8n"),
  );
  assert.equal(new Set(names).size, 3);
});

test("templateUxName drops empty parts instead of leaving a hole", () => {
  assert.equal(templateUxName("git-import", "candidate", "repo", ""), "tpl.git-import.candidate.repo");
});

test("templateUxName names a block with no control after the placement", () => {
  assert.equal(templateUxName("apps-empty"), "tpl.apps-empty");
});
