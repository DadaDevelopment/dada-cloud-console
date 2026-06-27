package api

import (
	"context"
	"encoding/json"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type cloudTaskInsert struct {
	ProjectID     uuid.UUID
	EnvironmentID uuid.UUID
	AppName       string
	GitRepoID     *uuid.UUID
	TaskType      string
	IntentID      string
	WorkflowID    string
	ActorID       uuid.UUID
}

const cloudTaskCols = `id, project_id, environment_id, app_name, git_repo_id, task_type,
	intent_id, workflow_id, status, pr_url, artifacts, error, created_at, updated_at`

func scanCloudTask(row pgx.Row) (models.CloudTask, error) {
	var t models.CloudTask
	var artifacts []byte
	if err := row.Scan(&t.ID, &t.ProjectID, &t.EnvironmentID, &t.AppName, &t.GitRepoID,
		&t.TaskType, &t.IntentID, &t.WorkflowID, &t.Status, &t.PRURL, &artifacts,
		&t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if len(artifacts) > 0 {
		_ = json.Unmarshal(artifacts, &t.Artifacts)
	}
	return t, nil
}

func (h *Handler) insertCloudTask(ctx context.Context, in cloudTaskInsert) (models.CloudTask, error) {
	row := h.pool.QueryRow(ctx,
		`INSERT INTO cloud_tasks (project_id, environment_id, app_name, git_repo_id, task_type, intent_id, workflow_id, actor_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+cloudTaskCols,
		in.ProjectID, in.EnvironmentID, in.AppName, in.GitRepoID, in.TaskType, in.IntentID, in.WorkflowID, in.ActorID)
	return scanCloudTask(row)
}

func (h *Handler) getCloudTask(ctx context.Context, id uuid.UUID) (models.CloudTask, error) {
	return scanCloudTask(h.pool.QueryRow(ctx, `SELECT `+cloudTaskCols+` FROM cloud_tasks WHERE id=$1`, id))
}

func (h *Handler) listCloudTasks(ctx context.Context, projectID uuid.UUID, appName string) ([]models.CloudTask, error) {
	rows, err := h.pool.Query(ctx, `SELECT `+cloudTaskCols+` FROM cloud_tasks WHERE project_id=$1 AND app_name=$2 ORDER BY created_at DESC`, projectID, appName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.CloudTask{}
	for rows.Next() {
		t, err := scanCloudTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// updateCloudTaskByIntent applies a webhook/agent status update. Each field is
// only overwritten when a non-empty value is supplied (idempotent COALESCE).
func (h *Handler) updateCloudTaskByIntent(ctx context.Context, intentID, status, prURL string, artifacts []byte, errMsg string) error {
	_, err := h.pool.Exec(ctx,
		`UPDATE cloud_tasks SET
		   status = COALESCE(NULLIF($2,''), status),
		   pr_url = COALESCE(NULLIF($3,''), pr_url),
		   artifacts = CASE WHEN $4::jsonb IS NULL THEN artifacts ELSE $4::jsonb END,
		   error = COALESCE(NULLIF($5,''), error),
		   updated_at = NOW()
		 WHERE intent_id = $1`,
		intentID, status, prURL, artifacts, errMsg)
	return err
}

// setCloudTaskWorkflow records the agent workflow id once submission returns it.
func (h *Handler) setCloudTaskWorkflow(ctx context.Context, intentID, workflowID string) error {
	_, err := h.pool.Exec(ctx,
		`UPDATE cloud_tasks SET workflow_id=$2, updated_at=NOW() WHERE intent_id=$1`,
		intentID, workflowID)
	return err
}

// resolveGitRepo finds the connected GitHub repo for an app and returns its
// full name, the numeric GitHub installation id (for token minting), and the
// git_repos row id. Errors (incl. pgx.ErrNoRows) mean "no connected repo".
func (h *Handler) resolveGitRepo(ctx context.Context, projectID, envID uuid.UUID, appName string) (repoFullName string, installationID int64, gitRepoID uuid.UUID, err error) {
	err = h.pool.QueryRow(ctx,
		`SELECT r.id, r.repo_full_name, i.installation_id
		   FROM git_repos r
		   JOIN git_app_installations i ON i.id = r.installation_id
		  WHERE r.project_id=$1 AND r.environment_id=$2 AND r.app_name=$3`,
		projectID, envID, appName).Scan(&gitRepoID, &repoFullName, &installationID)
	return repoFullName, installationID, gitRepoID, err
}
