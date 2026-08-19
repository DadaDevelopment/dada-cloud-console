package api

import (
	"context"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dada-tuda/console/backend/internal/agentchat"
	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/beget"
	"github.com/dada-tuda/console/backend/internal/billing"
	"github.com/dada-tuda/console/backend/internal/billing/costengine"
	"github.com/dada-tuda/console/backend/internal/billing/pricing"
	"github.com/dada-tuda/console/backend/internal/billing/tbank"
	"github.com/dada-tuda/console/backend/internal/billing/yookassa"
	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/cache"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/config"
	"github.com/dada-tuda/console/backend/internal/dadagent"
	"github.com/dada-tuda/console/backend/internal/grafana"
	"github.com/dada-tuda/console/backend/internal/llmchat"
	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/mlflow"
	"github.com/dada-tuda/console/backend/internal/notify"
	"github.com/dada-tuda/console/backend/internal/opencost"
	"github.com/dada-tuda/console/backend/internal/pdns"
	"github.com/dada-tuda/console/backend/internal/portainer"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/dada-tuda/console/backend/internal/userservice"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"k8s.io/client-go/kubernetes"
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
	beget       *beget.Client      // nil when BEGET_K8S_TOKEN unset; real hardware-cost source for admin_costs.go
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

	// boxAgentVerifier gates the box-agent ingest webhooks (status + samples).
	// nil disables them, exactly like agentVerifier: the routes are registered
	// only when the verifier builds, which is also what keeps those two
	// platform-internal endpoints out of the OpenAPI coverage gate and therefore
	// out of the reflected MCP tool surface. See webhooks_boxagent.go.
	boxAgentVerifier *auth.KeycloakVerifier

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

	// dbBackupPresigner mints presigned GET URLs to download a database backup's
	// dump straight from the Kanister dump bucket. Never nil: disabled when the
	// dump-bucket S3 access is unconfigured (download handler returns 503).
	dbBackupPresigner cloudtask.DBBackupPresigner

	// sourceUploader stores an uploaded source archive for upload-deploy
	// (UploadSourceArchive). Never nil: disabled when SOURCE_UPLOAD_S3_* is
	// unconfigured (upload handler returns 503).
	sourceUploader cloudtask.SourceUploader

	// podTarExporter execs a live tar.gz of an app's PVC directory out of a
	// running pod (ExportAppVolume). Never nil: off-cluster it is disabled
	// (Enabled() == false) and the export handler returns 503.
	podTarExporter cloudtask.PodTarExporter

	podFS cloudtask.PodFS

	// podProber execs a fixed DNS/TCP/TLS/HTTP diagnostic sequence inside an
	// ephemeral debug container attached to a running app pod (ProbeAppNetwork).
	// Never nil: off-cluster it is disabled (Enabled() == false) and the probe
	// handler returns 503.
	podProber cloudtask.PodProber

	// billingPlans is the full plan catalog loaded once at startup from the
	// embedded plans.yaml. Always populated (the embedded file is compiled in);
	// handlers degrade gracefully if somehow empty.
	billingPlans []pricing.Plan

	// billingUnit / billingMarkup back the informational "real consumption"
	// consumption endpoints. Loaded once from the embedded cluster-cost.yaml;
	// on failure billingUnit stays zero (all costs read 0) and we warn only —
	// this surface is transparency, never a bill, so it must never be fatal.
	billingUnit   costengine.UnitCost
	billingMarkup float64
	billingMargin float64
	billingSnapMu sync.Mutex
	billingSnap   *billingCostSnapshot

	usersvc *userservice.Client

	groupsEnsured sync.Map
	groupsAttempt sync.Map

	pdns *pdns.Client

	// platformHealthCS lazily-built in-cluster client for admin overview's
	// platform_health section (admin_platform_health.go). Off-cluster nil.
	platformHealthOnce sync.Once
	platformHealthCS   kubernetes.Interface

	auditNotifier         *notify.Notifier
	auditNotifyEmail      string
	auditRateLimiter      auditNotifyLimiter
	deployHookNotifyEmail string

	yookassa *yookassa.YooKassaProvider

	yookassaOAuth *yookassa.OAuthClient

	tbank *tbank.Provider

	optionalAuth func(c *gin.Context) (*auth.Claims, bool)

	agentChatLLM   *llmchat.Client
	agentChatTools *agentchat.Toolset
	chat           chatStore

	agentChatIdentityKey atomic.Pointer[string]

	// boxFunnelLimiter bounds the unauthenticated Dada Box fake-door ingest
	// (RecordBoxFunnelEvent). Never nil.
	boxFunnelLimiter *boxFunnelLimiter

	uxIngestLimiter *boxFunnelLimiter

	// boxStack is the box runtime: the LocalRuntime adapter, its warm pool, its
	// attach provider and its edge. nil when BOX_LOCAL_ROOT is unset, in which case
	// every box runtime verb answers 503 with a reason — the same degradation as
	// Portainer, Kanister and the S3 resolvers. See box_runtime.go, and note that
	// the production adapter per ADR-019 is a Pod in the existing cluster, NOT this.
	boxStack *boxRuntimeStack

	now func() time.Time
}

// clock is the injected clock, mirroring BoxMeter's: box_minutes quota
// (checkQuota) and GetBoxUsage both window on the current calendar month, and a
// test that cannot choose "now" can only assert on wall-clock coincidence with the
// next UTC month boundary. h.now is nil in every real Handler, so production
// always takes the time.Now() branch; only a test sets it.
func (h *Handler) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now().UTC()
}

// transcript is the chat store this Handler archives conversations in. A nil
// h.chat means Postgres, so a Handler assembled by hand -- which is every
// Handler in a test -- keeps the storage it has always had, and only an explicit
// AGENT_CHAT_STORE moves the transcript somewhere else.
func (h *Handler) transcript() chatStore {
	if h.chat != nil {
		return h.chat
	}
	return pgChatStore{h}
}

func (h *Handler) optionalClaims(c *gin.Context) (*auth.Claims, bool) {
	if h.optionalAuth != nil {
		return h.optionalAuth(c)
	}
	return auth.GetClaims(c)
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(pool *pgxpool.Pool, cfg *config.Config) *Handler {
	h := &Handler{pool: pool, cfg: cfg}
	h.chat = newChatStore(h)
	h.boxFunnelLimiter = newBoxFunnelLimiter(boxFunnelPerMin, boxFunnelGlobalPerMin)
	h.uxIngestLimiter = newBoxFunnelLimiter(uxIngestPerMin, uxIngestGlobalPerMin)
	if cfg.AIStudioEnabled && cfg.MLflowBaseURL != "" {
		h.mlflow = mlflow.New(cfg.MLflowBaseURL, cfg.MLflowAuthHeader)
	}
	h.portainer = portainer.New(cfg.PortainerURL, cfg.PortainerAPIToken)
	h.prometheus = prometheus.New(cfg.PrometheusQueryURL, cfg.PrometheusQueryUser, cfg.PrometheusQueryPass)
	h.opencost = opencost.New(cfg.OpenCostURL)
	h.beget = beget.New(cfg.BegetK8SToken)
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
	h.StartCostCacheWarmer(context.Background())
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
	h.auditNotifier = notify.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	h.auditNotifyEmail = cfg.AuditNotifyEmail
	h.deployHookNotifyEmail = cfg.DeployHookNotifyEmail
	if h.deployHookNotifyEmail == "" {
		h.deployHookNotifyEmail = cfg.SMTPFrom
	}
	h.counters = cloudtask.NewCounterResolver()
	h.s3creds = cloudtask.NewS3CredentialsResolver(cfg.CrossplaneSecretNamespace)
	h.dbcreds = cloudtask.NewDBCredentialsResolver()
	h.kanister = cloudtask.NewKanisterClient()
	h.dbBackupPresigner = cloudtask.NewDBBackupPresigner(
		cfg.DBBackupS3Endpoint, cfg.DBBackupS3Bucket, cfg.DBBackupS3Region,
		cfg.DBBackupS3AccessKey, cfg.DBBackupS3SecretKey, cfg.DBBackupS3Insecure,
	)
	h.sourceUploader = cloudtask.NewSourceUploader(
		cfg.SourceUploadS3Endpoint, cfg.SourceUploadS3Bucket, cfg.SourceUploadS3Region,
		cfg.SourceUploadS3AccessKey, cfg.SourceUploadS3SecretKey, cfg.SourceUploadS3Insecure,
	)
	h.podTarExporter = cloudtask.NewPodTarExporter()
	h.podFS = cloudtask.NewPodFS()
	h.podProber = cloudtask.NewPodProber()

	plans, err := billing.LoadPlans("")
	if err != nil {
		if cfg.BillingEnabled {
			log.Fatalf("billing: failed to load plans (BILLING_ENABLED=true): %v", err)
		}
		log.Printf("billing: warn: failed to load plans: %v", err)
	} else {
		h.billingPlans = plans
	}

	if cfg.YooKassaShopID != "" && cfg.YooKassaSecretKey != "" {
		ykClient := yookassa.New(cfg.YooKassaShopID, cfg.YooKassaSecretKey)
		h.yookassa = yookassa.NewProvider(pool, ykClient, cfg.YooKassaReturnURL, cfg.YooKassaSendReceipt, cfg.YooKassaVatCode, cfg.YooKassaTaxSystemCode)
	}
	h.yookassaOAuth = yookassa.NewOAuthClient()

	if cfg.TBankBusinessToken != "" && cfg.TBankAccountNumber != "" {
		tbankClient := tbank.New(cfg.TBankBusinessToken, cfg.TBankSandbox)
		h.tbank = tbank.NewProvider(pool, tbankClient, cfg.TBankAccountNumber)
	}

	h.billingMarkup = cfg.PricingMarkup
	h.billingMargin = cfg.PricingMarkup
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

	// Heal projects whose user-service IAM groups were never provisioned. No-op
	// when group sync is disabled (h.usersvc nil).
	h.StartProjectGroupReconciler(context.Background())

	// Silent-crash watcher (P1-2b): emails a project owner when their app is
	// stuck CrashLoopBackOff/OOMKilled/ImagePullBackOff. No-op off-cluster or
	// when SMTP is unconfigured.
	h.StartAppHealthWatcher(context.Background())
	h.StartAppVolumeWatcher(context.Background())
	h.StartAppURLWatcher(context.Background())
	h.StartDBTierReconciler(context.Background())
	h.StartDBQuotaWatcher(context.Background())
	h.StartDBStatsCollector(context.Background())
	h.StartDBMoveWorker(context.Background())
	h.StartDBArchiveWorker(context.Background())
	h.StartDBAdvisoryEngine(context.Background())
	h.StartAppAutoscaleWatcher(context.Background())
	h.StartIdentityDeliveryWatcher(context.Background())
	h.StartDemoAppReaper(context.Background())
	h.StartAppUsageMeter(context.Background())
	h.StartAppUsageBackfill(context.Background())

	h.agentChatLLM = llmchat.New(cfg.AgentChatGatewayURL, cfg.AgentChatGatewayKey, agentChatDefaultModel(cfg.AgentChatModel))
	h.agentChatLLM.KeyFunc = h.currentAgentChatKey
	h.StartAgentChatIdentityRefresher(context.Background())
	h.StartAgentChatMemoryFolder(context.Background())
	selfURL := cfg.MCPSelfURL
	if selfURL == "" {
		selfURL = "http://127.0.0.1:" + cfg.Port
	}
	if toolset, tsErr := agentchat.BuildToolset(EmbeddedSpec(), selfURL); tsErr != nil {
		log.Printf("agent-chat: failed to build toolset: %v", tsErr)
	} else {
		h.agentChatTools = toolset
	}
	// Dada Box runtime (ADR-019). No-op unless BOX_LOCAL_ROOT is set, so tests and
	// every production deployment that has not opted in are untouched.
	h.initBoxRuntime(cfg)
	h.StartBoxSessionSweeper(context.Background())
	h.StartBoxOperationsWorker(context.Background())
	h.StartArchiveRedetectSweeper(context.Background())
	h.startFinCoreSync(cfg)

	if h.agentChatLLM.Configured() {
		source := "static AGENT_CHAT_GATEWAY_KEY"
		if h.currentAgentChatKey() != "" {
			source = "ServiceIdentity " + agentChatIdentityApp
		}
		log.Printf("agent-chat: gateway configured at %s, model %s, credential %s", cfg.AgentChatGatewayURL, h.agentChatLLM.Model, source)
	} else {
		log.Printf("agent-chat: gateway not configured (no AGENT_CHAT_GATEWAY_URL, and no identity token or AGENT_CHAT_GATEWAY_KEY); endpoint answers with a friendly error")
	}

	return h
}
