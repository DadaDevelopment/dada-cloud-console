import assert from "node:assert/strict";
import test from "node:test";

import { classifyDomainEndpointError } from "./domain-endpoint-verdict.ts";

test("409 with code fqdn_taken classifies as already_exists", () => {
  const err = Object.assign(new Error("a domain with that FQDN already exists in this environment"), {
    status: 409,
    code: "fqdn_taken",
  });
  assert.equal(classifyDomainEndpointError(err), "already_exists");
});

test("bare 409 without a code still classifies as already_exists", () => {
  const err = Object.assign(new Error("conflict"), { status: 409 });
  assert.equal(classifyDomainEndpointError(err), "already_exists");
});

test("403 with code no_verified_apex classifies as not_verified", () => {
  const err = Object.assign(new Error("apex domain not verified"), {
    status: 403,
    code: "no_verified_apex",
  });
  assert.equal(classifyDomainEndpointError(err), "not_verified");
});

test("bare 403 without a code still classifies as not_verified", () => {
  const err = Object.assign(new Error("forbidden"), { status: 403 });
  assert.equal(classifyDomainEndpointError(err), "not_verified");
});

test("a generic 500 Error classifies as other", () => {
  const err = Object.assign(new Error("internal server error"), { status: 500 });
  assert.equal(classifyDomainEndpointError(err), "other");
});

test("a plain Error with no status or code classifies as other", () => {
  assert.equal(classifyDomainEndpointError(new Error("network error")), "other");
});

test("a non-Error value returns null", () => {
  assert.equal(classifyDomainEndpointError("boom"), null);
  assert.equal(classifyDomainEndpointError(null), null);
  assert.equal(classifyDomainEndpointError(undefined), null);
  assert.equal(classifyDomainEndpointError({ status: 409, code: "fqdn_taken" }), null);
});
