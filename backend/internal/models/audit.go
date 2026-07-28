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
type ResourceSnapshot struct {
	ID            uuid.UUID       `json:"id"             db:"id"`
	ProjectID     uuid.UUID       `json:"project_id"     db:"project_id"`
	EnvironmentID *uuid.UUID      `json:"environment_id" db:"environment_id"`
	Kind          string          `json:"kind"           db:"kind"` // ServiceDatabase, App
	Name          string          `json:"name"           db:"name"`
	Phase         string          `json:"phase"          db:"phase"` // Ready, Pending, Failed
	SummaryJSON   json.RawMessage `json:"summary_json"   db:"summary_json"`
	LastSyncedAt  time.Time       `json:"last_synced_at" db:"last_synced_at"`
}
