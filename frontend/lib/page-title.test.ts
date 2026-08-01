/**
 * Unit tests for contextual page titles (lib/page-title.ts).
 *
 * Run with Node's built-in test runner and type stripping:
 *
 *   cd frontend && npm run test:unit
 *
 * What is worth pinning: the deepest console routes (the ones people actually
 * paste into chats) name the resource they point at, a path the map does not
 * know still produces a usable title rather than a raw segment, and the title
 * never contains anything the URL did not already carry.
 */

import test from "node:test";
import assert from "node:assert/strict";

import { describePath } from "./page-title.ts";

const PROJECT = "cd6481fa-e5c4-4ff8-a254-abdcdff4b42a";

test("app sub-tab names both the app and the tab", () => {
  const { title } = describePath(`/projects/${PROJECT}/apps/fonbet-value/files`, "ru");
  assert.equal(title, "fonbet-value · Файлы — DADA Cloud");
});

test("project name replaces the placeholder when the client knows it", () => {
  const { title } = describePath(
    `/projects/${PROJECT}/apps/fonbet-value/files`,
    "ru",
    "Ставки",
  );
  assert.equal(title, "fonbet-value · Файлы · Ставки — DADA Cloud");
});

test("app overview falls back to the singular section label", () => {
  const { title } = describePath(`/projects/${PROJECT}/apps/fonbet-value`, "en", "Bets");
  assert.equal(title, "fonbet-value · Application · Bets — DADA Cloud");
});

test("a build page carries its build number", () => {
  const { title } = describePath(`/projects/${PROJECT}/apps/api/builds/1723`, "en", "Bets");
  assert.equal(title, "api · Build 1723 · Bets — DADA Cloud");
});

test("list pages name the section", () => {
  const { title } = describePath(`/projects/${PROJECT}/databases`, "ru", "Ставки");
  assert.equal(title, "Базы данных · Ставки — DADA Cloud");
});

test("a named database is titled with its own name", () => {
  const { title } = describePath(`/projects/${PROJECT}/databases/orders`, "ru", "Ставки");
  assert.equal(title, "orders · База данных · Ставки — DADA Cloud");
});

test("monitoring keeps the section label, not the opaque app id", () => {
  const { title } = describePath(`/projects/${PROJECT}/monitoring/${PROJECT}`, "ru");
  assert.equal(title, "Мониторинг — DADA Cloud");
});

test("admin pages are named and scoped", () => {
  assert.equal(describePath("/admin/costs", "ru").title, "Экономика · Админка — DADA Cloud");
  assert.equal(describePath("/admin", "en").title, "Admin — DADA Cloud");
});

test("unknown paths fall back to the generic console title", () => {
  const { title, description } = describePath("/something/else", "en");
  assert.equal(title, "DADA Cloud Console");
  assert.equal(description, "GitOps-backed self-service cloud console");
});

test("the query string is never part of the title", () => {
  const { title } = describePath(
    `/projects/${PROJECT}/apps/fonbet-value/files?envId=761f69ce-5269-4a23-b2c7-690f93e1519a`,
    "ru",
  );
  assert.ok(!title.includes("761f69ce"));
  assert.equal(title, "fonbet-value · Файлы — DADA Cloud");
});
