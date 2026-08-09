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
// The manifest is looked up by name, not by kind: standalone databases of one
// project share the carrier app service-databases-<project>, so the first
// ServiceDatabaseV2 in that file usually belongs to a different database.
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
	raw, ok, err := rv.ManifestOfKindNamed("ServiceDatabaseV2", p.Name)
	if err != nil {
		return err
	}
	if !ok {
		return w.setShardInHelmValues(ctx, op, mgr, projectName, envName, p.AppRef, p.Name, p.Shard)
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

// setShardInHelmValues is the path for apps whose ServiceDatabaseV2 is rendered
// by their own chart out of common.serviceDatabase in values.yaml, rather than
// standing as a manifest in resources.values.yaml.
//
// Both shapes are live: databases the console creates today land in
// resources.values.yaml, but everything provisioned through an app chart --
// reels, user, telemost-bot -- has only the values block, and for those the
// manifest lookup finds nothing. Failing there left the shard unrecorded for
// exactly the apps whose charts do accept it (n8n already passes it through),
// so a move silently kept pointing Kasten and the ProviderConfig at the shard
// the data left.
func (w *DBWatcher) setShardInHelmValues(ctx context.Context, op db.Operation, mgr *git.Manager, projectName, envName, appRef, name, shard string) error {
	if appRef == "" {
		return fmt.Errorf("set database shard: %s has no ServiceDatabaseV2 manifest and no appRef to find its values.yaml", name)
	}
	valuesPath := renderer.AppHelmValuesGitPath(projectName, envName, appRef)
	existing, err := mgr.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("set database shard: neither a ServiceDatabaseV2 manifest nor %s: %w", valuesPath, err)
	}

	patched, changed, err := patchHelmValuesShard(existing, shard)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	commitMsg := fmt.Sprintf(
		"[DADA Console] Set ServiceDatabaseV2 %s shard=%s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		name, shard, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, []git.FileChange{{Path: valuesPath, Content: patched}}, commitMsg); err != nil {
		return err
	}

	patch, _ := json.Marshal(map[string]any{"shard": shard})
	_, err = w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3
	`, patch, op.EnvironmentID, name)
	return err
}

// patchHelmValuesShard sets common.serviceDatabase.shard and reports
// changed=false when the file already names that shard.
//
// It refuses a values.yaml with no serviceDatabase block rather than creating
// one: a block conjured here would carry no name, no schema and no backup
// settings, and the chart would render a second database next to the real one.
func patchHelmValuesShard(valuesYAML, shard string) (string, bool, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(valuesYAML), &doc); err != nil {
		return "", false, fmt.Errorf("parsing values.yaml for shard patch: %w", err)
	}
	common, ok := doc["common"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("values.yaml has no common block to carry the database shard")
	}
	sd, ok := common["serviceDatabase"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("values.yaml has no common.serviceDatabase block to carry the shard")
	}
	if cur, _ := sd["shard"].(string); cur == shard {
		return "", false, nil
	}
	sd["shard"] = shard
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
