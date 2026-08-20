/**
 * Maps the machine `code` the backend puts on env-var errors (Set/Bulk/Reveal/
 * Delete on `/env-vars`, and the solution-install env write) to the pair of
 * i18n keys the UI shows: what broke, in plain language, and what the user
 * should do next.
 *
 * Why this exists: a user who saw a bare "failed" on RevealEnvVar had exactly
 * one legible button left — Delete App — and pressed it seven times in a row.
 * The variable's own row was fine the whole time; nothing about a decrypt or
 * membership-check failure is fixed by deleting the app. Every code below
 * must say so explicitly for the "broken on our side" class.
 *
 * Branch ONLY on `err.status` + `err.code`, never on `err.message` text — see
 * project rule against regexing error prose.
 */

export type EnvErrorCode =
  | "key_required"
  | "malformed_body"
  | "value_too_large"
  | "invalid_scope"
  | "vars_required"
  | "batch_too_large"
  | "invalid_key"
  | "duplicate_key"
  | "reveal_flag_missing"
  | "membership_check_failed"
  | "env_check_failed"
  | "load_failed"
  | "decrypt_failed"
  | "delete_failed"
  | "save_failed"
  | "env_failed";

export interface EnvErrorDescription {
  reasonKey: string;
  nextStepKey: string;
}

const OUR_SIDE_NEXT_STEP = "apps.env.error.nextStep.ourSide";
const BAD_INPUT_NEXT_STEP = "apps.env.error.nextStep.badInput";

const ENV_ERROR_DESCRIPTIONS: Record<EnvErrorCode, EnvErrorDescription> = {
  key_required: { reasonKey: "apps.env.error.reason.keyRequired", nextStepKey: BAD_INPUT_NEXT_STEP },
  malformed_body: { reasonKey: "apps.env.error.reason.malformedBody", nextStepKey: BAD_INPUT_NEXT_STEP },
  value_too_large: { reasonKey: "apps.env.error.reason.valueTooLarge", nextStepKey: BAD_INPUT_NEXT_STEP },
  invalid_scope: { reasonKey: "apps.env.error.reason.invalidScope", nextStepKey: BAD_INPUT_NEXT_STEP },
  vars_required: { reasonKey: "apps.env.error.reason.varsRequired", nextStepKey: BAD_INPUT_NEXT_STEP },
  batch_too_large: { reasonKey: "apps.env.error.reason.batchTooLarge", nextStepKey: BAD_INPUT_NEXT_STEP },
  invalid_key: { reasonKey: "apps.env.error.reason.invalidKey", nextStepKey: BAD_INPUT_NEXT_STEP },
  duplicate_key: { reasonKey: "apps.env.error.reason.duplicateKey", nextStepKey: BAD_INPUT_NEXT_STEP },
  reveal_flag_missing: { reasonKey: "apps.env.error.reason.revealFlagMissing", nextStepKey: "apps.env.error.nextStep.revealFlagMissing" },
  membership_check_failed: { reasonKey: "apps.env.error.reason.membershipCheckFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  env_check_failed: { reasonKey: "apps.env.error.reason.envCheckFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  load_failed: { reasonKey: "apps.env.error.reason.loadFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  decrypt_failed: { reasonKey: "apps.env.error.reason.decryptFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  delete_failed: { reasonKey: "apps.env.error.reason.deleteFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  save_failed: { reasonKey: "apps.env.error.reason.saveFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
  env_failed: { reasonKey: "apps.env.error.reason.envFailed", nextStepKey: OUR_SIDE_NEXT_STEP },
};

function isEnvErrorCode(code: string): code is EnvErrorCode {
  return Object.prototype.hasOwnProperty.call(ENV_ERROR_DESCRIPTIONS, code);
}

/**
 * Returns the reason/next-step key pair for a known env error code.
 * Returns null when the code is missing or unrecognized — callers fall back
 * to `err.message` (never to an empty or "undefined" string).
 */
export function describeEnvError(code: string | undefined | null): EnvErrorDescription | null {
  if (!code || !isEnvErrorCode(code)) return null;
  return ENV_ERROR_DESCRIPTIONS[code];
}
