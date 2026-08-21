import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { databases } from "./databases.ts";

const deleteBody = databases["databases.delete.modal.body"];

const detailPage = readFileSync(
  new URL("../../../../app/(console)/projects/[projectId]/databases/[name]/page.tsx", import.meta.url),
  "utf8",
);

/**
 * The copy under the delete button is a promise about the data on the shard, and
 * until the reclaim path exists it must not make one it cannot keep.
 *
 * Deleting a database removes its ServiceDatabaseV2 from git; ArgoCD prunes the
 * CR, but the Crossplane Database and Role behind it carry deletionPolicy:
 * Orphan (the same policy MoveApp relies on to re-point an app without losing
 * data), so the logical database and its role keep living on the shard. Nothing
 * in backend or gitops-agent issues a DROP; the leftovers surface only in
 * /admin/db-shards as orphan=true and have so far been cleared by hand.
 */
test("delete copy does not promise a destruction the platform does not perform", () => {
  const forbidden = [
    /необратимо/i,
    /все её данные/i,
    /permanently deletes/i,
    /all its data/i,
  ];
  for (const locale of ["ru", "en"] as const) {
    for (const pattern of forbidden) {
      assert.ok(
        !pattern.test(deleteBody[locale]),
        `${locale} delete copy claims data destruction (${pattern}): ${deleteBody[locale]}`,
      );
    }
  }
  assert.match(deleteBody.ru, /остаются на сервере|не стираются/i);
  assert.match(deleteBody.en, /retained on the server|is not erased/i);
});

/**
 * Restoring a backup asks the operator to type the database name; deleting it
 * asked for nothing. The weaker gate guarded the more destructive action.
 */
test("delete modal is gated by typing the database name", () => {
  assert.match(detailPage, /deleteConfirmName/);
  assert.match(detailPage, /disabled=\{deleteConfirmName !== name\}/);
});
