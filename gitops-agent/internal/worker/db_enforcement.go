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

// databaseEnforcementStates are the values ServiceDatabaseV2.spec.enforcement
// accepts. They mirror the XRD enum: a value outside this set is rejected by the
// API server at sync time, which would wedge the whole app's Application — so it
// is rejected here, where the operation can fail honestly instead.
var databaseEnforcementStates = map[string]bool{
	"none":      true,
	"read-only": true,
	"frozen":    true,
}

// doSetDatabaseEnforcement flips a managed database's storage-quota enforcement
// state in git. "read-only" makes the role refuse writes while leaving the data
// readable; "frozen" additionally takes its connection limit to 0; "none"
// releases both.
//
// The field is patched into the manifest that is already in git rather than
// re-rendered from the operation payload: the deployed CR is the authoritative
// record of the database's identity (name, appRef, logical database, tier,
// extensions, backup policy), and re-rendering from a payload that carries none
// of that would silently rewrite it. Enforcement is applied by a watcher, not a
// human, so a silent rewrite would have no reviewer.
func (w *DBWatcher) doSetDatabaseEnforcement(ctx context.Context, op db.Operation) error {
	var p struct {
		Name        string `json:"name"`
		AppRef      string `json:"app_ref"`
		Enforcement string `json:"enforcement"`
	}
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if p.Name == "" {
		return fmt.Errorf("set database enforcement: name is required")
	}
	if !databaseEnforcementStates[p.Enforcement] {
		return fmt.Errorf("set database enforcement: unknown state %q", p.Enforcement)
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
		return fmt.Errorf("set database enforcement: %w", err)
	}

	patched, changed, err := patchDatabaseEnforcement(raw, p.Name, p.Enforcement)
	if err != nil {
		return err
	}
	if !changed {
		return db.MarkNoop(ctx, w.pool, op.ID, fmt.Sprintf("database %s already carries enforcement %q", p.Name, p.Enforcement))
	}

	manifestFile, err := upsertManifestFile(mgr, valuesPath, patched)
	if err != nil {
		return err
	}
	commitMsg := fmt.Sprintf(
		"[DADA Console] Set ServiceDatabaseV2 %s enforcement=%s\n\nOperation: %s\nProject: %s\nEnvironment: %s\n",
		p.Name, p.Enforcement, op.ID, projectName, envName,
	)
	if err := w.commitFilesAndRecord(ctx, op, mgr, valuesPath, []git.FileChange{manifestFile}, commitMsg); err != nil {
		return err
	}

	patch, _ := json.Marshal(map[string]any{
		"enforcement":    p.Enforcement,
		"enforcement_at": time.Now().UTC().Format(time.RFC3339),
	})
	_, err = w.pool.Exec(ctx, `
		UPDATE resource_snapshots
		SET summary_json = COALESCE(summary_json, '{}'::jsonb) || $1::jsonb
		WHERE environment_id = $2 AND kind = 'ServiceDatabaseV2' AND name = $3
	`, patch, op.EnvironmentID, p.Name)
	return err
}

// patchDatabaseEnforcement sets spec.enforcement on one ServiceDatabaseV2
// manifest, leaving every other field byte-identical in meaning. It reports
// changed=false when the manifest already carries the wanted state, so a
// watcher that re-decides the same thing every tick produces no commits.
//
// The name guard is what keeps an operation for one database from patching a
// different one that happens to sit in the same values file.
func patchDatabaseEnforcement(manifestYAML, wantName, enforcement string) (string, bool, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(manifestYAML), &doc); err != nil {
		return "", false, fmt.Errorf("parsing ServiceDatabaseV2 for enforcement patch: %w", err)
	}
	meta, _ := doc["metadata"].(map[string]any)
	gotName, _ := meta["name"].(string)
	if gotName != wantName {
		return "", false, fmt.Errorf("enforcement patch targets %q but manifest is %q", wantName, gotName)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("ServiceDatabaseV2 %s has no spec", wantName)
	}
	cur, _ := spec["enforcement"].(string)
	if cur == "" {
		cur = "none"
	}
	if cur == enforcement {
		return "", false, nil
	}
	if enforcement == "none" {
		delete(spec, "enforcement")
	} else {
		spec["enforcement"] = enforcement
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}
