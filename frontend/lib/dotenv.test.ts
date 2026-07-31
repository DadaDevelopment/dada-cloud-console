/**
 * Unit tests for the pasted-env parser (lib/dotenv.ts).
 *
 *   cd frontend && npm run test:unit
 *
 * The properties worth pinning are the ones a user hits while pasting a real
 * file: shell noise is tolerated, a comma inside a value does NOT split the
 * value in half, and anything unparseable is reported rather than swallowed —
 * a silently dropped line means an app that boots without a variable the user
 * believes they set.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { parseEnvBlob } from "./dotenv.ts";

test("parses a plain .env file", () => {
  const { vars, errors } = parseEnvBlob("BOT_TOKEN=123:abc\nPORT=8080\n");
  assert.deepEqual(vars, [
    { key: "BOT_TOKEN", value: "123:abc" },
    { key: "PORT", value: "8080" },
  ]);
  assert.deepEqual(errors, []);
});

test("ignores comments, blank lines and export prefixes", () => {
  const { vars, errors } = parseEnvBlob("# secrets\n\nexport API_KEY=k1\n  \n");
  assert.deepEqual(vars, [{ key: "API_KEY", value: "k1" }]);
  assert.deepEqual(errors, []);
});

test("strips quotes and unescapes newlines in double-quoted values", () => {
  const { vars } = parseEnvBlob(`A="line1\\nline2"\nB='raw\\nvalue'\n`);
  assert.deepEqual(vars, [
    { key: "A", value: "line1\nline2" },
    { key: "B", value: "raw\\nvalue" },
  ]);
});

test("splits a comma-separated one-liner", () => {
  const { vars } = parseEnvBlob("A=1, B=2,C=3");
  assert.deepEqual(vars, [
    { key: "A", value: "1" },
    { key: "B", value: "2" },
    { key: "C", value: "3" },
  ]);
});

test("keeps commas that are part of a value", () => {
  const { vars } = parseEnvBlob("ALLOWED_HOSTS=a.ru,b.ru\nQUOTED=\"x, y\"");
  assert.deepEqual(vars, [
    { key: "ALLOWED_HOSTS", value: "a.ru,b.ru" },
    { key: "QUOTED", value: "x, y" },
  ]);
});

test("reports lines it cannot use instead of dropping them", () => {
  const { vars, errors } = parseEnvBlob("GOOD=1\njust some prose\n9BAD=2\n");
  assert.deepEqual(vars, [{ key: "GOOD", value: "1" }]);
  assert.deepEqual(errors, ["just some prose", "9BAD=2"]);
});

test("last assignment of a repeated key wins", () => {
  const { vars } = parseEnvBlob("K=first\nK=second");
  assert.deepEqual(vars, [{ key: "K", value: "second" }]);
});

test("keeps '=' inside a value", () => {
  const { vars } = parseEnvBlob("TOKEN=abc==");
  assert.deepEqual(vars, [{ key: "TOKEN", value: "abc==" }]);
});
