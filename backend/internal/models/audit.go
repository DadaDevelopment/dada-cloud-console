package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditEvent records a user or system action for compliance and traceability.
// This table is immutable — rows are never updated or deleted.
type AuditEvent struct {
	ID           uuid.UUID       `json:"id"                        db:"id"`
	ActorID      uuid.UUID       `json:"actor_id"                  db:"actor_id"`
	ProjectID    *uuid.UUID      `json:"project_id,omitempty"      db:"project_id"`
	OperationID  *uuid.UUID      `json:"operation_id,omitempty"    db:"operation_id"`
	Action       string          `json:"action"                    db:"action"`
	ResourceKind string          `json:"resource_kind,omitempty"   db:"resource_kind"`
	ResourceName string          `json:"resource_name,omitempty"   db:"resource_name"`
	Metadata     json.RawMessage `json:"metadata"                  db:"metadata"`
	CreatedAt    time.Time       `json:"created_at"                db:"created_at"`
}

// ResourceSnapshot caches Kubernetes / Argo CD resource status for fast UI reads.
//
// DemoExpiresAt is not a snapshot column: it is joined in from git_repos and is
// non-nil only for an app deployed from a platform starter template, carrying
// the moment the reaper deletes it unless the user claims it first. Ordinary
// apps omit the field entirely.
type ResourceSnapshot struct {
	ID            uuid.UUID       `json:"id"             db:"id"`
	ProjectID     uuid.UUID       `json:"project_id"     db:"project_id"`
	EnvironmentID *uuid.UUID      `json:"environment_id" db:"environment_id"`
	Kind          string          `json:"kind"           db:"kind"` // ServiceDatabase, App
	Name          string          `json:"name"           db:"name"`
	Phase         string          `json:"phase"          db:"phase"` // Ready, Pending, Failed
	SummaryJSON   json.RawMessage `json:"summary_json"   db:"summary_json"`
	LastSyncedAt  time.Time       `json:"last_synced_at" db:"last_synced_at"`
	Alerts        []AppAlert      `json:"alerts,omitempty" db:"-"`
	DemoExpiresAt *time.Time      `json:"demo_expires_at,omitempty" db:"-"`
}

// AppAlert is one unresolved-within-cooldown alert surfaced to the console for
// an app: either a health alert (crash/OOM/image-pull) or a volume-fill alert,
// read straight off the app_health_alerts / app_volume_alerts cooldown rows
// the watchers already write when they send an owner email. No live cluster
// or Prometheus read backs this — it is exactly the fact "we emailed about
// this within the last 24h", so ListApps stays within its latency budget.
// Cause/CauseLine are only ever populated for Type == "crash": the platform
// already fetches and classifies the crashed container's log tail
// (notify.ClassifyCrashLog / notify.ExtractCauseLine) for the alert email,
// so surfacing the same result here costs nothing extra and lets the console
// show WHY an app crashed, not just that it did. Both are best-effort and
// omitted (empty) whenever the log read failed or no known signature
// matched — the console must never show a guessed cause.
type AppAlert struct {
	Type       string    `json:"type"` // "crash" or "volume"
	Reason     string    `json:"reason,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Cause      string    `json:"cause,omitempty"`
	CauseLine  string    `json:"cause_line,omitempty"`
	CauseKind  string    `json:"cause_kind,omitempty"`
	Ratio      *float64  `json:"ratio,omitempty"`
	DetectedAt time.Time `json:"detected_at"`
}
