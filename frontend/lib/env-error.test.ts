import assert from "node:assert/strict";
import test from "node:test";

import { describeEnvError, type EnvErrorCode } from "./env-error.ts";

/**
 * kkartov@yandex.ru hit RevealEnvVar failures and, having no legible message,
 * pressed Delete App seven times. Every code the backend actually emits on
 * Set/Bulk/Reveal/Delete (see envvars.go) and on the solution-install env
 * write (see solutions.go's env_failed) must resolve to a non-empty reason
 * and a non-empty next step here.
 */
const ALL_CODES: EnvErrorCode[] = [
  "key_required",
  "malformed_body",
  "value_too_large",
  "invalid_scope",
  "vars_required",
  "batch_too_large",
  "invalid_key",
  "duplicate_key",
  "reveal_flag_missing",
  "membership_check_failed",
  "env_check_failed",
  "load_failed",
  "decrypt_failed",
  "delete_failed",
  "save_failed",
  "env_failed",
  "not_a_member",
  "read_only_role",
  "project_not_found",
  "app_not_found",
  "env_not_in_project",
  "var_not_found",
  "not_found",
];

const ALIAS_CODES: Partial<Record<EnvErrorCode, EnvErrorCode>> = {
  not_found: "var_not_found",
};

test("describeEnvError covers every backend env error code with a reason and a next step", () => {
  for (const code of ALL_CODES) {
    const desc = describeEnvError(code);
    assert.ok(desc, `missing mapping for code ${code}`);
    assert.ok(desc!.reasonKey.length > 0, `empty reasonKey for ${code}`);
    assert.ok(desc!.nextStepKey.length > 0, `empty nextStepKey for ${code}`);
  }
});

test("describeEnvError returns distinct reason keys per code (no copy-paste collisions)", () => {
  const reasonKeys = ALL_CODES.filter((code) => !ALIAS_CODES[code]).map((code) => describeEnvError(code)!.reasonKey);
  assert.equal(new Set(reasonKeys).size, reasonKeys.length);
});

test("describeEnvError points the 'broken on our side' codes at the do-not-delete-the-app next step", () => {
  const ourSideCodes: EnvErrorCode[] = [
    "membership_check_failed",
    "env_check_failed",
    "load_failed",
    "decrypt_failed",
    "delete_failed",
    "save_failed",
    "env_failed",
  ];
  for (const code of ourSideCodes) {
    assert.equal(describeEnvError(code)!.nextStepKey, "apps.env.error.nextStep.ourSide");
  }
});

test("describeEnvError gives reveal_flag_missing its own next step saying the variable is intact", () => {
  assert.equal(describeEnvError("reveal_flag_missing")!.nextStepKey, "apps.env.error.nextStep.revealFlagMissing");
});

test("describeEnvError points each access/not-found code at its own actionable next step, none of them ourSide or badInput", () => {
  const expected: Record<string, string> = {
    not_a_member: "apps.env.error.nextStep.notAMember",
    read_only_role: "apps.env.error.nextStep.readOnlyRole",
    project_not_found: "apps.env.error.nextStep.projectNotFound",
    app_not_found: "apps.env.error.nextStep.appNotFound",
    env_not_in_project: "apps.env.error.nextStep.envNotInProject",
    var_not_found: "apps.env.error.nextStep.varNotFound",
  };
  for (const [code, nextStepKey] of Object.entries(expected)) {
    const desc = describeEnvError(code);
    assert.ok(desc, `missing mapping for code ${code}`);
    assert.equal(desc!.nextStepKey, nextStepKey, `wrong next step for ${code}`);
    assert.notEqual(desc!.nextStepKey, "apps.env.error.nextStep.ourSide", `${code} must not be told it's a server-side failure`);
    assert.notEqual(desc!.nextStepKey, "apps.env.error.nextStep.badInput", `${code} must not be told to fix a form value`);
  }
});

test("describeEnvError treats the DeleteEnvVar wire code not_found as the missing-variable case", () => {
  const alias = describeEnvError("not_found");
  const canonical = describeEnvError("var_not_found");
  assert.ok(alias, "DeleteEnvVar answers 404 with code not_found; leaving it unmapped drops the user back to raw prose");
  assert.deepEqual(alias, canonical);
});

test("describeEnvError returns null for an unknown code (caller falls back to err.message)", () => {
  assert.equal(describeEnvError("some_new_backend_code_nobody_mapped_yet"), null);
});

test("describeEnvError returns null for undefined/empty code (403 with no known code)", () => {
  assert.equal(describeEnvError(undefined), null);
  assert.equal(describeEnvError(null), null);
  assert.equal(describeEnvError(""), null);
});
