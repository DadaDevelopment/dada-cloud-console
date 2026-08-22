import { test } from "node:test";
import assert from "node:assert/strict";

import { agentFormFromSnapshot, envLines, parseEnvLines, toolNames } from "./agents.ts";
import type { ResourceSnapshot } from "./types.ts";

test("an env value keeps every character after the first equals sign", () => {
  const parsed = parseEnvLines("DSN=postgres://u:p@h/db?sslmode=require\n  LOG_LEVEL=info  \n\nFLAG");
  assert.deepEqual(parsed, [
    { name: "DSN", value: "postgres://u:p@h/db?sslmode=require" },
    { name: "LOG_LEVEL", value: "info" },
    { name: "FLAG", value: "" },
  ]);
});

test("a line with no name is dropped rather than saved as an empty variable", () => {
  assert.deepEqual(parseEnvLines("=orphan\n   \nKEEP=1"), [{ name: "KEEP", value: "1" }]);
});

test("the editor re-fills itself with the tools and env the agent already has", () => {
  const snapshot: ResourceSnapshot = {
    id: "1",
    project_id: "p",
    environment_id: "e",
    kind: "ManagedAgent",
    name: "support-bot",
    phase: "Ready",
    last_synced_at: "2026-08-22T10:00:00Z",
    summary_json: {
      display_name: "Support bot",
      description: "answers tickets",
      prompt: "Ты помощник.\n\nОтвечай коротко.",
      prompt_version: "v3",
      model_config: "gpt-oss",
      tools: [{ name: "dada-mcp" }, { name: "search-mcp" }],
      env: [{ name: "TZ", value: "Europe/Moscow" }],
    },
  };

  const form = agentFormFromSnapshot(snapshot);

  assert.deepEqual(form.tools, ["dada-mcp", "search-mcp"]);
  assert.equal(form.env, "TZ=Europe/Moscow");
  assert.equal(form.prompt, "Ты помощник.\n\nОтвечай коротко.");
  assert.equal(form.prompt_version, "v3");
});

test("a snapshot with no tools or env fills an empty editor instead of throwing", () => {
  const form = agentFormFromSnapshot({
    id: "1",
    project_id: "p",
    kind: "ManagedAgent",
    name: "bare",
    summary_json: {},
    last_synced_at: "2026-08-22T10:00:00Z",
  });
  assert.deepEqual(form.tools, []);
  assert.equal(form.env, "");
  assert.equal(form.display_name, "");
});

test("stored env entries survive a round trip through the textarea", () => {
  const stored = { env: [{ name: "A", value: "1" }, { name: "B", value: "x=y" }] };
  assert.deepEqual(parseEnvLines(envLines(stored)), [
    { name: "A", value: "1" },
    { name: "B", value: "x=y" },
  ]);
  assert.deepEqual(toolNames(stored), []);
});
