/**
 * Pure, framework-free validation for the create-database form. Mirrors the
 * browser-side constraints the modal already declares (`pattern` +
 * `required` attributes) plus the backend contract in
 * backend/internal/api/databases.go, so a rejected attempt is visible in
 * ux_events instead of dying silently in the DOM.
 */

export type DbFormField = "name" | "database" | "app_ref" | "backup_schedule" | "backup_retention";

export interface DbFormShape {
  name: string;
  database: string;
  app_ref: string;
  backup_enabled: boolean;
  backup_schedule: string;
  backup_retention: string;
}

export interface DbFormIssue {
  field: DbFormField;
  reason: string;
}

const NAME_RE = /^[a-z0-9-]+$/;
const PG_NAME_RE = /^[a-z][a-z0-9-]*$/;
const SCHEDULES = new Set(["hourly", "daily", "weekly"]);
const RETENTIONS = new Set(["7d", "14d", "30d"]);

/**
 * Returns every issue that would make the browser block submission or the
 * backend answer 400. Empty array = the form would pass.
 */
export function validateDbForm(form: DbFormShape): DbFormIssue[] {
  const issues: DbFormIssue[] = [];
  if (!form.name.trim()) {
    issues.push({ field: "name", reason: "required" });
  } else if (form.name.length > 63) {
    issues.push({ field: "name", reason: "too_long" });
  } else if (!NAME_RE.test(form.name)) {
    issues.push({ field: "name", reason: "invalid_chars" });
  }
  if (!form.database.trim()) {
    issues.push({ field: "database", reason: "required" });
  } else if (form.database.length > 63) {
    issues.push({ field: "database", reason: "too_long" });
  } else if (!PG_NAME_RE.test(form.database)) {
    issues.push({ field: "database", reason: "invalid_chars" });
  }
  if (form.backup_enabled && !SCHEDULES.has(form.backup_schedule)) {
    issues.push({ field: "backup_schedule", reason: "invalid_value" });
  }
  if (form.backup_enabled && !RETENTIONS.has(form.backup_retention)) {
    issues.push({ field: "backup_retention", reason: "invalid_value" });
  }
  return issues;
}

/**
 * Telemetry target for a rejected submit attempt. Kept here (not inline in
 * the page) so the event name has one owner and stays greppable.
 */
export function dbFormValidationTarget(issues: DbFormIssue[]): string {
  const first = issues[0];
  return first ? `create_db_modal:validation_error:${first.field}:${first.reason}` : "create_db_modal:validation_error";
}
