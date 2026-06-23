package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// appLabel is the label every console-managed workload carries; its value is
// the App snapshot name (e.g. dada.io/app=profi). It's how a live Deployment is
// mapped back to the resource_snapshots row the console UI renders.
const appLabel = "dada.io/app"

// StatusReconciler periodically reads Deployment status from each k8s
// environment's namespace and mirrors phase + image + replicas onto the
// matching App snapshot. Without it, git-synced apps are stuck at phase
// "Unknown" with no live image/replica data. Read-only against the cluster.
type StatusReconciler struct {
	pool   *pgxpool.Pool
	cfg    *config.Config
	client kubernetes.Interface
}

func NewStatusReconciler(pool *pgxpool.Pool, cfg *config.Config, client kubernetes.Interface) *StatusReconciler {
	return &StatusReconciler{pool: pool, cfg: cfg, client: client}
}

func (r *StatusReconciler) Start(ctx context.Context) {
	log.Info().Dur("interval", r.cfg.PollIntervalStatus).Msg("status-reconciler started")
	r.reconcile(ctx)
	ticker := time.NewTicker(r.cfg.PollIntervalStatus)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
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
	for _, e := range envs {
		envByNs[e.Namespace] = e.EnvID
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
		app := appKey(d)
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
		n, err := db.UpdateAppLiveStatus(ctx, r.pool, k.env, k.app, phase, patch)
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

// appKey maps a Deployment to its App snapshot name: the dada.io/app label when
// present, else the deployment name with a trailing "-deploy" stripped (the
// chart convention, e.g. profi-deploy → profi). Unmatched keys simply find no
// snapshot row and no-op, so a wrong guess is harmless.
func appKey(d *appsv1.Deployment) string {
	if v := d.Labels[appLabel]; v != "" {
		return v
	}
	return strings.TrimSuffix(d.Name, "-deploy")
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
