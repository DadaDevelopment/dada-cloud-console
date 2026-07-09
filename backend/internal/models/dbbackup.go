package models

import (
	"time"

	"github.com/google/uuid"
)

// DBBackup catalogs one per-database logical backup produced by a Kanister
// ActionSet. Status transitions Pending -> Running -> Ready|Failed; retention
// moves expired Ready backups to Deleting -> Deleted.
type DBBackup struct {
	ID            uuid.UUID  `json:"id"`
	ProjectID     uuid.UUID  `json:"project_id"`
	EnvironmentID uuid.UUID  `json:"environment_id"`
	ResourceName  string     `json:"resource_name"`
	DatabaseName  string     `json:"database_name"`
	DumpPath      string     `json:"dump_path"`
	SizeBytes     *int64     `json:"size_bytes,omitempty"`
	Status        string     `json:"status"`
	Kind          string     `json:"kind"`
	ActionSet     *string    `json:"action_set,omitempty"`
	ErrorMessage  *string    `json:"error_message,omitempty"`
	CreatedBy     *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// DBBackup status values.
const (
	DBBackupStatusPending  = "Pending"
	DBBackupStatusRunning  = "Running"
	DBBackupStatusReady    = "Ready"
	DBBackupStatusFailed   = "Failed"
	DBBackupStatusDeleting = "Deleting"
	DBBackupStatusDeleted  = "Deleted"
)

// DBBackup kind values.
const (
	DBBackupKindManual     = "manual"
	DBBackupKindScheduled  = "scheduled"
	DBBackupKindPreRestore = "pre-restore"
)

// RestoreServiceDatabasePayload is the typed payload for RestoreServiceDatabase
// operations: restore the named database from a cataloged backup.
type RestoreServiceDatabasePayload struct {
	Name     string `json:"name"`
	Database string `json:"database"`
	BackupID string `json:"backup_id"`
}
