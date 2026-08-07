import assert from "node:assert/strict";
import test from "node:test";

import { patchCommon, readCommon } from "./values-common.ts";

const values = `common:
  image:
    name: test
    tag: latest
  servicePort: 8080
  replicas: 1
  useDotEnv: "false"
  resources:
    requests:
      cpu: 10m
      memory: 128Mi
    limits:
      cpu: 250m
      memory: 256Mi
`;

test("blank service port explicitly disables the public HTTP route", () => {
  const cfg = readCommon(values);
  cfg.servicePort = "";
  const patched = patchCommon(values, cfg);

  assert.match(patched, /servicePort: ~/);
  assert.match(patched, /service:\n    enabled: false/);
  assert.equal(readCommon(patched).servicePort, "");
});

test("configured service port explicitly enables the public HTTP route", () => {
  const cfg = readCommon(values);
  cfg.servicePort = "3000";
  const patched = patchCommon(values, cfg);

  assert.match(patched, /servicePort: 3000/);
  assert.match(patched, /service:\n    enabled: true/);
});
