package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"gopkg.in/yaml.v3"
)

// databaseTiers are the values ServiceDatabaseV2.spec.tier accepts, mirroring
// serviceDatabase.tiers in the crossplane-platform-api chart. A value outside
// this set is rejected by the API server at sync time and would wedge the whole
// app's Application, so it is rejected here instead, where the operation can
// fail honestly.
var databaseTiers = map[string]bool{
	"unlimited": true,
	"internal":  true,
	"free":      true,
	"starter":   true,
	"business":  true,
}

// doSetDatabaseTier writes a managed database's quota tier into git. The tier
// decides the role's connection limit and per-role postgres parameters, and it
// is the limit the console's storage-quota watcher measures against.
//
// Like enforcement, the field is patched into the manifest already in git
// rather than re-rendered from the payload: the deployed CR is the
// authoritative record of the database's identity, and this operation is
// emitted by a reconciler with no human reviewer behind it.
func (w *DBWatcher) doSetDatabaseTier(ctx context.Context, op db.Operation) error {
	var p struct {
		Name   string `json:"name"`
		AppRef string `json:"app_ref"`
		Tier   string `json:"tier"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("set database tier: name is required")
	}
	if !databaseTiers[p.Tier] {
		return fmt.Errorf("set database tier: unknown tier %q", p.Tier)
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

	valuesPath, raw, err := locateServiceDatabase(mgr, projectName, envName, p.AppRef, p.Name)
	if err != nil {
		return fmt.Errorf("set database tier: %w", err)
	}

	patched, changed, err := patchDatabaseTier(raw, p.Name, p.Tier)
	if err != nil {
		return err
	}
	if !changed {
		return db.MarkNoop(ctx, w.pool, op.ID, fmt.Sprintf("database %s already carries tier %q", p.Name, p.Tier))
	}

	manifestFile, err := upsertManifestFile(mgr, valuesPath, patched)
	if err != nil {
		return err
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Set ServiceDatabaseV2 %s tier=%s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, p.Tier, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, []git.FileChange{manifestFile}, commitMsg); err != nil {
		return err
	}

	patch, _ := json.Marshal(map[string]any{
		"tier":    p.Tier,
		"tier_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, err = w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3
	`, patch, op.EnvironmentID, p.Name)
	return err
}

// patchDatabaseTier sets spec.tier on one ServiceDatabaseV2 manifest, leaving
// every other field byte-identical in meaning. changed=false when the manifest
// already carries the wanted tier, so a reconciler that re-decides the same
// thing every tick produces no commits.
//
// An absent spec.tier reads as "unlimited" because that is the XRD default;
// without that the first reconcile of every pre-tier database would look like a
// change from "" and rewrite manifests that already say the right thing.
func patchDatabaseTier(manifestYAML, wantName, tier string) (string, bool, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(manifestYAML), &doc); err != nil {
		return "", false, fmt.Errorf("parsing ServiceDatabaseV2 for tier patch: %w", err)
	}
	meta, _ := doc["metadata"].(map[string]any)
	gotName, _ := meta["name"].(string)
	if gotName != wantName {
		return "", false, fmt.Errorf("tier patch targets %q but manifest is %q", wantName, gotName)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("ServiceDatabaseV2 %s has no spec", wantName)
	}
	cur, _ := spec["tier"].(string)
	if cur == "" {
		cur = "unlimited"
	}
	if cur == tier {
		return "", false, nil
	}
	if tier == "unlimited" {
		delete(spec, "tier")
	} else {
		spec["tier"] = tier
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
