import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

import {
  CONSOLE_ROUTES,
  autolinkConsolePaths,
  isInternalConsolePath,
  isKnownConsoleRoute,
  repairConsoleHref,
  repairConsoleLinks,
} from "./agent-chat-links.ts";

test("isInternalConsolePath accepts console routes", () => {
  assert.equal(isInternalConsolePath("/projects"), true);
  assert.equal(isInternalConsolePath("/projects/p-1/apps/web/settings"), true);
  assert.equal(isInternalConsolePath("/billing"), true);
  assert.equal(isInternalConsolePath("/admin/costs"), true);
  assert.equal(isInternalConsolePath("/ai-studio/keys"), true);
  assert.equal(isInternalConsolePath("/deploy?repo=owner/name"), true);
});

test("isInternalConsolePath rejects anything the router does not own", () => {
  assert.equal(isInternalConsolePath("https://console.dada-tuda.ru/projects"), false);
  assert.equal(isInternalConsolePath("//evil.example/projects"), false);
  assert.equal(isInternalConsolePath("/docs/quickstart"), false);
  assert.equal(isInternalConsolePath("/projectsomething"), false);
  assert.equal(isInternalConsolePath(""), false);
  assert.equal(isInternalConsolePath("mailto:a@b.c"), false);
});

test("bare paths become links", () => {
  assert.equal(
    autolinkConsolePaths("Открой /projects/p-1/apps и жми Deploy"),
    "Открой [`/projects/p-1/apps`](/projects/p-1/apps) и жми Deploy",
  );
});

test("inline code paths become links keeping the code label", () => {
  assert.equal(
    autolinkConsolePaths("Смотри `/projects/p-1/databases/db1`."),
    "Смотри [`/projects/p-1/databases/db1`](/projects/p-1/databases/db1).",
  );
});

test("trailing punctuation stays outside the link", () => {
  assert.equal(
    autolinkConsolePaths("Иди в /billing."),
    "Иди в [`/billing`](/billing).",
  );
  assert.equal(
    autolinkConsolePaths("Это /admin/costs, там цифры"),
    "Это [`/admin/costs`](/admin/costs), там цифры",
  );
});

test("existing links are left alone", () => {
  const src = "Открой [настройки](/projects/p-1/settings)";
  assert.equal(autolinkConsolePaths(src), src);
});

test("a link whose label is itself a path is not double wrapped", () => {
  const src = "[/projects/p-1/apps](/projects/p-1/apps)";
  assert.equal(autolinkConsolePaths(src), src);
});

test("fenced code is untouched", () => {
  const src = "```\ncurl https://x/api/v1 /projects/p-1/apps\n```";
  assert.equal(autolinkConsolePaths(src), src);
});

test("non-console paths are not linked", () => {
  const src = "Файл лежит в /etc/nginx/nginx.conf, а образ в /usr/bin";
  assert.equal(autolinkConsolePaths(src), src);
});

test("several paths in one line all get linked", () => {
  assert.equal(
    autolinkConsolePaths("Сначала /projects/p-1/apps, потом /billing"),
    "Сначала [`/projects/p-1/apps`](/projects/p-1/apps), потом [`/billing`](/billing)",
  );
});

test("empty input survives", () => {
  assert.equal(autolinkConsolePaths(""), "");
});

test("path at the very start of the message is linked", () => {
  assert.equal(
    autolinkConsolePaths("/projects/p-1/apps/web/logs"),
    "[`/projects/p-1/apps/web/logs`](/projects/p-1/apps/web/logs)",
  );
});

test("isKnownConsoleRoute rejects the plausible-but-dead deep links", () => {
  assert.equal(isKnownConsoleRoute("/billing"), false);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps/web/logs"), false);
  assert.equal(isKnownConsoleRoute("/projects/p-1/settings"), false);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps/web/env"), false);
  assert.equal(isKnownConsoleRoute("/admin/users"), false);
  assert.equal(isKnownConsoleRoute("/"), false);
  assert.equal(isKnownConsoleRoute("//evil.example/projects"), false);
  assert.equal(isKnownConsoleRoute("https://console.dada-tuda.ru/projects"), false);
});

test("isKnownConsoleRoute accepts the routes the console really renders", () => {
  assert.equal(isKnownConsoleRoute("/projects"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps/web"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps/web#logs"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/apps/web/settings"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/monitoring/app-1"), true);
  assert.equal(isKnownConsoleRoute("/projects/p-1/billing"), true);
  assert.equal(isKnownConsoleRoute("/admin/costs"), true);
  assert.equal(isKnownConsoleRoute("/ai-studio"), true);
  assert.equal(isKnownConsoleRoute("/billing/return"), true);
  assert.equal(isKnownConsoleRoute("/deploy?repo=owner/name"), true);
});

test("CONSOLE_ROUTES matches the pages on disk", () => {
  const appDir = path.join(import.meta.dirname, "..", "app");
  const found: string[] = [];

  const walk = (dir: string, route: string) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        const name = entry.name;
        if (name.startsWith("_") || name.startsWith("@") || name === "node_modules") continue;
        const isGroup = name.startsWith("(") && name.endsWith(")");
        walk(path.join(dir, name), isGroup ? route : route + "/" + name);
        continue;
      }
      if (entry.name === "page.tsx" && route !== "") found.push(route);
    }
  };
  walk(appDir, "");

  const tops = new Set(CONSOLE_ROUTES.map((r) => r.split("/")[1]));
  const onDisk = found.filter((p) => tops.has(p.split("/")[1])).sort();
  assert.deepEqual(onDisk, [...CONSOLE_ROUTES].sort());
});

test("repairs the malformed console links the assistant writes on production", () => {
  assert.equal(
    repairConsoleHref("projects/7a387969-e082-415c-8b61-1f53f7e18295/git/import"),
    "/projects/7a387969-e082-415c-8b61-1f53f7e18295/git/import",
  );
  assert.equal(
    repairConsoleHref("project/7a387969-e082-415c-8b61-1f53f7e18295/git/import"),
    "/projects/7a387969-e082-415c-8b61-1f53f7e18295/git/import",
  );
  assert.equal(repairConsoleHref("projects/p-1/apps"), "/projects/p-1/apps");
});

test("leaves hrefs that already work, and ones it cannot verify, alone", () => {
  assert.equal(repairConsoleHref("/projects/p-1/apps"), null);
  assert.equal(repairConsoleHref("https://example.com/projects/p-1/apps"), null);
  assert.equal(repairConsoleHref("mailto:hi@example.com"), null);
  assert.equal(repairConsoleHref("#anchor"), null);
  assert.equal(repairConsoleHref("//evil.example.com/projects/p-1/apps"), null);
  assert.equal(repairConsoleHref("projects/p-1/apps/a-1/logs"), null);
  assert.equal(repairConsoleHref(""), null);
});

test("rewrites link targets in an answer without touching the text", () => {
  const answer =
    "Если ваш код на GitHub: [Подключите репозиторий](projects/p-1/git/import). " +
    "Иначе загрузите папку: [страница приложений](project/p-1/apps).";
  assert.equal(
    repairConsoleLinks(answer),
    "Если ваш код на GitHub: [Подключите репозиторий](/projects/p-1/git/import). " +
      "Иначе загрузите папку: [страница приложений](/projects/p-1/apps).",
  );
});

test("a repaired link is one the panel will route internally", () => {
  const repaired = repairConsoleHref("projects/p-1/apps");
  assert.ok(repaired);
  assert.equal(isInternalConsolePath(repaired), true);
  assert.equal(isKnownConsoleRoute(repaired), true);
});

test("repair runs before autolinking and the two do not fight", () => {
  const answer = "Загрузите проект здесь: [страница](projects/p-1/apps), либо откройте /projects/p-1/git.";
  const out = autolinkConsolePaths(repairConsoleLinks(answer));
  assert.ok(out.includes("[страница](/projects/p-1/apps)"));
  assert.ok(out.includes("[`/projects/p-1/git`](/projects/p-1/git)"));
});

test("a sentence-ending period is not swallowed into a deep path", () => {
  assert.equal(
    autolinkConsolePaths("Открой /projects/p-1/git."),
    "Открой [`/projects/p-1/git`](/projects/p-1/git).",
  );
  assert.equal(
    autolinkConsolePaths("Логи тут: /projects/p-1/apps/web/settings. Дальше жми Save"),
    "Логи тут: [`/projects/p-1/apps/web/settings`](/projects/p-1/apps/web/settings). Дальше жми Save",
  );
});
