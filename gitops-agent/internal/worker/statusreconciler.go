package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// inferenceServiceGVR is the KServe InferenceService resource — how AIModels run
// (serverless predictors, often in a dedicated ml-* namespace).
var inferenceServiceGVR = schema.GroupVersionResource{
	Group:    "serving.kserve.io",
	Version:  "v1beta1",
	Resource: "inferenceservices",
}

// appLabel is the label every console-managed workload carries; its value is
// the App snapshot name (e.g. dada.io/app=profi). It's how a live Deployment is
// mapped back to the resource_snapshots row the console UI renders.
const appLabel = "dada.io/app"

// StatusReconciler periodically reads Deployment status from each k8s
// environment's namespace and mirrors phase + image + replicas onto the
// matching App snapshot. Without it, git-synced apps are stuck at phase
// "Unknown" with no live image/replica data. Read-only against the cluster.
type StatusReconciler struct {
	pool    *pgxpool.Pool
	cfg     *config.Config
	client  kubernetes.Interface
	clients *dadak8s.Clients
}

func NewStatusReconciler(pool *pgxpool.Pool, cfg *config.Config, clients *dadak8s.Clients) *StatusReconciler {
	return &StatusReconciler{pool: pool, cfg: cfg, client: clients.Typed, clients: clients}
}

func (r *StatusReconciler) Start(ctx context.Context) {
	log.Info().Dur("interval", r.cfg.PollIntervalStatus).Msg("status-reconciler started")
	r.tick(ctx)
	ticker := time.NewTicker(r.cfg.PollIntervalStatus)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *StatusReconciler) tick(ctx context.Context) {
	if r.cfg.ClusterDiscoveryEnabled {
		r.discover(ctx)
	}
	r.reconcile(ctx)
	r.reconcileModels(ctx)
}

// reconcileModels mirrors KServe InferenceService readiness onto AIModel
// snapshots. Predictors usually live in a dedicated namespace (ml-prod), not the
// env namespace, so each is matched to its env by unambiguous snapshot name.
func (r *StatusReconciler) reconcileModels(ctx context.Context) {
	modelEnvs, err := db.SnapshotEnvsByKind(ctx, r.pool, "AIModel")
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list aimodel envs")
		return
	}
	if len(modelEnvs) == 0 {
		return
	}

	list, err := r.clients.Dynamic.Resource(inferenceServiceGVR).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list inferenceservices")
		return
	}

	updated := 0
	for i := range list.Items {
		isvc := &list.Items[i]
		name := isvc.GetName()
		ids := modelEnvs[name]
		if len(ids) != 1 {
			continue // no AIModel snapshot for this name, or ambiguous
		}
		phase := isvcPhase(isvc)
		patch, _ := json.Marshal(map[string]any{
			"status":      phase,
			"url":         isvcURL(isvc),
			"live_source": "kserve",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		})
		n, err := db.UpdateLiveStatus(ctx, r.pool, ids[0], "AIModel", name, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("model", name).Msg("status-reconciler: update aimodel")
			continue
		}
		updated += int(n)
	}
	if updated > 0 {
		log.Debug().Int("updated", updated).Msg("status-reconciler: synced model statuses")
	}
}

// isvcPhase reads the InferenceService Ready condition. Serverless models sit at
// zero replicas yet report Ready=True when their route is provisioned.
func isvcPhase(o *unstructured.Unstructured) string {
	conds, found, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	if !found {
		return "Pending"
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Ready" {
			switch m["status"] {
			case "True":
				return "Ready"
			case "False":
				return "Failed"
			default:
				return "Pending"
			}
		}
	}
	return "Pending"
}

func isvcURL(o *unstructured.Unstructured) string {
	url, _, _ := unstructured.NestedString(o.Object, "status", "url")
	return url
}

// liveApp aggregates the workload state for one app within a namespace. An app
// may map to more than one Deployment (rare); replicas are summed and the first
// non-empty image wins.
type liveApp struct {
	desired int32
	ready   int32
	image   string
}

// snapKey identifies one App snapshot to update: its environment + app name.
type snapKey struct {
	env uuid.UUID
	app string
}

func (r *StatusReconciler) reconcile(ctx context.Context) {
	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list environments")
		return
	}
	envByNs := make(map[string]uuid.UUID, len(envs))
	envNames := make(map[string]bool, len(envs))
	for _, e := range envs {
		envByNs[e.Namespace] = e.EnvID
		envNames[e.Name] = true
	}

	// appEnvs resolves namespace-override apps (App spec.namespace ≠ env
	// namespace): a Deployment found in a non-env namespace is attributed to its
	// env only when its name maps to exactly one App snapshot.
	appEnvs, err := db.AppSnapshotEnvs(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list app snapshot envs")
		return
	}

	// One cluster-wide list: covers both env namespaces and override namespaces
	// (e.g. dada-agent in argocd-prod). Unlabelled deployments fall back to name.
	deps, err := r.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list deployments")
		return
	}

	agg := map[snapKey]*liveApp{}
	for i := range deps.Items {
		d := &deps.Items[i]
		app := appKey(d, envNames)
		if app == "" {
			continue
		}
		var envID uuid.UUID
		if id, ok := envByNs[d.Namespace]; ok {
			envID = id // deployment sits in its env's namespace (normal case)
		} else if ids := appEnvs[app]; len(ids) == 1 {
			envID = ids[0] // namespace override, unambiguous app name
		} else {
			continue // not an app namespace, and name absent/ambiguous → skip
		}
		k := snapKey{envID, app}
		la := agg[k]
		if la == nil {
			la = &liveApp{}
			agg[k] = la
		}
		la.desired += desiredReplicas(d)
		la.ready += d.Status.ReadyReplicas
		if la.image == "" {
			la.image = primaryImage(d)
		}
	}

	updated := 0
	for k, la := range agg {
		phase := livePhase(la)
		patch, _ := json.Marshal(map[string]any{
			"status":      phase,
			"image":       la.image,
			"replicas":    la.desired,
			"ready":       la.ready,
			"live_source": "k8s",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		})
		n, err := db.UpdateLiveStatus(ctx, r.pool, k.env, "App", k.app, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("app", k.app).Msg("status-reconciler: update snapshot")
			continue
		}
		updated += int(n)
	}
	if updated > 0 {
		log.Debug().Int("updated", updated).Msg("status-reconciler: synced app statuses")
	}
}

// appKey maps a Deployment to its App snapshot name, in priority order:
//  1. dada.io/app label (explicit).
//  2. argocd.argoproj.io/instance label minus its "-<env>" suffix — this groups
//     every child workload of an ArgoCD/Helm app (e.g. jira-software StatefulSet
//     carries instance=jira-prod → app "jira"; cloud-console-prod → "cloud-console").
//  3. deployment name minus a trailing "-deploy" (the chart convention).
//
// Unmatched keys simply find no snapshot row and no-op, so a wrong guess is harmless.
func appKey(d *appsv1.Deployment, envNames map[string]bool) string {
	if v := d.Labels[appLabel]; v != "" {
		return v
	}
	if inst := d.Labels["argocd.argoproj.io/instance"]; inst != "" {
		if app := stripEnvSuffix(inst, envNames); app != "" {
			return app
		}
	}
	return strings.TrimSuffix(d.Name, "-deploy")
}

// stripEnvSuffix turns "cloud-console-prod" → "cloud-console" when the trailing
// token is a known environment name; otherwise returns the input unchanged.
func stripEnvSuffix(instance string, envNames map[string]bool) string {
	if i := strings.LastIndex(instance, "-"); i > 0 {
		if envNames[instance[i+1:]] {
			return instance[:i]
		}
	}
	return instance
}

func desiredReplicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas != nil {
		return *d.Spec.Replicas
	}
	return 1 // k8s default when unset
}

func primaryImage(d *appsv1.Deployment) string {
	for _, c := range d.Spec.Template.Spec.Containers {
		// Skip well-known logging sidecar so the card shows the app image.
		if c.Name == "fluent-container" {
			continue
		}
		return c.Image
	}
	if len(d.Spec.Template.Spec.Containers) > 0 {
		return d.Spec.Template.Spec.Containers[0].Image
	}
	return ""
}

// livePhase maps replica readiness to a phase string the PhaseBadge understands
// ("Ready" → green). Conservative: a partially-ready or scaled-to-zero workload
// is "Pending"/"Stopped", never silently "Ready".
func livePhase(la *liveApp) string {
	switch {
	case la.desired == 0:
		return "Stopped"
	case la.ready >= la.desired:
		return "Ready"
	default:
		return "Pending"
	}
}
