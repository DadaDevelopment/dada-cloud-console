package api

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/dadagent"
	"github.com/dada-tuda/console/backend/internal/grafana"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/mlflow"
	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/dada-tuda/console/backend/internal/pdns"
	"github.com/dada-tuda/console/backend/internal/portainer"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/dada-tuda/console/backend/internal/userservice"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler holds shared dependencies for all API handlers.
type Handler struct {
	pool        *pgxpool.Pool
	cfg         *config.Config
	mlflow      *mlflow.Client
	portainer   *portainer.Client  // nil when PORTAINER_URL/PORTAINER_API_TOKEN unset
	prometheus  *prometheus.Client // nil when PROMETHEUS_QUERY_URL unset; infra/container/db reads
	userMetrics *prometheus.Client // user-telemetry read store (multi-tenant Mimir); == prometheus when USER_METRICS_QUERY_URL unset
	logsearch   *logsearch.Client  // nil when ELASTICSEARCH_URL unset
	opencost    *opencost.Client   // nil when OPENCOST_URL unset; per-project cost reads
	cache       *cache.Cache       // nil when REDIS_ADDR unset; fail-open cache-aside for read-heavy endpoints
	// Infra stream (in-cluster kube pod logs) — the second /logs source for
	// native (k8s) apps; nil when ES unset or ELASTICSEARCH_INFRA_LOG_INDEX=off.
	infraLogsearch *logsearch.Client
	buildagent     *buildagent.Client // nil when BUILD_AGENT_URL unset

	// Monitoring read/alert/health layer (ADR-011).
	grafana      *grafana.Client   // nil when GRAFANA_BASE_URL/GRAFANA_API_TOKEN unset
	appLogsearch *logsearch.Client // ES client on the dada-app-logs-* index; nil when ES unset

	// Monitoring write path (PRD-monitoring / ADR-011).
	promwrite     *prometheus.WriteClient // nil when no remote-write URL resolved
	eswrite       *logsearch.WriteClient  // nil when ELASTICSEARCH_URL unset
	ingestLimiter *ingestLimiter          // per-app token bucket
	maxLabels     int                     // cardinality guard: metrics per ingest request

	// DadaAgent cloud-task integration. dadagent is nil when DADA_AGENT_BASE_URL /
	// CLOUD_AGENT_CLIENT_ID are unset (create handler returns 503). agentVerifier
	// is the JWKS verifier that gates the agent webhook callback; nil disables it.
	dadagent      *dadagent.Client
	agentVerifier *auth.KeycloakVerifier

	// counters resolves an app's Yandex Metrika counter id from its live
	// YandexMetrikaCounter CR. Never nil: off-cluster it returns a resolver
	// whose Resolve fails with a clear "not configured" error.
	counters cloudtask.CounterResolver

	// s3creds reveals an S3 bucket's access keys/endpoint by reading the
	// Crossplane connection secret on demand. Never nil: off-cluster it returns
	// a resolver whose Resolve fails with a clear "not configured" error.
	s3creds cloudtask.S3CredentialsResolver

	// dbcreds reveals a managed PostgreSQL database's connection credentials by
	// reading its Crossplane connection secret ("<db>-db-credentials" in the app
	// namespace) on demand. Never nil: off-cluster it returns a resolver whose
	// Resolve fails with a clear "not configured" error.
	dbcreds cloudtask.DBCredentialsResolver

	// kanister drives per-database backup/restore via Kanister ActionSets. Never
	// nil: off-cluster it is disabled (Enabled() == false) and every create fails
	// with a clear "not configured" error.
	kanister cloudtask.KanisterClient

	// billingPlans is the full plan catalog loaded once at startup from the
	// embedded plans.yaml. Always populated (the embedded file is compiled in);
	// handlers degrade gracefully if somehow empty.
	billingPlans []pricing.Plan

	// billingUnit / billingMarkup back the informational "real consumption"
	// consumption endpoints. Loaded once from the embedded cluster-cost.yaml;
	// on failure billingUnit stays zero (all costs read 0) and we warn only —
	// this surface is transparency, never a bill, so it must never be fatal.
	billingUnit    costengine.UnitCost
	billingMarkup  float64
	billingMargin  float64
	billingMinUtil float64
	billingSnapMu  sync.Mutex
	billingSnap    *billingCostSnapshot

	usersvc *userservice.Client

	groupsEnsured sync.Map

	pdns *pdns.Client
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config) *Handler {
	h := &Handler{pool: pool, cfg: cfg}
	if cfg.AIStudioEnabled && cfg.MLflowBaseURL != "" {
		h.mlflow = mlflow.New(cfg.MLflowBaseURL, cfg.MLflowAuthHeader)
	}
	h.portainer = portainer.New(cfg.PortainerURL, cfg.PortainerAPIToken)
	h.prometheus = prometheus.New(cfg.PrometheusQueryURL, cfg.PrometheusQueryUser, cfg.PrometheusQueryPass)
	h.opencost = opencost.New(cfg.OpenCostURL)
	h.cache = cache.New(cfg.RedisAddr)
	if h.cache.Enabled() {
		pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := h.cache.Ping(pingCtx); err != nil {
			log.Printf("cache: redis at %s unreachable at startup, running fail-open (endpoints compute uncached): %v", cfg.RedisAddr, err)
		} else {
			log.Printf("cache: redis cache-aside enabled at %s", cfg.RedisAddr)
		}
		cancel()
	}
	h.StartCostCacheWarmer(context.Background(), h.cfg.CacheCostTTL/2)
	// User-telemetry reads go to the multi-tenant Mimir store (per-tenant
	// X-Scope-OrgID). When USER_METRICS_QUERY_URL is unset, reuse the plain
	// Prometheus client so behaviour is unchanged until the Mimir cutover.
	if cfg.UserMetricsQueryURL != "" {
		h.userMetrics = prometheus.NewMultitenant(cfg.UserMetricsQueryURL, cfg.UserMetricsQueryUser, cfg.UserMetricsQueryPass)
	} else {
		h.userMetrics = h.prometheus
	}
	h.logsearch = logsearch.New(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.ElasticsearchIndex)
	if cfg.ElasticsearchInfraIndex != "off" {
		h.infraLogsearch = logsearch.New(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.ElasticsearchInfraIndex)
	}
	h.buildagent = buildagent.New(cfg.BuildAgentURL)
	// Prefer admin basic-auth (survives the emptyDir-backed Grafana's DB wipe on
	// pod restart); fall back to the service-account token when admin creds are unset.
	if cfg.GrafanaAdminUser != "" && cfg.GrafanaAdminPassword != "" {
		h.grafana = grafana.NewBasicAuth(cfg.GrafanaBaseURL, cfg.GrafanaAdminUser, cfg.GrafanaAdminPassword, cfg.GrafanaPromDatasourceUID, cfg.GrafanaPublicURL)
	} else {
		h.grafana = grafana.New(cfg.GrafanaBaseURL, cfg.GrafanaAPIToken, cfg.GrafanaPromDatasourceUID, cfg.GrafanaPublicURL)
	}
	h.appLogsearch = logsearch.New(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.MonitoringLogIndex)

	// Monitoring write path. Remote-write defaults to the query Prometheus +
	// its creds when a dedicated receiver URL/creds are not set.
	rwURL := cfg.PrometheusRemoteWriteURL
	rwUser := cfg.PrometheusRemoteWriteUser
	rwPass := cfg.PrometheusRemoteWritePass
	if rwURL == "" {
		rwURL = cfg.PrometheusQueryURL
	}
	if rwUser == "" && rwPass == "" {
		rwUser, rwPass = cfg.PrometheusQueryUser, cfg.PrometheusQueryPass
	}
	h.promwrite = prometheus.NewWriteClient(rwURL, rwUser, rwPass)
	h.eswrite = logsearch.NewWriteClient(cfg.ElasticsearchURL, cfg.ElasticsearchAPIKey, cfg.MonitoringLogIndex)
	h.ingestLimiter = newIngestLimiter(cfg.MonitoringRateLimitPerMin)
	h.maxLabels = cfg.MonitoringMaxLabels
	if h.maxLabels <= 0 {
		h.maxLabels = 30
	}

	if cfg.DadaAgentBaseURL != "" && cfg.CloudAgentClientID != "" {
		ts := dadagent.NewTokenSource(cfg.KeycloakTokenURL, cfg.CloudAgentClientID, cfg.CloudAgentClientSecret)
		h.dadagent = dadagent.New(cfg.DadaAgentBaseURL, ts)
	}
	if os.Getenv("PROJECT_GROUP_SYNC_ENABLED") == "true" &&
		cfg.UserServiceURL != "" && cfg.KeycloakTokenURL != "" && cfg.CloudAgentClientID != "" {
		uts := dadagent.NewTokenSource(cfg.KeycloakTokenURL, cfg.CloudAgentClientID, cfg.CloudAgentClientSecret)
		h.usersvc = userservice.New(cfg.UserServiceURL, uts)
		log.Printf("iam: project-group sync to user-service ENABLED")
	}
	h.pdns = pdns.NewClient(cfg.PowerDNSAPIURL, cfg.PowerDNSAPIKey, 15*time.Second)
	h.counters = cloudtask.NewCounterResolver()
	h.s3creds = cloudtask.NewS3CredentialsResolver(cfg.CrossplaneSecretNamespace)
	h.dbcreds = cloudtask.NewDBCredentialsResolver()
	h.kanister = cloudtask.NewKanisterClient()

	plans, err := billing.LoadPlans("")
	if err != nil {
		if cfg.BillingEnabled {
			log.Fatalf("billing: failed to load plans (BILLING_ENABLED=true): %v", err)
		}
		log.Printf("billing: warn: failed to load plans: %v", err)
	} else {
		h.billingPlans = plans
	}

	h.billingMarkup = pricing.MarkupDefault
	h.billingMargin = cfg.BillingMargin
	h.billingMinUtil = cfg.BillingMinUtilization
	if cc, ccErr := billing.LoadClusterCost(""); ccErr != nil {
		log.Printf("billing: warn: failed to load cluster-cost (consumption costs will be 0): %v", ccErr)
	} else if unit, uErr := costengine.ComputeUnitCost(cc); uErr != nil {
		log.Printf("billing: warn: failed to derive unit cost (consumption costs will be 0): %v", uErr)
	} else {
		h.billingUnit = unit
	}

	// Advance in-flight backup/restore ActionSets + retention. No-op off-cluster
	// (Kanister disabled), so tests and local dev never spawn it.
	h.StartBackupReconciler(context.Background())

	return h
}
