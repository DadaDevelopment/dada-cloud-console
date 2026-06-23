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

func (r *StatusReconciler) reconcile(ctx context.Context) {
	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list environments")
		return
	}

	updated := 0
	for _, env := range envs {
		// List every Deployment, not just dada.io/app-labelled ones: some charts
		// (e.g. n8n) omit the label, so we fall back to the deployment name
		// (minus the "-deploy" suffix) as the app key. Labelled wins when present.
		deps, err := r.client.AppsV1().Deployments(env.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			// Namespace may not exist yet, or RBAC gap — log once per env and move on.
			log.Warn().Err(err).Str("namespace", env.Namespace).Msg("status-reconciler: list deployments")
			continue
		}

		apps := map[string]*liveApp{}
		for i := range deps.Items {
			d := &deps.Items[i]
			name := appKey(d)
			if name == "" {
				continue
			}
			la := apps[name]
			if la == nil {
				la = &liveApp{}
				apps[name] = la
			}
			la.desired += desiredReplicas(d)
			la.ready += d.Status.ReadyReplicas
			if la.image == "" {
				la.image = primaryImage(d)
			}
		}

		for name, la := range apps {
			phase := livePhase(la)
			patch, _ := json.Marshal(map[string]any{
				"status":      phase,
				"image":       la.image,
				"replicas":    la.desired,
				"ready":       la.ready,
				"live_source": "k8s",
				"live_at":     time.Now().UTC().Format(time.RFC3339),
			})
			n, err := db.UpdateAppLiveStatus(ctx, r.pool, env.EnvID, name, phase, patch)
			if err != nil {
				log.Error().Err(err).Str("app", name).Str("namespace", env.Namespace).Msg("status-reconciler: update snapshot")
				continue
			}
			updated += int(n)
		}
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
