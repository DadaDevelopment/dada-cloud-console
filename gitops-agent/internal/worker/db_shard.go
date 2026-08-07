package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

// doSetDatabaseShard writes the shard a database actually lives on into the CR
// in git, after a move has already put the data there.
//
// Until this exists the only record of a completed move is the db_moves
// override the router reads, and that override is a cutover primitive, not a
// placement store: the CR keeps naming the shard the database left, so the
// composition would point Kasten backups and the admin ProviderConfig at the
// wrong instance, and anyone reading the manifest is told a lie.
//
// Like the enforcement patch, this edits the manifest already in git rather
// than re-rendering from the payload: the deployed CR is the authoritative
// record of the database identity, and a payload that carried none of it would
// silently rewrite the rest.
func (w *DBWatcher) doSetDatabaseShard(ctx context.Context, op db.Operation) error {
	var p struct {
		Name   string `json:"name"`
		AppRef string `json:"app_ref"`
		Shard  string `json:"shard"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("set database shard: name is required")
	}
	if p.Shard == "" {
		return fmt.Errorf("set database shard: shard is required")
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

	valuesPath := renderer.ServiceDatabaseResourcesValuesGitPath(projectName, envName, p.AppRef)
	rv, err := loadResourcesValues(mgr, valuesPath)
	if err != nil {
		return err
	}
	raw, ok, err := rv.ManifestOfKind("ServiceDatabaseV2")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("set database shard: no ServiceDatabaseV2 in %s", valuesPath)
	}

	patched, changed, err := patchDatabaseShard(raw, p.Name, p.Shard)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	manifestFile, err := upsertManifestFile(mgr, valuesPath, patched)
	if err != nil {
		return err
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Set ServiceDatabaseV2 %s shard=%s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, p.Shard, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, []git.FileChange{manifestFile}, commitMsg); err != nil {
		return err
	}

	patch, _ := json.Marshal(map[string]any{"shard": p.Shard})
	_, err = w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3
	`, patch, op.EnvironmentID, p.Name)
	return err
}

// patchDatabaseShard sets spec.shard on one ServiceDatabaseV2 manifest and
// reports changed=false when it already names that shard, so re-running a
// finished move produces no commit.
//
// The name guard keeps an operation for one database from patching another that
// happens to share the values file.
func patchDatabaseShard(manifestYAML, wantName, shard string) (string, bool, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(manifestYAML), &doc); err != nil {
		return "", false, fmt.Errorf("parsing ServiceDatabaseV2 for shard patch: %w", err)
	}
	meta, _ := doc["metadata"].(map[string]any)
	gotName, _ := meta["name"].(string)
	if gotName != wantName {
		return "", false, fmt.Errorf("shard patch targets %q but manifest is %q", wantName, gotName)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("ServiceDatabaseV2 %s has no spec", wantName)
	}
	if cur, _ := spec["shard"].(string); cur == shard {
		return "", false, nil
	}
	spec["shard"] = shard
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
