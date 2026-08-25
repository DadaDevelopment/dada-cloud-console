import assert from "node:assert/strict";
import test from "node:test";
import { deriveAuthorizationDomain } from "./domain-authorization.ts";

test("starts verification at the entered subdomain when it has no authorization", () => {
  assert.deepEqual(deriveAuthorizationDomain("fanclub.run.place", []), {
    domain: "fanclub.run.place",
    existing: null,
  });
});

test("uses the most-specific existing authorization that covers the hostname", () => {
  const parent = { apex_domain: "run.place", status: "verified" };
  const delegated = { apex_domain: "fanclub.run.place", status: "verified" };

  assert.deepEqual(
    deriveAuthorizationDomain("www.fanclub.run.place", [parent, delegated]),
    { domain: "fanclub.run.place", existing: delegated },
  );
});

test("does not reuse a pending parent authorization for a delegated subdomain", () => {
  const pendingParent = { apex_domain: "run.place", status: "pending" };

  assert.deepEqual(
    deriveAuthorizationDomain("fanclub.run.place", [pendingParent]),
    { domain: "fanclub.run.place", existing: null },
  );
});
