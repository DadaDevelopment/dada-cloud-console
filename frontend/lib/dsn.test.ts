import assert from "node:assert/strict";
import test from "node:test";

import { maskDsnPassword } from "./dsn.ts";

test("masks the password and keeps every other part addressable", () => {
  assert.equal(
    maskDsnPassword("postgresql://app:s3cr3t@pg-router.databases.svc.cluster.local:5432/megafactory"),
    "postgresql://app:••••••••@pg-router.databases.svc.cluster.local:5432/megafactory",
  );
});

test("masks percent-encoded passwords whole", () => {
  assert.equal(maskDsnPassword("postgresql://app:a%40b%2Fc@host:5432/db"), "postgresql://app:••••••••@host:5432/db");
});

test("leaves a credential-less DSN alone", () => {
  assert.equal(maskDsnPassword("postgresql://host:5432/db"), "postgresql://host:5432/db");
});
