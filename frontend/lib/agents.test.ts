import { test } from "node:test";
import assert from "node:assert/strict";

import {
  agentFormFromSnapshot,
  customTools,
  customToolToRef,
  envLines,
  isConsoleOwnedAgent,
  parseEnvLines,
  parseHeaderLines,
  toolNames,
} from "./agents.ts";
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

test("an agent written into git by hand is not editable here", () => {
  assert.equal(isConsoleOwnedAgent("Agent"), false);
  assert.equal(isConsoleOwnedAgent("ManagedAgent"), true);
});

test("custom MCP servers round-trip through the editor", () => {
  const summary = {
    tools: [
      { name: "platform-task-tools" },
      {
        name: "sandbox-notion",
        url: "https://mcp.notion.com/mcp",
        protocol: "SSE",
        headers: [{ name: "Authorization", value: "Bearer ${NOTION_TOKEN}" }],
      },
    ],
  };

  assert.deepEqual(toolNames(summary), ["platform-task-tools"]);

  const own = customTools(summary);
  assert.deepEqual(own.length, 1);
  assert.deepEqual(own[0].headers, "Authorization: Bearer ${NOTION_TOKEN}");

  const ref = customToolToRef(own[0]);
  assert.deepEqual(ref.url, "https://mcp.notion.com/mcp");
  assert.deepEqual(ref.protocol, "SSE");
  assert.deepEqual(ref.headers, [{ name: "Authorization", value: "Bearer ${NOTION_TOKEN}" }]);
});

test("a header value keeps every colon after the first", () => {
  const headers = parseHeaderLines("Authorization: Bearer a:b:c\nX-Base: https://x.example.com/v1");
  assert.deepEqual(headers, [
    { name: "Authorization", value: "Bearer a:b:c" },
    { name: "X-Base", value: "https://x.example.com/v1" },
  ]);
});
