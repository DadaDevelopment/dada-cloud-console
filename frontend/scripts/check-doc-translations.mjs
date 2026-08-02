/**
 * Build guard: every English guide under content/docs/ has a Russian translation.
 *
 * Run standalone:  npm run check:docs
 * Runs on its own: `prebuild`, so `npm run build` (CI and the Dockerfile both
 * call it) fails before Next compiles anything.
 *
 * Why a build gate and not a note in a doc: getDocMarkdown() falls back to the
 * English original when content/docs/ru/<slug>.md is missing, so an untranslated
 * guide does not 404 and does not look broken — /developer/<slug> just serves
 * English prose inside a lang="ru" document. That is invisible in review and
 * unrankable in Yandex, which is how the whole /developer tree sat unfound for a
 * Russian "s3" query until 069f0b0. The next author who adds an English guide
 * gets a red build instead of a silent hole.
 *
 * Plain .mjs rather than a lib/*.test.ts case because CI builds on node 20,
 * which has no --experimental-strip-types and therefore never runs test:unit.
 *
 * Scope rule — UNTRANSLATED_BY_DESIGN is the only exemption list:
 *   - README.md is the directory's own index, not a routed page.
 *   - mcp-tool-reference.md is a machine-facing table of tool names and JSON
 *     argument keys; those identifiers are English in the protocol itself, so a
 *     Russian copy would translate the prose around unchanged payloads and then
 *     rot separately from the tool surface it documents.
 * Anything else missing a translation is a defect, not a decision.
 */

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const DOCS_DIR = path.join(HERE, "..", "content", "docs");
const RU_DIR = path.join(DOCS_DIR, "ru");

const UNTRANSLATED_BY_DESIGN = new Set(["README.md", "mcp-tool-reference.md"]);

const markdownFiles = (dir) =>
  fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".md"))
    .map((entry) => entry.name)
    .sort();

const english = markdownFiles(DOCS_DIR);
const russian = markdownFiles(RU_DIR);
const russianSet = new Set(russian);
const englishSet = new Set(english);
const cyrillic = /\p{Script=Cyrillic}/u;

const problems = [];

const missing = english.filter((n) => !UNTRANSLATED_BY_DESIGN.has(n) && !russianSet.has(n));
if (missing.length) {
  problems.push(
    `${missing.length} guide(s) have no Russian translation: ${missing.join(", ")}\n` +
      "  Translate into content/docs/ru/, or add to UNTRANSLATED_BY_DESIGN with the reason.",
  );
}

const orphans = russian.filter((n) => !englishSet.has(n));
if (orphans.length) {
  problems.push(`${orphans.length} translation(s) have no English original: ${orphans.join(", ")}`);
}

const copied = russian.filter((n) => !cyrillic.test(fs.readFileSync(path.join(RU_DIR, n), "utf8")));
if (copied.length) {
  problems.push(`${copied.length} file(s) under ru/ are not translated, only copied: ${copied.join(", ")}`);
}

if (problems.length) {
  console.error("check-doc-translations FAILED\n" + problems.map((p) => `- ${p}`).join("\n"));
  process.exit(1);
}

console.log(
  `check-doc-translations OK — ${russian.length} translated, ${UNTRANSLATED_BY_DESIGN.size} English by design`,
);
