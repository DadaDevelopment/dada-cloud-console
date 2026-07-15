package worker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

	managers map[string]*git.Manager
	gcBase   string
}

func NewStatusReconciler(pool *pgxpool.Pool, cfg *config.Config, clients *dadak8s.Clients) *StatusReconciler {
	return &StatusReconciler{
		pool:     pool,
		cfg:      cfg,
		client:   clients.Typed,
		clients:  clients,
		managers: map[string]*git.Manager{},
		gcBase:   filepath.Join(cfg.RepoLocalPath, "orphan-gc"),
	}
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
	live := r.reconcile(ctx)
	r.reconcileModels(ctx)
	r.reconcileDatabases(ctx)
	r.reconcilePublicApis(ctx)
	if r.cfg.OrphanGCEnabled {
		r.reconcileOrphans(ctx, live)
	}
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

// reconcileDatabases mirrors ServiceDatabaseV2 (Crossplane) readiness onto DB
// snapshots. The managed Postgres CRs are cluster-scoped, so each is matched to
// its env by unambiguous snapshot name — like reconcileModels. This is the ONLY
// live-status path for databases now that the cluster-wide discover() pass is
// gated off by default (GITOPS_CLUSTER_DISCOVERY_ENABLED); without it a managed
// DB is frozen at its create-time "Pending" forever. Existing rows only, so no
// isolation leak: a CR with no snapshot in this project is never created here.
func (r *StatusReconciler) reconcileDatabases(ctx context.Context) {
	dbEnvs, err := db.SnapshotEnvsByKind(ctx, r.pool, "ServiceDatabaseV2")
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list servicedatabase envs")
		return
	}
	if len(dbEnvs) == 0 {
		return
	}

	list, err := r.clients.Dynamic.Resource(pgvr("servicedatabasesv2")).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list servicedatabasesv2")
		return
	}

	updated := 0
	for i := range list.Items {
		cr := &list.Items[i]
		name := cr.GetName()
		ids := dbEnvs[name]
		if len(ids) != 1 {
			continue // no DB snapshot for this name, or ambiguous
		}
		phase := crPhase(cr)
		patch, _ := json.Marshal(map[string]any{
			"status":      phase,
			"live_source": "crossplane",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		})
		n, err := db.UpdateLiveStatus(ctx, r.pool, ids[0], "ServiceDatabaseV2", name, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("database", name).Msg("status-reconciler: update servicedatabase")
			continue
		}
		updated += int(n)
	}
	if updated > 0 {
		log.Debug().Int("updated", updated).Msg("status-reconciler: synced database statuses")
	}
}

// reconcilePublicApis mirrors PublicApi (Crossplane publicapi-beget-dns)
// readiness onto PublicApi snapshots. Like databases/models the CRs are
// cluster-scoped, so each is matched to its env by unambiguous snapshot name.
// Without this a public endpoint is frozen at the git-watcher's create-time
// "Unknown" forever even though its Ingress + DNS are live. Existing rows only,
// so no isolation leak: a CR with no snapshot in this project is never created.
//
// It also self-heals summary_json.app_name from the CR's ArgoCD instance label
// (app-env). The app Domains tab filters endpoints by app_name, so a snapshot
// synced before app_name stamping (git-watcher 4100de1) is a real, live domain
// invisible in the UI. Re-deriving it here fixes old rows and any future gap.
func (r *StatusReconciler) reconcilePublicApis(ctx context.Context) {
	apiEnvs, err := db.SnapshotEnvsByKind(ctx, r.pool, "PublicApi")
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list publicapi envs")
		return
	}
	if len(apiEnvs) == 0 {
		return
	}

	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list environments for publicapi")
		return
	}
	envNames := make(map[string]bool, len(envs))
	for _, e := range envs {
		envNames[e.Name] = true
	}

	list, err := r.clients.Dynamic.Resource(pgvr("publicapis")).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list publicapis")
		return
	}

	updated := 0
	for i := range list.Items {
		cr := &list.Items[i]
		name := cr.GetName()
		ids := apiEnvs[name]
		if len(ids) != 1 {
			continue
		}
		phase := crPhase(cr)
		fields := map[string]any{
			"status":      phase,
			"live_source": "crossplane",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		}
		if app := stripEnvSuffix(cr.GetLabels()["argocd.argoproj.io/instance"], envNames); app != "" {
			fields["app_name"] = app
		}
		patch, _ := json.Marshal(fields)
		n, err := db.UpdateLiveStatus(ctx, r.pool, ids[0], "PublicApi", name, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("publicapi", name).Msg("status-reconciler: update publicapi")
			continue
		}
		updated += int(n)
	}
	if updated > 0 {
		log.Debug().Int("updated", updated).Msg("status-reconciler: synced publicapi statuses")
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

// reconcile mirrors live Deployment state onto App snapshots and returns the set
// of snapshot keys (env + app) that had at least one live Deployment this tick.
// The orphan GC consumes that set as its "still alive" guard: a snapshot present
// here is never a prune candidate, even scaled to zero.
func (r *StatusReconciler) reconcile(ctx context.Context) map[snapKey]bool {
	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list environments")
		return nil
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
		return nil
	}

	// One cluster-wide list per workload kind: covers both env namespaces and
	// override namespaces (e.g. dada-agent in argocd-prod). Beyond Deployments we
	// also mirror StatefulSets and DaemonSets so a legitimate chart whose primary
	// workload is not a Deployment (fluent-bit DaemonSet, keycloak/mimir/jira
	// StatefulSet, any GitHub/Docker image) reports its real live phase instead of
	// being frozen at the git-watcher's "Unknown". Still strictly read-only.
	agg := map[snapKey]*liveApp{}
	acc := func(labels map[string]string, name, ns string, desired, ready int32, containers []corev1.Container) {
		app := appKeyFromMeta(labels, name, envNames)
		if app == "" {
			return
		}
		var envID uuid.UUID
		if id, ok := envByNs[ns]; ok {
			envID = id // workload sits in its env's namespace (normal case)
		} else if ids := appEnvs[app]; len(ids) == 1 {
			envID = ids[0] // namespace override, unambiguous app name
		} else {
			return // not an app namespace, and name absent/ambiguous → skip
		}
		k := snapKey{envID, app}
		la := agg[k]
		if la == nil {
			la = &liveApp{}
			agg[k] = la
		}
		la.desired += desired
		la.ready += ready
		if la.image == "" {
			la.image = imageFromContainers(containers)
		}
	}

	if deps, err := r.client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list deployments")
	} else {
		for i := range deps.Items {
			d := &deps.Items[i]
			acc(d.Labels, d.Name, d.Namespace, desiredReplicas(d), d.Status.ReadyReplicas, d.Spec.Template.Spec.Containers)
		}
	}

	if sts, err := r.client.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{}); err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list statefulsets")
	} else {
		for i := range sts.Items {
			s := &sts.Items[i]
			acc(s.Labels, s.Name, s.Namespace, replicasOrDefault(s.Spec.Replicas), s.Status.ReadyReplicas, s.Spec.Template.Spec.Containers)
		}
	}

	// DaemonSet desired count is node-driven (DesiredNumberScheduled), not a
	// spec.replicas; a DS matching zero nodes reads Stopped, which is honest.
	if dss, err := r.client.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{}); err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list daemonsets")
	} else {
		for i := range dss.Items {
			ds := &dss.Items[i]
			acc(ds.Labels, ds.Name, ds.Namespace, ds.Status.DesiredNumberScheduled, ds.Status.NumberReady, ds.Spec.Template.Spec.Containers)
		}
	}

	updated := 0
	for k, la := range agg {
		phase := livePhase(la)
		patchFields := map[string]any{
			"status":      phase,
			"image":       la.image,
			"replicas":    la.desired,
			"ready":       la.ready,
			"live_source": "k8s",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		}
		if hostname, err := db.PrimaryHostname(ctx, r.pool, k.env, k.app, r.cfg.DefaultDomainBase); err != nil {
			log.Warn().Err(err).Str("app", k.app).Msg("status-reconciler: primary hostname lookup")
		} else if hostname != "" {
			patchFields["url"] = "https://" + hostname
		}
		patch, _ := json.Marshal(patchFields)
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

	live := make(map[snapKey]bool, len(agg))
	for k := range agg {
		live[k] = true
	}
	return live
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
	return appKeyFromMeta(d.Labels, d.Name, envNames)
}

// appKeyFromMeta is the workload-kind-agnostic core of appKey: it takes only the
// labels and name so it works for Deployments, StatefulSets, and DaemonSets alike.
func appKeyFromMeta(labels map[string]string, name string, envNames map[string]bool) string {
	if v := labels[appLabel]; v != "" {
		return v
	}
	if inst := labels["argocd.argoproj.io/instance"]; inst != "" {
		if app := stripEnvSuffix(inst, envNames); app != "" {
			return app
		}
	}
	return strings.TrimSuffix(name, "-deploy")
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
	return replicasOrDefault(d.Spec.Replicas)
}

// replicasOrDefault mirrors the k8s controller default: a nil spec.replicas means
// 1. Shared by Deployment and StatefulSet (both use *int32 spec.replicas).
func replicasOrDefault(replicas *int32) int32 {
	if replicas != nil {
		return *replicas
	}
	return 1
}

func primaryImage(d *appsv1.Deployment) string {
	return imageFromContainers(d.Spec.Template.Spec.Containers)
}

// imageFromContainers returns the app image for a pod template, skipping the
// well-known logging sidecar so the card shows the app image, not the collector.
func imageFromContainers(cs []corev1.Container) string {
	for _, c := range cs {
		if c.Name == "fluent-container" {
			continue
		}
		return c.Image
	}
	if len(cs) > 0 {
		return cs[0].Image
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
