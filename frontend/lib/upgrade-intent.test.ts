import assert from "node:assert/strict";
import test from "node:test";
import { saveUpgradeIntent, takeUpgradeIntent, type IntentStore } from "./upgrade-intent.ts";

function fakeStore(seed: Record<string, string> = {}): IntentStore & { data: Record<string, string> } {
  const data = { ...seed };
  return {
    data,
    getItem: (k) => (k in data ? data[k] : null),
    setItem: (k, v) => {
      data[k] = v;
    },
    removeItem: (k) => {
      delete data[k];
    },
  };
}

test("the intent survives the checkout round trip", () => {
  const store = fakeStore();
  saveUpgradeIntent({ returnTo: "/projects/p1/apps", resource: "storage_gb", plan: "business", label: "jellyfin" }, store);
  const got = takeUpgradeIntent(store);
  assert.equal(got?.returnTo, "/projects/p1/apps");
  assert.equal(got?.plan, "business");
  assert.equal(got?.label, "jellyfin");
});

test("reading consumes it, so a stale intent cannot resurface later", () => {
  const store = fakeStore();
  saveUpgradeIntent({ returnTo: "/projects/p1/apps", resource: "apps", plan: "startup" }, store);
  takeUpgradeIntent(store);
  assert.equal(takeUpgradeIntent(store), null);
});

test("no intent stored reads as none", () => {
  assert.equal(takeUpgradeIntent(fakeStore()), null);
  assert.equal(takeUpgradeIntent(null), null);
});

test("garbage is ignored rather than thrown", () => {
  assert.equal(takeUpgradeIntent(fakeStore({ "dada.upgrade-intent": "{not json" })), null);
  assert.equal(takeUpgradeIntent(fakeStore({ "dada.upgrade-intent": "null" })), null);
});

test("only same-origin paths come back", () => {
  const external = fakeStore({ "dada.upgrade-intent": JSON.stringify({ returnTo: "https://evil.example/x", resource: "", plan: "" }) });
  assert.equal(takeUpgradeIntent(external), null, "a stored URL must never become an off-site redirect target");
  const protocolRelative = fakeStore({ "dada.upgrade-intent": JSON.stringify({ returnTo: "//evil.example/x", resource: "", plan: "" }) });
  assert.equal(takeUpgradeIntent(protocolRelative), null);
});

test("a store that refuses to work degrades to no intent", () => {
  const broken: IntentStore = {
    getItem: () => {
      throw new Error("denied");
    },
    setItem: () => {
      throw new Error("denied");
    },
    removeItem: () => {
      throw new Error("denied");
    },
  };
  saveUpgradeIntent({ returnTo: "/x", resource: "apps", plan: "startup" }, broken);
  assert.equal(takeUpgradeIntent(broken), null);
});
