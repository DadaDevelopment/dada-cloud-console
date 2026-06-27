package models

import "time"

// CloudTaskArtifact is one file the agent produced for a cloud task. The bytes
// live on the agent; the cloud proxies them by file_id.
type CloudTaskArtifact struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Kind   string `json:"kind"`
}

// CloudTask is one fired DadaAgent task (one row in cloud_tasks).
type CloudTask struct {
	ID            string              `json:"id"`
	ProjectID     string              `json:"project_id"`
	EnvironmentID string              `json:"environment_id"`
	AppName       string              `json:"app_name"`
	GitRepoID     *string             `json:"git_repo_id,omitempty"`
	TaskType      string              `json:"task_type"`
	IntentID      *string             `json:"intent_id,omitempty"`
	WorkflowID    *string             `json:"workflow_id,omitempty"`
	Status        string              `json:"status"`
	PRURL         *string             `json:"pr_url,omitempty"`
	Artifacts     []CloudTaskArtifact `json:"artifacts"`
	Error         *string             `json:"error,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}
