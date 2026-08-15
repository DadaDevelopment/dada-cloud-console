package worker

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	dadak8s "github.com/dada-tuda/console/gitops-agent/internal/k8s"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
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

// Container-state reasons mirrored from backend/internal/api/app_health_watcher.go
// (reasonOOMKilled etc, detectPodAlert) so the two live-health detectors in this
// codebase agree on vocabulary and precedence instead of one saying "Ready" while
// the other is emailing the owner about the same pod. Not extracted to a shared
// package this cycle — see the reconcile() doc comment.
const (
	reasonOOMKilled        = "OOMKilled"
	reasonCrashLoopBackOff = "CrashLoopBackOff"
	reasonImagePullBackOff = "ImagePullBackOff"
	reasonErrImagePull     = "ErrImagePull"
	reasonError            = "Error"
)

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

	dnsVerdicts  map[string]dnsVerdict
	addrVerdicts map[string]addrVerdict
	deployments  chan deploymentObservation

	ambiguous map[string]bool
}

// projectLabel and environmentLabel are stamped on every CR the renderer writes
// (internal/renderer). On a cluster-scoped CR they are the only signal that says
// which project and environment own it.
const (
	projectLabel     = "dada.io/project"
	environmentLabel = "dada.io/environment"
)

// envOwner is the project+environment slug pair an environment id renders as,
// i.e. the labels a CR belonging to that environment carries.
type envOwner struct {
	project     string
	environment string
}

// deploymentObservation is a just-committed application rollout that needs
// event-driven status tracking. A git commit says only that Argo may begin the
// rollout; a Ready pod without a crash state is the recovery proof.
type deploymentObservation struct {
	environmentID uuid.UUID
	appName       string
	operationID   uuid.UUID
}

const deploymentObservationTimeout = 15 * time.Minute

// dnsVerdict caches one endpoint's resolve result, see dnsRecordLive.
type dnsVerdict struct {
	live bool
	at   time.Time
}

// addrVerdict caches one endpoint's resolved addresses, see publicApiRouteMissing.
type addrVerdict struct {
	addrs []string
	at    time.Time
}

func NewStatusReconciler(pool *pgxpool.Pool, cfg *config.Config, clients *dadak8s.Clients) *StatusReconciler {
	return &StatusReconciler{
		pool:         pool,
		cfg:          cfg,
		client:       clients.Typed,
		clients:      clients,
		managers:     map[string]*git.Manager{},
		dnsVerdicts:  map[string]dnsVerdict{},
		addrVerdicts: map[string]addrVerdict{},
		ambiguous:    map[string]bool{},
		deployments:  make(chan deploymentObservation, 64),
		gcBase:       filepath.Join(cfg.RepoLocalPath, "orphan-gc"),
	}
}

// ObserveDeployment begins watching a specific k8s app after its redeploy
// manifest is committed. The buffered, coalesced handoff keeps the DB worker
// independent from watch latency and never blocks operation processing.
func (r *StatusReconciler) ObserveDeployment(ctx context.Context, op db.Operation) {
	if op.EnvironmentID == nil || op.ResourceName == "" {
		return
	}
	target := deploymentObservation{
		environmentID: *op.EnvironmentID,
		appName:       op.ResourceName,
		operationID:   op.ID,
	}
	select {
	case r.deployments <- target:
	case <-ctx.Done():
	default:
		log.Warn().Str("app", target.appName).Str("operation", target.operationID.String()).Msg("status-reconciler: deploy observation queue full")
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
		case target := <-r.deployments:
			go r.observeDeployment(ctx, target)
		}
	}
}

// observeDeployment turns a DeployImageVersion commit into a Kubernetes watch
// scoped to the app's pods. Unlike the normal status ticker it wakes precisely
// when the rollout changes a pod, so a recovered app does not keep a stale
// CrashLoop alert until the next broad reconciliation pass.
func (r *StatusReconciler) observeDeployment(parent context.Context, target deploymentObservation) {
	ctx, cancel := context.WithTimeout(parent, deploymentObservationTimeout)
	defer cancel()

	var namespace string
	if err := r.pool.QueryRow(ctx, `SELECT namespace FROM environments WHERE id = $1 AND runtime = 'k8s'`, target.environmentID).Scan(&namespace); err != nil {
		log.Warn().Err(err).Str("app", target.appName).Str("operation", target.operationID.String()).Msg("status-reconciler: load deploy observation namespace")
		return
	}
	if namespace == "" {
		return
	}

	selector := labels.Set{appLabel: target.appName}.AsSelector().String()
	for {
		if r.reconcileObservedDeployment(ctx, target, namespace) {
			return
		}

		pods, err := r.client.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			log.Warn().Err(err).Str("app", target.appName).Str("operation", target.operationID.String()).Msg("status-reconciler: watch redeploy pods")
			return
		}

		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				pods.Stop()
				return
			case event, ok := <-pods.ResultChan():
				if !ok || event.Type == watch.Error {
					closed = true
					break
				}
				if r.reconcileObservedDeployment(ctx, target, namespace) {
					pods.Stop()
					return
				}
			}
		}
		pods.Stop()
	}
}

// reconcileObservedDeployment reuses the authoritative app reconciliation and
// stops the scoped watch only after that reconciliation has recorded Ready.
// A commit, a new pod, or a merely running container never qualifies.
func (r *StatusReconciler) reconcileObservedDeployment(ctx context.Context, target deploymentObservation, namespace string) bool {
	r.reconcile(ctx)
	var phase string
	err := r.pool.QueryRow(ctx, `
		SELECT phase FROM resource_snapshots
		WHERE environment_id = $1 AND kind = 'App' AND name = $2
	`, target.environmentID, target.appName).Scan(&phase)
	if err != nil || phase != "Ready" {
		return false
	}
	if err := db.ResolveAppHealthAlert(ctx, r.pool, namespace, target.appName); err != nil {
		log.Warn().Err(err).Str("app", target.appName).Msg("status-reconciler: resolve recovered crash alert")
		return false
	}
	log.Info().Str("app", target.appName).Str("namespace", namespace).Str("operation", target.operationID.String()).Msg("status-reconciler: redeploy recovered app")
	return true
}

func (r *StatusReconciler) tick(ctx context.Context) {
	if r.cfg.ClusterDiscoveryEnabled {
		r.discover(ctx)
	}
	live := r.reconcile(ctx)
	owners := r.snapshotEnvOwners(ctx)
	r.reconcileModels(ctx, owners)
	r.reconcileDatabases(ctx, owners)
	r.reconcilePublicApis(ctx, owners)
	r.reconcileS3Buckets(ctx, owners)
	if r.cfg.OrphanGCEnabled {
		r.reconcileOrphans(ctx, live)
	}
}

// snapshotEnvOwners maps every k8s environment id to the project+environment
// slugs a CR of that environment carries in its labels. It is read once per tick
// and handed to each cluster-scoped pass so an ambiguous snapshot name can be
// matched back to the single environment that really owns the live resource.
func (r *StatusReconciler) snapshotEnvOwners(ctx context.Context) map[uuid.UUID]envOwner {
	envs, err := db.ListK8sEnvironments(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list environments for snapshot ownership")
		return nil
	}
	projects, err := db.ListProjects(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list projects for snapshot ownership")
		return nil
	}
	projectNames := make(map[uuid.UUID]string, len(projects))
	for _, p := range projects {
		projectNames[p.ID] = p.Name
	}
	owners := make(map[uuid.UUID]envOwner, len(envs))
	for _, e := range envs {
		owners[e.EnvID] = envOwner{project: projectNames[e.ProjectID], environment: e.Name}
	}
	return owners
}

// resolveSnapshotEnv picks which environment's snapshot a cluster-scoped CR
// (AIModel, ServiceDatabaseV2, PublicApi, S3Bucket) belongs to.
//
// Such a CR has a globally unique name, but resource_snapshots does not: two
// projects can carry a row under the same name — a stale copy left behind by a
// dead project, or a mis-attributed adoption import. Every pass used to skip an
// ambiguous name with a bare `continue`, and because the ambiguity is a property
// of the data rather than of the tick, that skip was both permanent and silent:
// each row froze at its create-time phase forever while the CR itself was
// healthy, and the admin panel rendered those frozen rows as live breakage.
// Sixteen PublicApi names sat wedged that way for up to four weeks.
//
// So the tie is broken on the labels the renderer stamps on everything it
// writes, and an ambiguity that survives that is said out loud once rather than
// returning to silence — a reconciler that cannot decide is itself the finding.
func (r *StatusReconciler) resolveSnapshotEnv(kind, name string, ids []uuid.UUID, crLabels map[string]string, owners map[uuid.UUID]envOwner) (uuid.UUID, bool) {
	return r.resolveSnapshotEnvHinted(kind, name, ids, crLabels, owners, uuid.Nil)
}

// resolveSnapshotEnvHinted is resolveSnapshotEnv plus a caller-supplied
// environment hint, consulted only after the label tie-break has failed.
//
// The labels are the strongest signal but only the renderer stamps them, so
// every CR written by the infra repo carries none: on 2026-08-11 nine PublicApi
// names (n8n, svod, jenkins, jira, mlflow, nexus, portainer, rabbitmq-dada-prod,
// reels-tracker) plus AIModel/ServiceDatabaseV2/S3Bucket twins were frozen this
// way for over a week, and the admin breakage panel rendered the two of them
// that were stuck at Pending as live outages while both apps served 200.
//
// The hint is the environment of the cluster object the CR actually points at
// (for PublicApi, the namespace of its upstream Service). It is accepted only
// when it is one of the claiming rows, so a hint can pick the right claimant but
// can never invent an environment or leak status into a project that holds no
// snapshot for the name.
func (r *StatusReconciler) resolveSnapshotEnvHinted(kind, name string, ids []uuid.UUID, crLabels map[string]string, owners map[uuid.UUID]envOwner, hint uuid.UUID) (uuid.UUID, bool) {
	switch len(ids) {
	case 0:
		// No snapshot in any project: a CR this console does not track. That is
		// the isolation guarantee holding, not a failure — never warn.
		return uuid.Nil, false
	case 1:
		r.resolvedAmbiguity(kind, name)
		return ids[0], true
	}

	project, environment := crLabels[projectLabel], crLabels[environmentLabel]
	if project != "" && environment != "" {
		var matched []uuid.UUID
		for _, id := range ids {
			if o, ok := owners[id]; ok && o.project == project && o.environment == environment {
				matched = append(matched, id)
			}
		}
		if len(matched) == 1 {
			r.resolvedAmbiguity(kind, name)
			return matched[0], true
		}
	}

	if hint != uuid.Nil {
		for _, id := range ids {
			if id == hint {
				r.resolvedAmbiguity(kind, name)
				return hint, true
			}
		}
	}

	r.warnAmbiguity(kind, name, ids, owners)
	return uuid.Nil, false
}

// warnAmbiguity reports a snapshot name the reconciler cannot attribute. It logs
// once per name per process: the condition repeats every 30s tick until the data
// is corrected, and a line per tick would bury it.
func (r *StatusReconciler) warnAmbiguity(kind, name string, ids []uuid.UUID, owners map[uuid.UUID]envOwner) {
	key := kind + "/" + name
	if r.ambiguous[key] {
		return
	}
	r.ambiguous[key] = true

	claims := make([]string, 0, len(ids))
	for _, id := range ids {
		if o, ok := owners[id]; ok && o.project != "" {
			claims = append(claims, o.project+"/"+o.environment)
			continue
		}
		claims = append(claims, id.String())
	}
	sort.Strings(claims)
	log.Warn().Str("kind", kind).Str("resource", name).Strs("claimed_by", claims).
		Msg("status-reconciler: snapshot name claimed by several environments, live status frozen until one is removed")
}

// resolvedAmbiguity clears a previously reported name so that a later relapse is
// reported again instead of being swallowed by the once-per-process guard.
func (r *StatusReconciler) resolvedAmbiguity(kind, name string) {
	key := kind + "/" + name
	if r.ambiguous[key] {
		delete(r.ambiguous, key)
		log.Info().Str("kind", kind).Str("resource", name).
			Msg("status-reconciler: snapshot name no longer ambiguous, live status syncing again")
	}
}

// reconcileModels mirrors KServe InferenceService readiness onto AIModel
// snapshots. Predictors usually live in a dedicated namespace (ml-prod), not the
// env namespace, so each is matched to its env by unambiguous snapshot name.
func (r *StatusReconciler) reconcileModels(ctx context.Context, owners map[uuid.UUID]envOwner) {
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
		envID, ok := r.resolveSnapshotEnv("AIModel", name, modelEnvs[name], isvc.GetLabels(), owners)
		if !ok {
			continue
		}
		phase := isvcPhase(isvc)
		patch, _ := json.Marshal(map[string]any{
			"status":      phase,
			"url":         isvcURL(isvc),
			"live_source": "kserve",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		})
		n, err := db.UpdateLiveStatus(ctx, r.pool, envID, "AIModel", name, phase, patch)
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
// crDatabaseTier reads the quota tier the composition actually applied to a
// ServiceDatabaseV2. It is read from the LIVE CR, not from git or the billing
// plan, so the console shows the limits a tenant really runs under — including
// the "unlimited" default carried by every database created before tiers
// existed. defaultDatabaseTier mirrors the XRD default.
const defaultDatabaseTier = "unlimited"

// crDatabaseShard reads the Postgres instance a ServiceDatabaseV2 actually
// lives on. Like the tier it comes from the LIVE CR, so the console reports
// where the data really is rather than where the registry meant to put it —
// the two diverge for every database created before shards existed and during
// a move. defaultDatabaseShard mirrors the XRD default (the shared instance).
const defaultDatabaseShard = "shard-1"

func crDatabaseShard(cr *unstructured.Unstructured) string {
	shard, found, err := unstructured.NestedString(cr.Object, "spec", "shard")
	if err != nil || !found || shard == "" {
		return defaultDatabaseShard
	}
	return shard
}

func crDatabaseTier(cr *unstructured.Unstructured) string {
	tier, found, err := unstructured.NestedString(cr.Object, "spec", "tier")
	if err != nil || !found || tier == "" {
		return defaultDatabaseTier
	}
	return tier
}

func (r *StatusReconciler) reconcileDatabases(ctx context.Context, owners map[uuid.UUID]envOwner) {
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
		r.syncRouterEndpoint(ctx, cr)
		name := cr.GetName()
		envID, ok := r.resolveSnapshotEnv("ServiceDatabaseV2", name, dbEnvs[name], cr.GetLabels(), owners)
		if !ok {
			continue
		}
		phase := crPhase(cr)
		fields := map[string]any{
			"status":      phase,
			"live_source": "crossplane",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		}
		fields["tier"] = crDatabaseTier(cr)
		fields["shard"] = crDatabaseShard(cr)
		patch, _ := json.Marshal(fields)
		n, err := db.UpdateLiveStatus(ctx, r.pool, envID, "ServiceDatabaseV2", name, phase, patch)
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
func (r *StatusReconciler) reconcilePublicApis(ctx context.Context, owners map[uuid.UUID]envOwner) {
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
	envByNamespace := make(map[string]uuid.UUID, len(envs))
	for _, e := range envs {
		envNames[e.Name] = true
		envByNamespace[e.Namespace] = e.EnvID
	}

	list, err := r.clients.Dynamic.Resource(pgvr("publicapis")).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list publicapis")
		return
	}

	serviceEnvs := r.upstreamServiceEnvs(ctx, apiEnvs, envByNamespace)
	routes := r.loadIngressRoutes(ctx)

	updated := 0
	for i := range list.Items {
		cr := &list.Items[i]
		name := cr.GetName()
		envID, ok := r.resolveSnapshotEnvHinted("PublicApi", name, apiEnvs[name], cr.GetLabels(), owners,
			serviceEnvs[upstreamServiceName(cr)])
		if !ok {
			continue
		}
		phase := crPhase(cr)
		verdict := ""
		if phase == "Pending" && r.dnsRecordLive(ctx, cr) {
			phase = "Ready"
			verdict = "dns"
		}
		routeMissing := phase == "Ready" && r.publicApiRouteMissing(ctx, cr, routes)
		if routeMissing {
			phase = "Pending"
			verdict = publicApiVerdictRouteMissing
		}
		fields := map[string]any{
			"status":      phase,
			"live_source": "crossplane",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
		}
		if verdict != "" {
			fields["live_verdict"] = verdict
		}
		switch {
		case routeMissing:
			fields["reason"] = publicApiRouteMissingReason
		case phase == "Pending":
			if msg, _, ok := crProvisionError(cr); ok {
				fields["reason"] = msg
			}
		default:
			fields["reason"] = ""
		}
		if inst := cr.GetLabels()["argocd.argoproj.io/instance"]; inst != "" {
			if app := stripEnvSuffix(inst, envNames); app != "" && app != inst {
				fields["app_name"] = app
			}
		}
		patch, _ := json.Marshal(fields)
		n, err := db.UpdateLiveStatus(ctx, r.pool, envID, "PublicApi", name, phase, patch)
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

// upstreamServiceName is the Service a PublicApi routes to, "" when the CR does
// not declare one.
func upstreamServiceName(cr *unstructured.Unstructured) string {
	name, found, err := unstructured.NestedString(cr.Object, "spec", "upstream", "serviceName")
	if err != nil || !found {
		return ""
	}
	return name
}

// upstreamServiceEnvs maps a Service name to the environment that owns it, for
// use as a tie-break hint when several projects claim the same PublicApi name.
// A Service lives in exactly one namespace and a namespace belongs to exactly
// one environment, so a name that resolves to a single environment identifies
// the real owner of the endpoint; a name found in several environments (or in
// none the console knows) is left out and the ambiguity is reported as before.
//
// The cluster-wide Service list is skipped entirely when no name is contested,
// which is the normal state — the hint costs one LIST only while a stale twin
// row exists.
func (r *StatusReconciler) upstreamServiceEnvs(ctx context.Context, apiEnvs map[string][]uuid.UUID, envByNamespace map[string]uuid.UUID) map[string]uuid.UUID {
	contested := false
	for _, ids := range apiEnvs {
		if len(ids) > 1 {
			contested = true
			break
		}
	}
	if !contested {
		return nil
	}

	svcs, err := r.client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list services for publicapi tie-break")
		return nil
	}

	seen := make(map[string]uuid.UUID, len(svcs.Items))
	for i := range svcs.Items {
		envID, ok := envByNamespace[svcs.Items[i].Namespace]
		if !ok {
			continue
		}
		name := svcs.Items[i].Name
		if prev, dup := seen[name]; dup && prev != envID {
			seen[name] = uuid.Nil
			continue
		}
		seen[name] = envID
	}
	for name, envID := range seen {
		if envID == uuid.Nil {
			delete(seen, name)
		}
	}
	return seen
}

// publicApiVerdictRouteMissing marks a snapshot demoted by the route check, so
// live_verdict says which of the two live signals (DNS record, HTTP route)
// decided the phase instead of only ever recording the promoting one.
const publicApiVerdictRouteMissing = "route_missing"

// publicApiRouteMissingReason is the text the app Domains tab prints under a
// demoted endpoint. It names the missing object, because the fix is always to
// restore the Ingress -- the DNS half of the endpoint is fine by construction
// (the check only fires for records already pointing at our own edge).
const publicApiRouteMissingReason = "DNS указывает на наш ingress, но Ingress-правила для этого хоста нет — запрос упадёт в 404"

// ingressRoutes is the cluster's live HTTP routing table: every host any
// Ingress claims, plus the edge addresses those Ingresses are published on.
//
// known is false when the list call failed. Every consumer treats that as
// "leave the row alone" rather than "the route is gone", so an API-server blip
// never flips a healthy endpoint to Pending.
type ingressRoutes struct {
	hosts     map[string]bool
	wildcards map[string]bool
	edgeAddrs map[string]bool
	known     bool
}

// routed reports whether some Ingress claims host, counting the single-label
// wildcard form Kubernetes defines (*.example.com matches foo.example.com but
// never bar.foo.example.com nor the bare apex).
func (ir ingressRoutes) routed(host string) bool {
	if ir.hosts[host] {
		return true
	}
	label, rest, found := strings.Cut(host, ".")
	return found && label != "" && ir.wildcards[rest]
}

// atOurEdge reports whether any of addrs is an address our own ingress
// controller answers on. This is what separates "the endpoint is broken" from
// "this record was never ours to route": ns1/ns2, vpn and ipa are PublicApi
// rows whose A record points at foreign infrastructure and whose upstream is
// not even HTTP (port 53, 2096, 636). Demanding an Ingress for those would
// paint six permanently-correct records red.
func (ir ingressRoutes) atOurEdge(addrs []string) bool {
	for _, a := range addrs {
		if ir.edgeAddrs[a] {
			return true
		}
	}
	return false
}

// loadIngressRoutes builds the routing table once per tick. The list is
// cluster-wide because a hostname is not always routed from its own app's
// namespace -- PR previews under *.pv.dada-tuda.ru are served by one shared
// wildcard Ingress in argocd-prod.
//
// The edge addresses come from the Ingresses' own status rather than config:
// CLUSTER_LB_IP and GITOPS_DEFAULT_DOMAIN_DNS_TARGET already disagree in this
// repo (93.189.231.60 vs 155.212.223.198), and a stale constant here would
// silently switch the whole check off.
func (r *StatusReconciler) loadIngressRoutes(ctx context.Context) ingressRoutes {
	out := ingressRoutes{
		hosts:     map[string]bool{},
		wildcards: map[string]bool{},
		edgeAddrs: map[string]bool{},
	}
	list, err := r.client.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list ingresses for publicapi route check")
		return out
	}
	for i := range list.Items {
		ing := &list.Items[i]
		for _, rule := range ing.Spec.Rules {
			if rule.Host == "" {
				continue
			}
			if suffix, ok := strings.CutPrefix(rule.Host, "*."); ok {
				out.wildcards[suffix] = true
				continue
			}
			out.hosts[rule.Host] = true
		}
		for _, lb := range ing.Status.LoadBalancer.Ingress {
			if lb.IP != "" {
				out.edgeAddrs[lb.IP] = true
			}
		}
	}
	out.known = len(out.edgeAddrs) > 0
	return out
}

// publicApiRouteMissing reports that an endpoint the composite calls Ready
// cannot actually serve: its record points at our own ingress controller and no
// Ingress there claims the host, so every visitor gets the default backend's
// 404.
//
// This is the endpoint-side twin of RevalidateActiveHostnameRoutes in the
// backend, which already does exactly this for domain_hostnames rows. PublicApi
// snapshots never got the same treatment, so "Ready" on the app Domains tab has
// only ever meant "the Beget DNS Request succeeded" -- development.dada-tuda.ru
// carried a green badge for 16 hours while answering 404, and jira. and
// payments.dada-tuda.ru are still green today with no Ingress and no TLS at all.
//
// Deliberately conservative: it never fires on an unreadable routing table, on
// gateway-routed endpoints (their traffic enters through the shared gateway
// Ingress, not one of their own), on records that do not resolve, or on records
// answering from somewhere other than our edge.
func (r *StatusReconciler) publicApiRouteMissing(ctx context.Context, cr *unstructured.Unstructured, routes ingressRoutes) bool {
	if !routes.known {
		return false
	}
	if gw, found, _ := unstructured.NestedBool(cr.Object, "spec", "gatewayRoute"); found && gw {
		return false
	}
	fqdn, _, _ := unstructured.NestedString(cr.Object, "spec", "dns", "fqdn")
	if fqdn == "" || routes.routed(fqdn) {
		return false
	}
	return routes.atOurEdge(r.resolveAddrs(ctx, fqdn))
}

// resolveAddrs resolves fqdn behind the same TTL the DNS verdict uses, so the
// route check adds at most one lookup per endpoint per window no matter how
// often the reconciler ticks.
func (r *StatusReconciler) resolveAddrs(ctx context.Context, fqdn string) []string {
	if v, ok := r.addrVerdicts[fqdn]; ok && time.Since(v.at) < dnsVerdictTTL {
		return v.addrs
	}
	lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(lookupCtx, fqdn)
	if err != nil {
		addrs = nil
	}
	if r.addrVerdicts == nil {
		r.addrVerdicts = map[string]addrVerdict{}
	}
	r.addrVerdicts[fqdn] = addrVerdict{addrs: addrs, at: time.Now()}
	return addrs
}

// dnsVerdictTTL bounds how long a PublicApi DNS verdict is reused. The
// reconciler ticks every 30s and most endpoints sit Pending indefinitely while
// their Crossplane Request is wedged, so without a cache every tick would fire
// one lookup per endpoint forever.
const dnsVerdictTTL = 5 * time.Minute

// dnsLookupTimeout bounds a single endpoint resolve.
const dnsLookupTimeout = 3 * time.Second

// dnsRecordLive reports whether the DNS record a PublicApi exists to create is
// actually in public DNS: the record resolves, and — when the spec names a
// target — resolves to that target.
//
// It exists because "Pending" from the composite is not evidence the endpoint
// is unfinished. A provider-http Request that completed its Beget call (HTTP
// 200, {"status":"success"}) but crashed before recording the result keeps the
// crossplane.io/external-create-pending annotation, never goes Ready, and pins
// its whole composite at Ready=False forever — 52 of 53 endpoints platform-wide
// as of 2026-08-04, every one of them serving traffic. Reporting that as
// "Pending" tells the user their live domain is still being created. The record
// itself is the contract this resource owns, so resolving it is the verdict.
//
// The check only ever promotes Pending to Ready: an endpoint whose record is
// missing, or points somewhere else, keeps the composite's own verdict.
func (r *StatusReconciler) dnsRecordLive(ctx context.Context, cr *unstructured.Unstructured) bool {
	if enabled, found, _ := unstructured.NestedBool(cr.Object, "spec", "dns", "enabled"); found && !enabled {
		return false
	}
	fqdn, _, _ := unstructured.NestedString(cr.Object, "spec", "dns", "fqdn")
	if fqdn == "" {
		return false
	}
	if v, ok := r.dnsVerdicts[fqdn]; ok && time.Since(v.at) < dnsVerdictTTL {
		return v.live
	}
	target, _, _ := unstructured.NestedString(cr.Object, "spec", "dns", "target")

	lookupCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(lookupCtx, fqdn)

	live := err == nil && len(addrs) > 0
	if live && target != "" {
		live = false
		for _, a := range addrs {
			if a == target {
				live = true
				break
			}
		}
	}
	if r.dnsVerdicts == nil {
		r.dnsVerdicts = map[string]dnsVerdict{}
	}
	r.dnsVerdicts[fqdn] = dnsVerdict{live: live, at: time.Now()}
	return live
}

// reconcileS3Buckets mirrors S3Bucket readiness — and, when the provider
// refuses to build the bucket, its own rejection text — onto EXISTING bucket
// snapshots. It deliberately never creates rows: project/env stay whatever git
// assigned, so a bucket cannot leak into a project it was not committed under
// (the same reason discover() is off by default). Without this pass an S3Bucket
// snapshot is written once at git-sync time and then never moves, which is why
// buckets sat at "Pending" for days after the cluster had them Ready, and why a
// provider rejection had no path to the console at all.
func (r *StatusReconciler) reconcileS3Buckets(ctx context.Context, owners map[uuid.UUID]envOwner) {
	bucketEnvs, err := db.SnapshotEnvsByKind(ctx, r.pool, "S3Bucket")
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list s3bucket envs")
		return
	}
	if len(bucketEnvs) == 0 {
		return
	}

	list, err := r.clients.Dynamic.Resource(pgvr("s3buckets")).Namespace("").List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list s3buckets")
		return
	}

	updated := 0
	for i := range list.Items {
		cr := &list.Items[i]
		name := cr.GetName()
		envID, ok := r.resolveSnapshotEnv("S3Bucket", name, bucketEnvs[name], cr.GetLabels(), owners)
		if !ok {
			continue
		}
		phase := crPhase(cr)
		msg, reason, _ := crProvisionError(cr)
		fields := map[string]any{
			"status":                 phase,
			"conditions":             crConditions(cr),
			"live_source":            "crossplane",
			"live_at":                time.Now().UTC().Format(time.RFC3339),
			"provision_error":        msg,
			"provision_error_reason": reason,
		}
		patch, _ := json.Marshal(fields)
		n, err := db.UpdateLiveStatus(ctx, r.pool, envID, "S3Bucket", name, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("s3bucket", name).Msg("status-reconciler: update s3bucket")
			continue
		}
		updated += int(n)
	}
	if updated > 0 {
		log.Debug().Int("updated", updated).Msg("status-reconciler: synced s3bucket statuses")
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
//
// crashLooping/reason/restarts/lastExitCode come from a separate cluster-wide
// Pod list: readyReplicas alone lies about health when a workload has no
// readinessProbe — kubelet marks a just-started container ready right up until
// it crashes again, so desired==ready reads "Ready" for a container stuck in
// CrashLoopBackOff.
//
// namespaces/images are the observability join keys the console cannot derive
// on its own: an adopted ArgoCD app (ADR-013) is filed under one environment
// while its pods run in a different namespace, and it may run several images at
// once. Log search (kube namespace) and container metrics (namespace+image
// cAdvisor labels) both go blind without them.
//
// cpuRequest/cpuLimit/memRequest/memLimit sum the primary container's envelope
// over every desired pod, so the console can state the app's real footprint
// instead of echoing a resource profile it never had.
type liveApp struct {
	desired int32
	ready   int32
	image   string

	crashLooping bool
	reason       string
	restarts     int32
	lastExitCode *int32

	namespaces map[string]bool
	images     map[string]bool

	cpuRequest resource.Quantity
	cpuLimit   resource.Quantity
	memRequest resource.Quantity
	memLimit   resource.Quantity
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
	// appEnvsAll is the orphan-including fallback for the case appEnvs cannot
	// resolve at all: an adopted/infra app whose only remaining App snapshot
	// got marked Orphaned. Without this, that name has zero live claimants
	// forever, its workload can never be attributed, and the orphan GC purges
	// the snapshot for real on the next sweep even though the pod is up.
	appEnvsAll, err := db.AppSnapshotEnvsIncludingOrphaned(ctx, r.pool)
	if err != nil {
		log.Error().Err(err).Msg("status-reconciler: list app snapshot envs (including orphaned)")
		return nil
	}

	// One cluster-wide list per workload kind: covers both env namespaces and
	// override namespaces (e.g. dada-agent in argocd-prod). Beyond Deployments we
	// also mirror StatefulSets and DaemonSets so a legitimate chart whose primary
	// workload is not a Deployment (fluent-bit DaemonSet, keycloak/mimir/jira
	// StatefulSet, any GitHub/Docker image) reports its real live phase instead of
	// being frozen at the git-watcher's "Unknown". Still strictly read-only.
	agg := map[snapKey]*liveApp{}

	// resolveEnv is the namespace-to-environment lookup shared by every workload
	// kind (Deployment/StatefulSet/DaemonSet/Pod), in three steps:
	//  1. the workload's own namespace (normal case).
	//  2. an unambiguous namespace-override app name among live (non-Orphaned)
	//     snapshots — a live twin always outranks an Orphaned one by this step,
	//     which is what keeps a re-homed app from reviving its old row.
	//  3. only if step 2 found no live claimant at all, fall back to the
	//     orphan-including map: the one remaining case is an adopted/infra app
	//     whose sole snapshot got marked Orphaned, and without this step its
	//     live workload can never be attributed again and the row gets purged
	//     for real out from under a running pod.
	resolveEnv := func(app, ns string) (uuid.UUID, bool) {
		if id, ok := envByNs[ns]; ok {
			return id, true
		}
		if ids := appEnvs[app]; len(ids) == 1 {
			return ids[0], true
		}
		if ids := appEnvsAll[app]; len(ids) == 1 {
			return ids[0], true
		}
		return uuid.UUID{}, false // not an app namespace, and name absent/ambiguous → skip
	}

	acc := func(labels map[string]string, name, ns string, desired, ready int32, containers []corev1.Container) {
		app := appKeyFromMeta(labels, name, envNames)
		if app == "" {
			return
		}
		envID, ok := resolveEnv(app, ns)
		if !ok {
			return
		}
		k := snapKey{envID, app}
		la := agg[k]
		if la == nil {
			la = &liveApp{namespaces: map[string]bool{}, images: map[string]bool{}}
			agg[k] = la
		}
		la.desired += desired
		la.ready += ready
		img := imageFromContainers(containers)
		if la.image == "" {
			la.image = img
		}
		if ns != "" {
			la.namespaces[ns] = true
		}
		if img != "" {
			la.images[img] = true
		}
		if c := primaryContainer(containers); c != nil && desired > 0 {
			addScaled(&la.cpuRequest, c.Resources.Requests[corev1.ResourceCPU], desired)
			addScaled(&la.cpuLimit, c.Resources.Limits[corev1.ResourceCPU], desired)
			addScaled(&la.memRequest, c.Resources.Requests[corev1.ResourceMemory], desired)
			addScaled(&la.memLimit, c.Resources.Limits[corev1.ResourceMemory], desired)
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

	if pods, err := r.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err != nil {
		log.Warn().Err(err).Msg("status-reconciler: list pods")
	} else {
		for i := range pods.Items {
			p := &pods.Items[i]
			if !isLivePod(p) {
				continue
			}
			app := appKeyFromMeta(p.Labels, p.Name, envNames)
			if app == "" {
				continue
			}
			envID, ok := resolveEnv(app, p.Namespace)
			if !ok {
				continue
			}
			k := snapKey{envID, app}
			if la := agg[k]; la != nil {
				applyPodCrashState(la, p)
			}
		}
	}

	phases := make(map[snapKey]string, len(agg))
	patches := make(map[snapKey]map[string]any, len(agg))
	prober := newLivenessProber(r.cfg.LivenessProbeURL)
	var checkTimes map[snapKey]time.Time
	if prober != nil {
		checkTimes = r.loadLivenessCheckTimes(ctx)
	}
	var candidates []livenessCandidate

	for k, la := range agg {
		phase := livePhase(la)
		patchFields := map[string]any{
			"status":      phase,
			"image":       la.image,
			"replicas":    la.desired,
			"ready":       la.ready,
			"restarts":    la.restarts,
			"live_source": "k8s",
			"live_at":     time.Now().UTC().Format(time.RFC3339),
			"namespaces":  sortedKeys(la.namespaces),
			"images":      sortedKeys(la.images),
		}
		if env := observedResources(la); len(env) > 0 {
			patchFields["observed_resources"] = env
		}
		if la.reason != "" {
			patchFields["reason"] = la.reason
		}
		if la.lastExitCode != nil {
			patchFields["exit_code"] = *la.lastExitCode
		}
		if hostInfo, err := db.PrimaryHostname(ctx, r.pool, k.env, k.app, r.cfg.DefaultDomainBase); err != nil {
			log.Warn().Err(err).Str("app", k.app).Msg("status-reconciler: primary hostname lookup")
		} else if hostInfo.Hostname != "" {
			patchFields["url"] = "https://" + hostInfo.Hostname
			patchFields["url_status"] = hostInfo.Status
			patchFields["url_reason"] = hostInfo.Reason
			if prober != nil && hostInfo.Status == "active" && livenessDue(checkTimes[k], r.cfg.LivenessProbeMinInterval) {
				candidates = append(candidates, livenessCandidate{key: k, hostname: hostInfo.Hostname})
			}
		}
		phases[k] = phase
		patches[k] = patchFields
	}

	if len(candidates) > 0 {
		for k, res := range r.probeLiveness(ctx, prober, candidates) {
			patches[k]["http_status"] = res.status
			patches[k]["http_reason"] = res.reason
			patches[k]["http_checked_at"] = res.checkedAt.Format(time.RFC3339)
		}
	}

	updated := 0
	for k, la := range agg {
		phase := phases[k]
		patch, _ := json.Marshal(patches[k])
		n, err := db.UpdateLiveStatus(ctx, r.pool, k.env, "App", k.app, phase, patch)
		if err != nil {
			log.Error().Err(err).Str("app", k.app).Msg("status-reconciler: update snapshot")
			continue
		}
		updated += int(n)
		if phase == "Ready" {
			for namespace := range la.namespaces {
				if err := db.ResolveAppHealthAlert(ctx, r.pool, namespace, k.app); err != nil {
					log.Warn().Err(err).Str("app", k.app).Str("namespace", namespace).Msg("status-reconciler: resolve ready app health alert")
				}
			}
		}
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
// token is a known environment name. Tenant ArgoCD Applications are named
// "<app>-<env>-<hash>" (collision-proofed by the ApplicationSet), so when the
// LAST token is not an env name but the one BEFORE it is, both are stripped:
// "nextjs-fhvx20-prod-3e0c7967" → "nextjs-fhvx20". Otherwise returns the input
// unchanged. Callers that persist the result must treat an unchanged return as
// "no app derived" — stamping the raw instance label corrupts app_name and
// breaks both the Domains-tab filter and the DeleteApp child cascade.
func stripEnvSuffix(instance string, envNames map[string]bool) string {
	if i := strings.LastIndex(instance, "-"); i > 0 {
		if envNames[instance[i+1:]] {
			return instance[:i]
		}
		if j := strings.LastIndex(instance[:i], "-"); j > 0 && envNames[instance[j+1:i]] {
			return instance[:j]
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

// primaryContainer returns the app container of a pod template — the same one
// imageFromContainers names — so the observed resource envelope describes the
// app and not its logging sidecar.
func primaryContainer(cs []corev1.Container) *corev1.Container {
	for i := range cs {
		if cs[i].Name == "fluent-container" {
			continue
		}
		return &cs[i]
	}
	if len(cs) > 0 {
		return &cs[0]
	}
	return nil
}

// addScaled adds q, repeated n times, into dst. A zero/absent quantity (a
// container with no limit set) contributes nothing, which keeps "unset" and
// "zero" distinguishable in observedResources.
func addScaled(dst *resource.Quantity, q resource.Quantity, n int32) {
	if q.IsZero() {
		return
	}
	for i := int32(0); i < n; i++ {
		dst.Add(q)
	}
}

// observedResources renders the summed envelope in the same shape the console
// already reads for App resources, omitting whatever the workloads never set.
func observedResources(la *liveApp) map[string]string {
	out := map[string]string{}
	for key, q := range map[string]*resource.Quantity{
		"cpu_request":    &la.cpuRequest,
		"cpu_limit":      &la.cpuLimit,
		"memory_request": &la.memRequest,
		"memory_limit":   &la.memLimit,
	} {
		if !q.IsZero() {
			out[key] = q.String()
		}
	}
	return out
}

// sortedKeys flattens a presence set into a stable slice so an unchanged
// cluster produces an unchanged patch (jsonb equality, no snapshot churn).
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

// isLivePod excludes a pod from crash-state aggregation once it is no longer
// the app's current revision: a Terminating pod (DeletionTimestamp set) or one
// that already reached Phase=Succeeded can keep carrying a stale
// waiting.reason=CrashLoopBackOff from before a fix landed and a fresh,
// healthy ReplicaSet replaced it. Without this guard a resolved crashloop reads
// CrashLoop forever until the old pod is garbage-collected. A genuinely
// crashlooping container's pod stays Phase=Running throughout (verified live:
// the pod object itself never restarts, only the container inside it does).
//
// Phase=Failed is deliberately NOT excluded here: a container that exits
// non-zero without ever entering CrashLoopBackOff (a plain crash, no restart
// backoff yet) lands its pod in Phase=Failed, and that pod is exactly the
// signal applyPodCrashState's exit-code case needs. desired/ready (Stopped
// detection) come from the Deployment/StatefulSet/DaemonSet spec/status
// above, not from this pod list, so including Failed pods here does not
// affect desired==0 -> "Stopped".
func isLivePod(p *corev1.Pod) bool {
	if p.DeletionTimestamp != nil {
		return false
	}
	return p.Status.Phase != corev1.PodSucceeded
}

// applyPodCrashState folds one pod's container statuses onto its app's
// aggregate, mirroring detectPodAlert's reason vocabulary and precedence
// (backend/internal/api/app_health_watcher.go): OOMKilled beats
// CrashLoopBackOff/ImagePullBackOff/ErrImagePull because it is the actual root
// cause, the backoff state is just its symptom, and all four of those beat the
// last-resort reasonError case (a plain non-zero exit that never reached a
// named waiting reason at all, e.g. a container killed with exit code 1 before
// the kubelet ever applied a CrashLoopBackOff backoff). The well-known logging
// sidecar is skipped, same convention as imageFromContainers. restarts is a
// max across containers/pods, not a sum, since the signal that matters is "is
// anything still crashing", not a cluster-wide counter.
func applyPodCrashState(la *liveApp, p *corev1.Pod) {
	applyPodCrashStateAt(la, p, time.Now(), crashRecoveryWindow)
}

// crashRecoveryWindow is how long a container must have been running and
// ready before the crash it recovered from stops counting as a live one. It
// is deliberately many reconcile ticks wide (the reconciler visits pods every
// ~30s) so a container that is genuinely flapping — up for seconds, down
// again — never slips through as recovered between two crashes.
const crashRecoveryWindow = 15 * time.Minute

// containerRecovered reports whether a container's recorded termination is
// old news rather than a live incident: it is running right now, its
// readiness probe passes, and it has stayed up for at least window.
//
// LastTerminationState is a permanent record on the pod object and is never
// cleared while the pod lives, so without this gate one historical non-zero
// exit pins the app at phase "CrashLoop" for the pod's whole lifetime —
// livePhase checks crashLooping before readiness, so the app can never read
// "Ready" again no matter how healthy it is. Observed live 2026-08-11:
// artemmendeleev's fonbet-value, pod 1/1 Ready with a single restart, sat in
// the admin panel's not_ready list as "CrashLoop".
//
// This does not weaken the guarantee the livePhase comment relies on (a
// crashlooping container must never read "Ready" just because readyReplicas
// momentarily matches between crashes): a container that crashes every few
// seconds or minutes cannot satisfy a 15-minute ready uptime, so it stays
// "CrashLoop" exactly as before. Only positive evidence of sustained health
// demotes the flag, and a container that is not running, not ready, or whose
// start time is unknown is never treated as recovered.
//
// Mirrors containerRecovered in backend/internal/api/app_health_watcher.go;
// the two live in separate Go modules and the same class of stale-crash
// false positive had to be fixed on both sides.
func containerRecovered(cs corev1.ContainerStatus, now time.Time, window time.Duration) bool {
	running := cs.State.Running
	if running == nil || !cs.Ready || running.StartedAt.IsZero() {
		return false
	}
	return now.Sub(running.StartedAt.Time) >= window
}

func applyPodCrashStateAt(la *liveApp, p *corev1.Pod, now time.Time, window time.Duration) {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "fluent-container" {
			continue
		}
		if cs.RestartCount > la.restarts {
			la.restarts = cs.RestartCount
		}
	}
	if la.reason == reasonOOMKilled {
		return
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "fluent-container" {
			continue
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == reasonOOMKilled {
			if containerRecovered(cs, now, window) {
				continue
			}
			la.crashLooping = true
			la.reason = reasonOOMKilled
			code := cs.LastTerminationState.Terminated.ExitCode
			la.lastExitCode = &code
			return
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "fluent-container" || cs.State.Waiting == nil {
			continue
		}
		switch cs.State.Waiting.Reason {
		case reasonCrashLoopBackOff, reasonImagePullBackOff, reasonErrImagePull:
			la.crashLooping = true
			la.reason = cs.State.Waiting.Reason
			if cs.LastTerminationState.Terminated != nil {
				code := cs.LastTerminationState.Terminated.ExitCode
				la.lastExitCode = &code
			}
			return
		}
	}
	if la.reason != "" {
		return
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == "fluent-container" {
			continue
		}
		term := cs.LastTerminationState.Terminated
		if term == nil {
			term = cs.State.Terminated
		}
		if term == nil || term.ExitCode == 0 {
			continue
		}
		if cs.RestartCount < 1 {
			continue
		}
		if containerRecovered(cs, now, window) {
			continue
		}
		la.crashLooping = true
		la.reason = reasonError
		code := term.ExitCode
		la.lastExitCode = &code
		return
	}
}

// livePhase maps replica readiness and pod crash state to a phase string the
// PhaseBadge understands ("Ready" → green, "CrashLoop" → red). Conservative: a
// partially-ready or scaled-to-zero workload is "Pending"/"Stopped", never
// silently "Ready" — and a crashlooping container never reads "Ready" even
// when readyReplicas momentarily matches desired between crashes (no
// readinessProbe means kubelet marks it ready right up until the next crash).
func livePhase(la *liveApp) string {
	switch {
	case la.desired == 0:
		return "Stopped"
	case la.crashLooping:
		return "CrashLoop"
	case la.ready >= la.desired:
		return "Ready"
	default:
		return "Pending"
	}
}
