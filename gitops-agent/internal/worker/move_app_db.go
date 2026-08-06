package worker

import (
	"fmt"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

// serviceDatabaseManifest is the subset of a rendered ServiceDatabaseV2 that the
// move re-point reads back out of the source app's resources.values.yaml.
//
// These fields must survive a cross-project move byte-for-byte. The
// ServiceDatabaseV2 composite backs a logical database inside the shared
// postgresql-0 cluster via a Crossplane Database managed resource whose
// deletionPolicy is Orphan and which is namespace-independent. Renaming the CR
// (metadata.name) or changing the logical spec.database would make Crossplane
// provision a fresh empty database and strand the real data, so only the
// destination namespace and the dada.io labels are allowed to change on a move.
type serviceDatabaseManifest struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		AppRef   string `yaml:"appRef"`
		Database string `yaml:"database"`
		Shard    string `yaml:"shard"`
		Tier     string `yaml:"tier"`
		Backup   struct {
			Enabled   bool   `yaml:"enabled"`
			Frequency string `yaml:"frequency"`
			Retention string `yaml:"retention"`
		} `yaml:"backup"`
	} `yaml:"spec"`
}

// rerenderServiceDatabaseForMove re-renders one ServiceDatabaseV2 CR for its new
// home. spec.namespace and the dada.io/project|environment|operation labels move
// to the target; metadata.name, spec.appRef, the logical spec.database, the
// quota spec.tier, the spec.shard placement and the entire backup policy are
// carried verbatim from src. Dropping spec.shard would re-point the provider at
// the default instance, where the moved database does not exist.
//
// That verbatim carry is what makes a move a data-safe RE-POINT rather than a
// destroy-and-recreate: re-rendering the same-named CR with
// spec.namespace=dstNamespace only re-delivers the <appRef>-db-credentials
// secret into the target namespace. The Orphan-policy Database MR is never
// dropped or recreated, so the shared logical database — and its data — stay put
// while the app moves. Both namespaces resolve the same DB during cutover, so
// there is no split-brain window.
func rerenderServiceDatabaseForMove(src serviceDatabaseManifest, dstProjectSlug, dstEnvSlug, dstNamespace, operationID string) (string, error) {
	if src.Metadata.Name == "" {
		return "", fmt.Errorf("source ServiceDatabaseV2 has no metadata.name; refusing to re-render (a rename in shared PG is a data-loss)")
	}
	return renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:            src.Metadata.Name,
		Namespace:       dstNamespace,
		ProjectSlug:     dstProjectSlug,
		EnvSlug:         dstEnvSlug,
		AppRef:          src.Spec.AppRef,
		Database:        src.Spec.Database,
		Shard:           src.Spec.Shard,
		Tier:            src.Spec.Tier,
		BackupEnabled:   src.Spec.Backup.Enabled,
		BackupSchedule:  src.Spec.Backup.Frequency,
		BackupRetention: src.Spec.Backup.Retention,
		OperationID:     operationID,
	})
}

// repointResourcesValuesDB rewrites the ServiceDatabaseV2 entry of a parsed
// resources.values.yaml in place, re-rendering it for the destination
// project/env/namespace and Upserting it back into its slot. It replaces the
// Phase-1 strip (RemoveKind) that used to drop the DB before copying an app to
// another project: under Phase 3 the database moves WITH the app as an
// Orphan-safe re-point.
//
// The DB's identity (name, appRef, database, backup policy) is read from the
// existing manifest — the authoritative record of what is actually deployed — so
// the re-point can never silently change the logical database. Every other
// manifest (the app's PublicApi, env Secret, ...) is left untouched. It is a
// no-op when the app carries no ServiceDatabaseV2.
func repointResourcesValuesDB(rv *renderer.ResourcesValues, dstProjectSlug, dstEnvSlug, dstNamespace, operationID string) error {
	raw, ok, err := rv.ManifestOfKind("ServiceDatabaseV2")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	var src serviceDatabaseManifest
	if err := yaml.Unmarshal([]byte(raw), &src); err != nil {
		return fmt.Errorf("parsing source ServiceDatabaseV2 for re-point: %w", err)
	}
	dbYAML, err := rerenderServiceDatabaseForMove(src, dstProjectSlug, dstEnvSlug, dstNamespace, operationID)
	if err != nil {
		return err
	}
	return rv.Upsert(dbYAML)
}

// moveVolumeAllowed reports whether MOVE_VOLUME_ENABLED unlocks moving an app
// that carries a persistent volume. It is a pure predicate over the config flag
// so the gate is unit-testable and reads identically wherever it is consulted.
//
// The flag defaults false, so a stateful (volume-bearing) app keeps aborting the
// move exactly as before. Even when the flag is true the worker still refuses the
// move: in-agent volume copy (ADR-014 Phase 2) is not implemented, and landing an
// app on a fresh empty PVC would silently lose its data. The flag is forward
// plumbing for that execution follow-up, not a switch that skips the copy — the
// only difference true makes today is a distinct, honest abort message.
func moveVolumeAllowed(enabled bool) bool {
	return enabled
}
