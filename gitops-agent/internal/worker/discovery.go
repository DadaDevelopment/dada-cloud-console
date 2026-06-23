package worker

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/rs/zerolog/log"
)

// discoverySpec declares one custom CRD the console should mirror as snapshots.
//   - kind:   the console resource kind written to resource_snapshots.
//   - nsFrom: where to read the target namespace for project/env mapping —
//     "metadata" (namespaced CR), "spec.namespace" (cluster-scoped CR that
//     carries the target ns), or "" (no namespace hint; map by name/fallback).
type discoverySpec struct {
	gvr    schema.GroupVersionResource
	kind   string
	nsFrom string
}

func pgvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "platform.dada-tuda.ru", Version: "v1alpha1", Resource: resource}
}

// restorePointGVR is the K10 (Kasten) RestorePoint — a completed backup. The
// shared Postgres lives in the `databases` namespace, so every managed DB shares
// its backup schedule; the latest restore point is the last successful backup.
var restorePointGVR = schema.GroupVersionResource{Group: "apps.kio.kasten.io", Version: "v1alpha1", Resource: "restorepoints"}

const databasesNamespace = "databases"

// backupInfo is the shared-instance backup summary attached to every
// ServiceDatabaseV2 snapshot.
type backupInfo struct {
	lastAt string
	count  int
}

// latestBackup returns the most recent K10 RestorePoint in the databases
// namespace and the total count. Best-effort: zero value when K10 is absent.
func (r *StatusReconciler) latestBackup(ctx context.Context) backupInfo {
	list, err := r.clients.Dynamic.Resource(restorePointGVR).Namespace(databasesNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Debug().Err(err).Msg("discovery: list restorepoints (skipped)")
		return backupInfo{}
	}
	info := backupInfo{count: len(list.Items)}
	for i := range list.Items {
		if ts := list.Items[i].GetCreationTimestamp(); info.lastAt == "" || ts.Time.Format(time.RFC3339) > info.lastAt {
			info.lastAt = ts.Time.UTC().Format(time.RFC3339)
		}
	}
	return info
}

// discoveryKinds are the custom CRDs mirrored into the console. Apps are
// intentionally excluded — App snapshots are Deployment/git-backed (the apps CR
// has no instances here). AIModel readiness is additionally refined from the
// KServe InferenceService in reconcileModels.
var discoveryKinds = []discoverySpec{
	{pgvr("servicedatabasesv2"), "ServiceDatabaseV2", "spec.namespace"},
	{pgvr("publicapis"), "PublicApi", ""},
	{pgvr("aimodels"), "AIModel", "metadata"},
	{pgvr("yandexmetrikacounters"), "YandexMetrikaCounter", "spec.namespace"},
	{pgvr("s3buckets"), "S3Bucket", "spec.namespace"},
	{pgvr("serviceidentities"), "ServiceIdentity", "spec.namespace"},
	{pgvr("dnszones"), "DnsZone", ""},
	{pgvr("domainroutes"), "DomainRoute", ""},
}

// discover lists every custom CR and upserts a snapshot for it, so the console
// reflects cluster truth regardless of how the resource was authored (console
// UI, git resources.values.yaml, or a Helm chart template). Each CR is attributed
// to a project/env by: (1) an existing snapshot of the same (kind,name) — keeps
// the grouping git already assigned and avoids duplicates; (2) its target
// namespace → env; (3) the platform project as an infra fallback.
func (r *StatusReconciler) discover(ctx context.Context) {
	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("discovery: list environments")
		return
	}
	envByNs := make(map[string]db.K8sEnvironment, len(envs))
	for _, e := range envs {
		envByNs[e.Namespace] = e
	}
	envProject, err := db.EnvProjects(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("discovery: env projects")
		return
	}
	platform, hasPlatform, _ := db.PlatformTarget(ctx, r.pool)
	backup := r.latestBackup(ctx) // shared across all managed DBs

	upserted := 0
	for _, spec := range discoveryKinds {
		list, err := r.clients.Dynamic.Resource(spec.gvr).Namespace("").List(ctx, metav1.ListOptions{})
		if err != nil {
			// CRD may be absent on this cluster — debug, not error.
			log.Debug().Err(err).Str("resource", spec.gvr.Resource).Msg("discovery: list (skipped)")
			continue
		}
		// Existing snapshots of this kind keep their project/env grouping.
		byName, err := db.SnapshotEnvsByKind(ctx, r.pool, spec.kind)
		if err != nil {
			log.Error().Err(err).Str("kind", spec.kind).Msg("discovery: snapshot envs")
			continue
		}

		for i := range list.Items {
			o := &list.Items[i]
			name := o.GetName()
			target, ok := resolveTarget(spec, o, name, byName, envByNs, envProject, platform, hasPlatform)
			if !ok {
				continue // unmappable and no platform fallback — skip rather than orphan
			}
			phase := crPhase(o)
			fields := map[string]any{
				"status":      phase,
				"kind":        spec.kind,
				"name":        name,
				"spec":        o.Object["spec"],
				"conditions":  crConditions(o),
				"live_source": "crd",
				"live_at":     time.Now().UTC().Format(time.RFC3339),
			}
			if spec.kind == "ServiceDatabaseV2" {
				fields["backup_last_at"] = backup.lastAt
				fields["backup_count"] = backup.count
			}
			summary, _ := json.Marshal(fields)
			env := target.EnvID
			if err := db.UpsertSnapshot(ctx, r.pool, target.ProjectID, &env, spec.kind, name, phase, summary, time.Now()); err != nil {
				log.Error().Err(err).Str("kind", spec.kind).Str("name", name).Msg("discovery: upsert")
				continue
			}
			upserted++
		}
	}
	if upserted > 0 {
		log.Debug().Int("upserted", upserted).Msg("discovery: synced custom resources")
	}
}

// resolveTarget picks the project/env a CR belongs to (see discover() doc).
func resolveTarget(
	spec discoverySpec, o *unstructured.Unstructured, name string,
	byName map[string][]uuid.UUID,
	envByNs map[string]db.K8sEnvironment,
	envProject map[uuid.UUID]uuid.UUID,
	platform db.EnvProject, hasPlatform bool,
) (db.EnvProject, bool) {
	// 1. Existing snapshot of this (kind,name) — keep its grouping.
	if ids := byName[name]; len(ids) == 1 {
		if proj, ok := envProject[ids[0]]; ok {
			return db.EnvProject{ProjectID: proj, EnvID: ids[0]}, true
		}
	}
	// 2. Target namespace → env.
	if ns := targetNamespace(spec, o); ns != "" {
		if e, ok := envByNs[ns]; ok {
			return db.EnvProject{ProjectID: e.ProjectID, EnvID: e.EnvID}, true
		}
	}
	// 3. Infra fallback.
	if hasPlatform {
		return platform, true
	}
	return db.EnvProject{}, false
}

func targetNamespace(spec discoverySpec, o *unstructured.Unstructured) string {
	switch spec.nsFrom {
	case "metadata":
		return o.GetNamespace()
	case "spec.namespace":
		ns, _, _ := unstructured.NestedString(o.Object, "spec", "namespace")
		return ns
	default:
		return ""
	}
}

// crPhase maps a Crossplane-style status (Ready/Synced conditions) to a phase.
func crPhase(o *unstructured.Unstructured) string {
	conds := crConditions(o)
	switch conds["Ready"] {
	case "True":
		return "Ready"
	case "False":
		return "Pending" // provisioning or degraded; real not-ready state
	}
	if conds["Synced"] == "True" {
		return "Pending"
	}
	return "Unknown"
}

func crConditions(o *unstructured.Unstructured) map[string]string {
	out := map[string]string{}
	conds, found, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	if !found {
		return out
	}
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok {
			t, _ := m["type"].(string)
			s, _ := m["status"].(string)
			if t != "" {
				out[t] = s
			}
		}
	}
	return out
}
