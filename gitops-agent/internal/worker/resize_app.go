package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
)

// doResizeApp changes an app's CPU and memory envelope and changes nothing else.
//
// The deploy path cannot be reused for this. It regenerates values.yaml out of
// the database, and for an app whose manifests are hand-maintained the database
// holds almost none of what the file contains -- so the render drops the rest,
// and guardUnattendedClobber has to refuse the operation to keep it from
// deleting a live app's environment and volumes. That refusal is correct and it
// also meant the autoscaler could not resize any of those apps: every starving
// one it found on this cluster failed there.
//
// So a resize reads the file that is already in git, rewrites the six resource
// scalars inside it, and commits that file. Nothing is re-derived, so nothing
// can be lost, and the operation works the same for a console-owned app and a
// hand-maintained one.
//
// The snapshot is updated too, so a later full re-render starts from the size
// the app actually has rather than undoing the resize.
func (w *DBWatcher) doResizeApp(ctx context.Context, op db.Operation) error {
	var p struct {
		AppName   string                 `json:"app_name"`
		Resources *renderer.AppResources `json:"resources"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.AppName == "" {
		return errors.New("resize payload carries no app_name")
	}
	if p.Resources == nil || !p.Resources.Complete() {
		return errors.New("resize payload carries an incomplete resource envelope")
	}

	var runtime string
	if err := w.pool.QueryRow(ctx, `SELECT runtime FROM environments WHERE id = $1`, op.EnvironmentID).Scan(&runtime); err != nil {
		return fmt.Errorf("load env runtime: %w", err)
	}
	if runtime == "vm" {
		return errors.New("resize is a Kubernetes operation; this environment runs on a VM")
	}

	projectName, envName, _, err := w.projectEnv(ctx, op.ProjectID, op.EnvironmentID)
	if err != nil {
		return fmt.Errorf("project/env lookup: %w", err)
	}

	mgr, err := w.managerFor(ctx, op.ProjectID)
	if err != nil {
		return err
	}
	if err := mgr.EnsureCloned(); err != nil {
		return err
	}
	if _, err := mgr.Pull(); err != nil {
		return fmt.Errorf("pull before resize: %w", err)
	}

	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, p.AppName)
	existing, err := mgr.ReadFile(valuesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s has no %s to resize; deploy the app once from the console first", p.AppName, valuesPath)
		}
		return fmt.Errorf("read %s: %w", valuesPath, err)
	}

	patched, err := renderer.PatchValuesResources(existing, *p.Resources)
	if err != nil {
		return fmt.Errorf("patch %s: %w", valuesPath, err)
	}

	if patched == existing {
		log.Info().Str("app", p.AppName).Msg("resize is a no-op, values.yaml already carries this envelope")
		sha, err := mgr.LocalHEAD()
		if err != nil {
			return err
		}
		return db.MarkCommitted(ctx, w.pool, op.ID, sha, valuesPath)
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Resize app %s to %s/%s CPU and %s/%s memory\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.AppName,
		p.Resources.CPURequest, p.Resources.CPULimit,
		p.Resources.MemoryRequest, p.Resources.MemoryLimit,
		op.ID, projectName, envName,
	)
	files := []git.FileChange{{Path: valuesPath, Content: patched}}
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, files, commitMsg); err != nil {
		return err
	}

	return w.recordResizeInSnapshot(ctx, op, p.AppName, *p.Resources)
}

// recordResizeInSnapshot writes the new envelope into the app's snapshot so a
// later full re-render keeps the size instead of reverting to whatever the
// profile fallback would have picked.
//
// A missing snapshot is not an error: apps that only exist in git have none,
// and the resize itself has already landed in the file that governs them.
func (w *DBWatcher) recordResizeInSnapshot(ctx context.Context, op db.Operation, appName string, res renderer.AppResources) error {
	var summaryRaw []byte
	if err := w.pool.QueryRow(ctx, `
		SELECT summary_json FROM resource_snapshots
		WHERE project_id=$1 AND environment_id=$2 AND kind='App' AND name=$3
	`, op.ProjectID, op.EnvironmentID, appName).Scan(&summaryRaw); err != nil {
		log.Warn().Err(err).Str("app", appName).Msg("resize committed but no App snapshot to update")
		return nil
	}
	cur := map[string]any{}
	_ = json.Unmarshal(summaryRaw, &cur)

	encoded, err := json.Marshal(res)
	if err != nil {
		return err
	}
	var asMap map[string]any
	if err := json.Unmarshal(encoded, &asMap); err != nil {
		return err
	}
	cur["resources"] = asMap

	status, _ := cur["status"].(string)
	if status == "" {
		status = "Pending"
	}
	updatedJSON, _ := json.Marshal(cur)
	return db.UpsertSnapshot(ctx, w.pool,
		op.ProjectID, op.EnvironmentID,
		"App", appName, status, updatedJSON, time.Now(),
	)
}
