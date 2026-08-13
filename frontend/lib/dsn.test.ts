import { describe, expect, it } from "vitest";

import { maskDsnPassword } from "./dsn";

describe("maskDsnPassword", () => {
  it("masks the password and keeps every other part addressable", () => {
    expect(maskDsnPassword("postgresql://app:s3cr3t@pg-router.databases.svc.cluster.local:5432/megafactory")).toBe(
      "postgresql://app:••••••••@pg-router.databases.svc.cluster.local:5432/megafactory",
    );
  });

  it("masks percent-encoded passwords whole", () => {
    expect(maskDsnPassword("postgresql://app:a%40b%2Fc@host:5432/db")).toBe("postgresql://app:••••••••@host:5432/db");
  });

  it("leaves a credential-less DSN alone", () => {
    expect(maskDsnPassword("postgresql://host:5432/db")).toBe("postgresql://host:5432/db");
  });
});
