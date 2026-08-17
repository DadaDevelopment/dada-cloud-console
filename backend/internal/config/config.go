package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	DBURL       string
	JWTSecret   string
	Port        string
	LogLevel    string
	DevMode     bool
	ClusterLBIP string

	// Custom domains (user-owned domains + auto TLS). The verify poller resolves a
	// TXT challenge to prove ownership before creating the PublicApi. These targets
	// are what the console tells users to put in their own DNS.
	//   CustomDomainATarget     — A-record target for apex domains (defaults to the LB IP)
	//   CustomDomainCNAMETarget — CNAME target for subdomains (a stable hostname → LB)
	//   CustomDomainVerifyLabel — TXT challenge host prefix (record: <label>.<fqdn>)
	CustomDomainATarget     string // CUSTOM_DOMAIN_A_TARGET
	CustomDomainCNAMETarget string // CUSTOM_DOMAIN_CNAME_TARGET
	CustomDomainVerifyLabel string // CUSTOM_DOMAIN_VERIFY_LABEL

	// IngressTLSProbeAddr is the host:port ReconcilePendingHostnames dials, with
	// SNI set to the hostname, to decide whether a hostname's certificate is
	// really being served — i.e. served BY US. It must address our own ingress
	// controller directly, never the public hostname: a domain being migrated
	// from another provider still answers its own name with a valid publicly
	// trusted certificate, so a probe that follows public DNS reports "live"
	// for a domain we do not serve at all. Defaults to the in-cluster Service
	// (no hairpin through the provider LB, and unaffected by any SNI-routing
	// edge in front of it). Set empty to fall back to dialing the public
	// hostname, which is only meaningful outside the cluster (dev, tests).
	IngressTLSProbeAddr string // INGRESS_TLS_PROBE_ADDR

	// Managed DNS via NS delegation (design: docs/plans/2026-07-13-ns-delegation-managed-dns.md).
	// PowerDNSAPIURL is the in-cluster PowerDNS API root; PowerDNSAPIKey is the
	// X-API-Key. An empty PowerDNSAPIKey disables all managed-DNS endpoints (503).
	// PlatformNameservers are the nameservers users delegate their zone to.
	PowerDNSAPIURL      string   // POWERDNS_API_URL
	PowerDNSAPIKey      string   // POWERDNS_API_KEY (secret)
	PlatformNameservers []string // PLATFORM_NAMESERVERS (comma-separated)

	// Identity provider selection. AuthMode defaults to "local" → the existing
	// HS256 local-JWT path (POST /auth/login + GinMiddleware). Set AUTH_MODE
	// to "keycloak" to validate Keycloak RS256 access tokens via JWKS instead;
	// in that mode JWTSecret is no longer required and /auth/login is disabled.
	AuthMode            string // AUTH_MODE: "local" (default) | "keycloak"
	KeycloakIssuer      string // KEYCLOAK_ISSUER (e.g. https://id.dada-tuda.ru/realms/master)
	KeycloakVerifyAud   bool   // KEYCLOAK_VERIFY_AUD: default false (Keycloak access-token aud is often "account")
	KeycloakAudience    string // KEYCLOAK_AUDIENCE: expected aud when KeycloakVerifyAud is true
	KeycloakRolesClient string // KEYCLOAK_ROLES_CLIENT: resource_access client whose roles are extracted

	// AI Studio (v1, declared 2026-05-22). MLflowBaseURL empty disables the
	// registry browser (the wizard falls back to "paste artifactURI").
	// AI_STUDIO_ENABLED remains as a runtime kill-switch — set to "false" to
	// hide the routes again if needed; default is enabled.
	DefaultDomainEnabled bool
	DefaultDomainBase    string

	PreviewHostBase   string
	PreviewHostSecret string

	AIStudioEnabled  bool
	MLflowBaseURL    string
	MLflowAuthHeader string // optional, forwarded as-is on every request

	// InferenceMaxBodyBytes caps both the request and upstream response that
	// the playground proxy is willing to buffer. Default 10 MiB covers
	// typical JSON tabular payloads and a single small image; raise via
	// INFERENCE_MAX_BODY_BYTES if a model legitimately needs larger inputs.
	InferenceMaxBodyBytes int64

	// Shared AES-256 encryption key used for token/secret storage (env_vars, git_repos, etc.).
	// Same secret value as gitops-agent. Hex-encoded 32 bytes.
	GitopsEncryptionKey string // GITOPS_ENCRYPTION_KEY

	// InternalAuthToken guards the internal provisioning API (POST /internal/*),
	// called server-to-server by user-service when it mints a project (ADR-009).
	// When unset, the /internal routes are not registered.
	InternalAuthToken string // INTERNAL_AUTH_TOKEN

	// Values editor WebSocket. Both values must be set to enable the /values-token
	// endpoint. Same env var name in both backend and gitops-agent: GITOPS_VALUES_TOKEN_SECRET.
	GitopsValuesTokenSecret string // GITOPS_VALUES_TOKEN_SECRET
	GitopsAgentWSURL        string // GITOPS_AGENT_WS_URL  (public WS base, e.g. wss://gitops.example.com)

	// build-agent (Vercel-flow). Optional — when unset the git-install/repo-listing
	// proxy and build log-stream token endpoints return 503.
	//   BuildAgentURL          — base HTTP URL of the build-agent (proxied for github repo listing)
	//   BuildAgentWSURL        — public WS base for the build log stream (e.g. wss://build.example.com)
	//   BuildAgentTokenSecret  — HMAC secret the build-agent uses to verify wstoken log-stream tokens
	BuildAgentURL         string // BUILD_AGENT_URL
	BuildAgentWSURL       string // BUILD_AGENT_WS_URL
	BuildAgentTokenSecret string // BUILD_AGENT_TOKEN_SECRET

	// GitHub App slug (the public name in github.com/apps/<slug>). Required for
	// the connect flow: the install-url endpoint sends the browser to
	// github.com/apps/<slug>/installations/new and GitHub redirects back to our
	// install-callback. Empty disables the install-url (503).
	GitAppSlug string // GIT_APP_SLUG

	// Nexus raw repo (mobile artifacts). Used by the artifact-download proxy to
	// stream APK/AAB bytes with server-side creds. Empty NexusRawURL disables
	// downloads (404). Same Nexus the build-agent confirms against (ADR-010).
	NexusRawURL string // NEXUS_RAW_URL  (base of the raw-hosted repo)
	NexusUser   string // NEXUS_USER
	NexusToken  string // NEXUS_TOKEN

	// Namespace holding the Crossplane connection secrets written by the S3Bucket
	// composition (writeConnectionSecretToRef defaults here). The console reads
	// "<bucket>-s3-credentials" from it to reveal object-storage keys on demand.
	CrossplaneSecretNamespace string // CROSSPLANE_SECRET_NAMESPACE

	// Per-database backup/restore via Kanister ActionSets. The console creates
	// ActionSets referencing the shared blueprint + profile against the managed
	// Postgres StatefulSet. Scheduling is opt-in (off by default) so deploying the
	// code never starts hitting the shared server until explicitly enabled.
	DBBackupNamespace       string // DB_BACKUP_NAMESPACE (kanister ns; where ActionSets + profile live)
	DBBackupStatefulSet     string // DB_BACKUP_STATEFULSET (managed Postgres workload)
	DBBackupProfile         string // DB_BACKUP_PROFILE (cr.kanister.io Profile name)
	DBBackupBlueprint       string // DB_BACKUP_BLUEPRINT (per-db blueprint name)
	DBBackupRetentionDays   int    // DB_BACKUP_RETENTION_DAYS
	DBBackupScheduleEnabled bool   // DB_BACKUP_SCHEDULE_ENABLED (opt-in scheduled backups)

	// S3 access to the Kanister dump bucket, used to presign short-lived
	// download URLs for backup dumps so the bytes are pulled straight from
	// object storage (never through the API). Must match the bucket/endpoint the
	// DB_BACKUP_PROFILE writes to. Empty endpoint/bucket/keys disables backup
	// downloads (503).
	DBBackupS3Endpoint  string // DB_BACKUP_S3_ENDPOINT (host[:port], no scheme)
	DBBackupS3Bucket    string // DB_BACKUP_S3_BUCKET
	DBBackupS3Region    string // DB_BACKUP_S3_REGION
	DBBackupS3AccessKey string // DB_BACKUP_S3_ACCESS_KEY
	DBBackupS3SecretKey string // DB_BACKUP_S3_SECRET_KEY
	DBBackupS3Insecure  bool   // DB_BACKUP_S3_INSECURE (plain http; default https)
	// DBBackupS3Prefix is the Kanister Profile's location.prefix. kando prepends
	// it to the dump path on push, so the object key for a direct presigned
	// download is prefix + "/" + db_backups.dump_path. Must match the configured
	// DB_BACKUP_PROFILE's prefix.
	DBBackupS3Prefix string // DB_BACKUP_S3_PREFIX

	// DBShardAdminDSNs maps a db_shards.name to a superuser DSN on that
	// instance, in the form "shard-1=postgres://...,shard-0=postgres://...".
	// The Database Insights collector is the only consumer: it connects per
	// shard, reads system views, and never writes. A shard missing from this
	// map is simply not collected, so adding a shard to the registry without
	// its credentials degrades to no insights rather than to a crash loop.
	DBShardAdminDSNs string // DB_SHARD_ADMIN_DSNS

	// Images the database-archive phases run in. The export and verify phases
	// need the DuckDB CLI (postgres_scanner + httpfs); the repack phase needs
	// pg_repack and psql. Both are third-party images that an installation has
	// to mirror into the registry its cluster may pull from, so both are
	// overridable; empty falls back to the defaults in db_archive_jobs.go.
	DBArchiveExportImage string // DB_ARCHIVE_EXPORT_IMAGE
	DBArchiveRepackImage string // DB_ARCHIVE_REPACK_IMAGE

	// pg-router admin console, used to hold traffic still for the instant a
	// database changes shard. The host must resolve to EVERY router pod (a
	// headless Service), not to the load-balanced one: a PAUSE sent through the
	// balanced Service lands on one replica while the other keeps forwarding
	// writes to the instance the data is leaving. Empty host = no cutovers.
	DBRouterAdminHost     string // DB_ROUTER_ADMIN_HOST
	DBRouterAdminPort     int    // DB_ROUTER_ADMIN_PORT
	DBRouterAdminUser     string // DB_ROUTER_ADMIN_USER
	DBRouterAdminPassword string // DB_ROUTER_ADMIN_PASSWORD

	// S3 access for upload-deploy (docs/plans/2026-07-23-upload-deploy.md):
	// where UploadSourceArchive stores the uploaded archive bytes, keyed
	// "source-uploads/<projectID>/<appName>/<uploadID>.<ext>". build-agent
	// reads the same env-var family to presign a GET against the same
	// object (build-agent/internal/worker/archivesource.go). Empty
	// endpoint/bucket/keys disables uploads (503).
	SourceUploadS3Endpoint  string
	SourceUploadS3Bucket    string
	SourceUploadS3Region    string
	SourceUploadS3AccessKey string
	SourceUploadS3SecretKey string
	SourceUploadS3Insecure  bool

	BoxWorkspaceS3Endpoint  string
	BoxWorkspaceS3Bucket    string
	BoxWorkspaceS3Region    string
	BoxWorkspaceS3AccessKey string
	BoxWorkspaceS3SecretKey string
	BoxWorkspaceS3Prefix    string
	BoxWorkspaceS3Insecure  bool

	// Portainer live-state proxy (read-only). Both must be set to enable the VM
	// /state and /logs endpoints. Same values the portainer-agent uses.
	PortainerURL      string // PORTAINER_URL
	PortainerAPIToken string // PORTAINER_API_TOKEN

	// Prometheus query proxy (read-only). The portainer-agent installs
	// node_exporter/cAdvisor sidecars on VMs that remote_write to a central
	// Prometheus; this is the *read* side that lets the console query it back.
	// Empty PrometheusQueryURL disables all /metrics endpoints. Same host/creds
	// as the agent's remote_write target. Base URL only (no /api/v1/... suffix).
	PrometheusQueryURL  string // PROMETHEUS_QUERY_URL
	PrometheusQueryUser string // PROMETHEUS_QUERY_USER
	PrometheusQueryPass string // PROMETHEUS_QUERY_PASS

	// OpenCost Allocation API base URL (read-only). Empty OpenCostURL disables
	// the per-project /cost endpoint. Base URL only, e.g.
	// http://opencost.opencost.svc.cluster.local:9003. No auth (in-cluster).
	OpenCostURL string // OPENCOST_URL

	// RedisAddr enables the fail-open cache-aside layer (internal/cache) for
	// read-heavy endpoints. host:port, empty disables caching. e.g.
	// dada-cloud-console-redis:6379.
	RedisAddr string

	// CacheCostTTL is how long a per-project cost response is cached. OpenCost
	// aggregates slowly-changing data, so seconds of staleness are fine.
	// Applies to the SHORT windows (24h, 7d) only; see CacheCostSlowTTL.
	CacheCostTTL time.Duration

	// CacheCostSlowTTL is CacheCostTTL for the LONG windows (14d, 30d). A month
	// of accumulated cost barely moves in an hour, while its OpenCost
	// aggregation is by far the most expensive thing the console asks of Mimir
	// (measured on prod: 30d ~100s, 14d ~90s, vs 7d ~60s and 24h ~6s), so the
	// long windows are cached for far longer and refreshed on their own slow
	// schedule. Must exceed CostSlowWarmInterval by enough that a failed slow
	// tick does not expose a cold entry to a user request.
	CacheCostSlowTTL time.Duration

	// CostWarmInterval is how often the cost-cache warmer refreshes the SHORT
	// windows. Set independently of CacheCostTTL: deriving it as TTL/2 assumed a
	// tick finishes well inside the TTL, and once the sweep grew past 300s the
	// warmer ran back-to-back forever and pinned Mimir at >1.5 CPU. Keep it
	// shorter than CacheCostTTL, and longer than one short-window sweep.
	CostWarmInterval time.Duration

	// CostSlowWarmInterval is how often the LONG windows (14d, 30d) are
	// refreshed. This is the knob that trades cost-page freshness for Mimir
	// load; the long windows are ~75% of the total warm cost.
	CostSlowWarmInterval time.Duration

	// CostWarmTimeout bounds each background cost-cache-warmer OpenCost/Mimir
	// call (per window, admin pod compute, billing snapshot). It is patient by
	// design: the warmer runs off the user request path, so it can afford to
	// wait out a slow/throttled Mimir instead of failing every tick like the
	// user-facing 20s client must. Raise via COST_WARM_TIMEOUT_SECONDS if
	// upstream Mimir/OpenCost latency grows past the default.
	CostWarmTimeout time.Duration

	// CacheMetricsTTL bounds how long an analytics/observability response
	// (metric panels, resource health) is served from Redis before recompute.
	// Short by design: the charts tolerate a few seconds of staleness, and the
	// cache exists to collapse the N sequential Mimir/Prometheus round-trips a
	// dashboard load fans out into a single upstream hit per window.
	CacheMetricsTTL time.Duration

	// CacheLogsTTL bounds how long an aggregated log-search response is served
	// from Redis. Shorter than CacheMetricsTTL because tailing logs expect fresher
	// results; still enough to absorb a burst of dashboard refreshes off one
	// OpenSearch query.
	CacheLogsTTL time.Duration

	// Fully-loaded consumption pricing knobs (billing_fullcost.go). BillingMargin
	// is the profit multiplier applied after infra-overhead loading (default 1.4);
	// BillingMinUtilization floors the per-type user share so the overhead factor
	// tops out at 1/min (default 0.30 -> 3.33x). Tunable without a rebuild.
	BillingMargin         float64 // BILLING_MARGIN
	BillingMinUtilization float64 // BILLING_MIN_UTILIZATION

	// HardwareMonthlyCostRUB is the real monthly hosting bill (all Beget nodes
	// backing the cluster), used by the god-admin cost drilldown as the ground
	// truth total that OpenCost's per-namespace proportions get scaled onto —
	// OpenCost's own custom pricing is a modeled unit-cost table, not an actual
	// invoice. Zero (default) means no real total is configured: the drilldown
	// falls back to OpenCost's raw totals (scale factor 1) and flags the
	// response accordingly. No Beget billing API integration exists yet (see
	// admin_costs.go); operators fill this in manually until one is built.
	HardwareMonthlyCostRUB float64 // HARDWARE_MONTHLY_COST_RUB

	// AgentTokenUSDToRUB converts the USD provider cost frozen in the
	// agent_token_usage ledger into rubles at invoice/read time, and
	// AgentTokenMarkup is the cost-plus multiplier billed on top (owner ask B:
	// tariff agent runs, cost-plus x2.7). Both are applied at read time so the
	// stored ledger stays a pure provider-cost record and re-pricing history
	// needs no migration. The FX default is a deliberately conservative lower
	// bound so users are never over-converted; operators raise it toward the
	// live rate via env. See internal/billing/pricing/agent_tokens.go.
	AgentTokenUSDToRUB float64 // AGENT_TOKEN_USD_RUB_RATE
	AgentTokenMarkup   float64 // AGENT_TOKEN_MARKUP

	// BegetK8SToken authenticates the read-only Beget managed-Kubernetes
	// billing client (internal/beget) against api.beget.com. Same bearer JWT
	// as the Terraform bootstrap credential (beget-credentials secret,
	// crossplane-system) -- copied into this app's own secret, never shared
	// live off the same value. Empty disables the beget_api hardware-cost
	// source; the drilldown falls back to HardwareMonthlyCostRUB, then
	// OpenCost's own raw total.
	BegetK8SToken string // BEGET_K8S_TOKEN
	// BegetK8SClusterSlug picks which Beget-managed cluster(s) price_month
	// figures back GetAdminCosts: a comma-separated list of slugs, or empty
	// to sum every cluster the token can see. The platform runs on more than
	// one Beget-managed cluster (the prod console cluster and the separate
	// ArgoCD mgmt cluster), and the hardware bill should cover all of them by
	// default.
	BegetK8SClusterSlug string // BEGET_K8S_CLUSTER_SLUG

	// User-telemetry read store (multi-tenant Grafana Mimir). The monitoring
	// product (user-pushed metrics) reads from here with a per-tenant
	// X-Scope-OrgID header; infra/container/db metrics keep reading the plain
	// PrometheusQueryURL above. Base URL only, ending at the Prometheus-compatible
	// query root (Mimir: http://mimir:8080/prometheus). Empty → the monitoring
	// read path falls back to PrometheusQueryURL (single shared store, no
	// per-tenant isolation) so this is safe to ship before Mimir exists.
	UserMetricsQueryURL  string // USER_METRICS_QUERY_URL
	UserMetricsQueryUser string // USER_METRICS_QUERY_USER
	UserMetricsQueryPass string // USER_METRICS_QUERY_PASS

	// Elasticsearch log search (read-only). VMs ship container logs via filebeat;
	// this is the read side for aggregated log search. Empty ElasticsearchURL
	// disables the /logs search endpoint.
	ElasticsearchURL    string // ELASTICSEARCH_URL
	ElasticsearchAPIKey string // ELASTICSEARCH_API_KEY
	ElasticsearchIndex  string // ELASTICSEARCH_LOG_INDEX (default "filebeat-*")
	// Infra stream carrying in-cluster kube pod logs — where native (k8s) app
	// stdout lands. Queried namespace-scoped as a second source by /logs so
	// native apps get logs alongside the VM/compose user stream. Set to "off"
	// to disable the second query.
	ElasticsearchInfraIndex string // ELASTICSEARCH_INFRA_LOG_INDEX (default "filebeat-*")

	// Monitoring (ADR-011). Grafana is the source of truth for alert rules,
	// contact points and the rich per-resource dashboards; this is the API client
	// the console uses to provision them and to pull firing-alert state for the
	// health badge. Empty GrafanaBaseURL/GrafanaAPIToken disables all alerting +
	// dashboard provisioning (handlers respond 503).
	//   GrafanaBaseURL          — API base (e.g. http://grafana.monitoring.svc:3000)
	//   GrafanaPublicURL        — browser-facing base for deep links (defaults to base)
	//   GrafanaAPIToken         — service-account token (admin: needs folder/alert write)
	//   GrafanaPromDatasourceUID— UID of the Prometheus datasource alert rules query
	//
	// GrafanaAdminUser/GrafanaAdminPassword select admin BASIC-AUTH instead of the
	// token. Prefer them for the shared emptyDir-backed Grafana: a pod restart wipes
	// the Grafana DB (and every service-account token with it), but the admin
	// credential is re-provisioned from env on every boot, so basic-auth survives
	// the wipe and the console keeps provisioning without a manual token re-mint.
	// When both admin vars are set they win; otherwise the token is used.
	GrafanaBaseURL           string // GRAFANA_BASE_URL
	GrafanaPublicURL         string // GRAFANA_PUBLIC_URL
	GrafanaAPIToken          string // GRAFANA_API_TOKEN
	GrafanaAdminUser         string // GRAFANA_ADMIN_USER
	GrafanaAdminPassword     string // GRAFANA_ADMIN_PASSWORD
	GrafanaPromDatasourceUID string // GRAFANA_PROM_DATASOURCE_UID

	// GrafanaMimirQueryURL is the in-cluster Mimir Prometheus-query base used to
	// provision a PER-PROJECT Grafana datasource (carrying X-Scope-OrgID = the
	// project tenant) so embedded dashboards/alerts read only that tenant's series
	// in Mimir. e.g. http://mimir.monitoring.svc.cluster.local:8080/prometheus.
	// When empty, dashboards/alerts fall back to GrafanaPromDatasourceUID (single
	// shared store, no per-tenant isolation in the embed).
	GrafanaMimirQueryURL string // GRAFANA_MIMIR_QUERY_URL

	// Grafana embed auth (backend-mediated iframe SSO). The console mints a
	// short-lived HMAC token (GrafanaEmbedSecret) scoped to one console user +
	// dashboard; the iframe carries it to the grafana-embed-gateway, which fronts
	// grafana.dada-tuda.ru and injects Grafana auth.proxy identity headers. See
	// internal/grafanaembed and cmd/grafana-embed-gateway. Empty secret disables
	// embed-token minting (grafana-link falls back to the plain deep link).
	GrafanaEmbedSecret       string // GRAFANA_EMBED_SECRET (shared with the gateway)
	GrafanaEmbedInternalURL  string // GRAFANA_EMBED_INTERNAL_URL (gateway upstream: internal Grafana svc)
	GrafanaEmbedUpstreamHost string // GRAFANA_EMBED_UPSTREAM_HOST (Host sent upstream; default grafana.dada-tuda.ru)
	GrafanaEmbedCookieDomain string // GRAFANA_EMBED_COOKIE_DOMAIN (gateway session cookie Domain)
	GrafanaEmbedListenAddr   string // GRAFANA_EMBED_LISTEN_ADDR (gateway bind, default :8080)

	// App-log index for monitoring resources (separate from the VM filebeat
	// index). Reuses the same Elasticsearch host/key as ElasticsearchURL.
	MonitoringLogIndex string // MONITORING_LOG_INDEX (default "dada-app-logs-*")

	// Monitoring ingestion (write path — PRD-monitoring / ADR-011).
	// PrometheusRemoteWriteURL is the receiver root (Prometheus started with
	// --web.enable-remote-write-receiver). Empty falls back to PrometheusQueryURL
	// (one Prometheus serves both query + remote-write); user/pass default to the
	// query creds. Empty resolved URL disables the /metrics ingest endpoint.
	// RateLimit/MaxLabels are the per-key abuse guards required by the ADR
	// (cardinality discipline + per-key rate limiting at ingest).
	PrometheusRemoteWriteURL  string // PROMETHEUS_REMOTE_WRITE_URL
	PrometheusRemoteWriteUser string // PROMETHEUS_REMOTE_WRITE_USER
	PrometheusRemoteWritePass string // PROMETHEUS_REMOTE_WRITE_PASS
	MonitoringRateLimitPerMin int    // MONITORING_RATE_LIMIT_PER_MIN (default 120)
	MonitoringMaxLabels       int    // MONITORING_MAX_LABELS (default 30) — metrics per request cap
	MonitoringMaxSeriesPerReq int    // MONITORING_MAX_SERIES_PER_REQUEST (default 2000) — gateway OTLP series/log cap

	// Telemetry Gateway (ADR-012) — standalone write-plane service.
	// GatewayDBURL is its read-only Postgres role (key verify + tenant resolve);
	// empty falls back to DBURL. GatewayPort is the gateway's listen port.
	GatewayDBURL string // GATEWAY_DB_URL
	GatewayPort  string // GATEWAY_PORT (default 8081)

	// UserServiceURL is the base URL of user-service. The telemetry gateway calls
	// its POST /v1/apikeys/introspect endpoint to resolve unified sk-dada- ingest
	// keys (gateway unification). Empty disables unified-key acceptance (sk-dada-
	// keys then 401; legacy dmon_ keys keep working).
	UserServiceURL string // USER_SERVICE_URL

	// SMTP for the Email contact point (shared with IAM invitations). Wired into
	// Grafana's email contact point settings at provision time. Also the relay
	// used for the new-signup owner notification (SignupNotifyEmail).
	SMTPHost string // SMTP_HOST
	SMTPPort int    // SMTP_PORT (default 587)
	SMTPUser string // SMTP_USER
	SMTPPass string // SMTP_PASS
	SMTPFrom string // SMTP_FROM

	// SignupNotifyEmail receives one email per brand-new local user row (first
	// Keycloak-provisioned login, never a repeat login or a service account).
	// Empty disables the feature outright — no SMTP dial is attempted.
	SignupNotifyEmail string // SIGNUP_NOTIFY_EMAIL

	AuditNotifyEmail string // AUDIT_NOTIFY_EMAIL

	// DeployHookNotifyEmail receives one email per deploy-hook create/revoke/
	// trigger event, in addition to the audit_events row already written.
	// Empty in this struct means "resolve to SMTPFrom at handler-init" (the
	// caller does that, since SMTPFrom is what actually sends the mail) — so
	// the out-of-the-box behaviour is development@ notifying itself, with no
	// new prod secret required.
	DeployHookNotifyEmail string // DEPLOY_HOOK_NOTIFY_EMAIL

	// Embedded MCP server. Served at /mcp (Streamable HTTP transport).
	// MCPEnabled defaults to true. Set MCP_ENABLED=false to disable.
	// MCPSelfURL is the loopback URL the MCP proxy uses to call backend
	// handlers (default: http://127.0.0.1:<PORT>/api/v1).
	// MCPOverridesPath is optional path to overrides.yaml for tool curation.
	// MCPResourceURL is the OAuth resource identifier for RFC 9728 metadata.
	MCPEnabled       bool   // MCP_ENABLED (default true)
	MCPSelfURL       string // MCP_SELF_URL (default derived from PORT)
	MCPOverridesPath string // MCP_OVERRIDES_PATH (default "overrides.yaml")
	MCPResourceURL   string // MCP_RESOURCE_URL (default "https://console.dada-tuda.ru/mcp")

	// In-console agent chat (P1-3d phase 2). AgentChatGatewayURL/Key point at
	// the ADR-015 LiteLLM gateway; empty URL or key means the feature is not
	// configured and the chat endpoint answers with a friendly SSE error
	// instead of attempting a call. AgentChatDailyMsgCap is a per-user-per-day
	// count of user turns (agent_chat_messages rows with role='user').
	AgentChatGatewayURL  string // AGENT_CHAT_GATEWAY_URL
	AgentChatGatewayKey  string // AGENT_CHAT_GATEWAY_KEY
	AgentChatModel       string // AGENT_CHAT_MODEL (default "or-gpt-41-mini")
	AgentChatDailyMsgCap int64  // AGENT_CHAT_DAILY_MSG_CAP (default 50)

	// AgentChatModelB and AgentChatModelBPercent are the live A/B. A user whose
	// stable hash falls in the first AgentChatModelBPercent percent runs every
	// turn on AgentChatModelB instead of AgentChatModel; the split is by user,
	// not by turn, so one person never sees two models mid-conversation and the
	// turn rows can be compared per cohort. Zero percent (the default) means the
	// experiment is off.
	AgentChatModelB        string // AGENT_CHAT_MODEL_B
	AgentChatModelBPercent int    // AGENT_CHAT_MODEL_B_PERCENT (default 0)

	// Memory. AgentChatSessionIdleMinutes is the gap that ends a conversation:
	// inside one session the assistant reads the whole transcript, across
	// sessions it reads only the recursive per-user summary. AgentChatHistoryMax
	// Chars bounds that transcript in characters rather than in messages, since
	// what blows a context window is length, not turn count.
	// AgentChatMemoryMaxChars is the size of the summary and doubles as the
	// switch: zero turns the folder off and the assistant remembers nothing
	// across sessions. AgentChatMemoryModel is optional -- empty means the
	// folder runs on whatever the chat itself runs on.
	AgentChatSessionIdleMinutes int    // AGENT_CHAT_SESSION_IDLE_MINUTES (default 60)
	AgentChatHistoryMaxChars    int    // AGENT_CHAT_HISTORY_MAX_CHARS (default 120000)
	AgentChatMemoryMaxChars     int    // AGENT_CHAT_MEMORY_MAX_CHARS (default 1200; 0 disables memory)
	AgentChatMemoryModel        string // AGENT_CHAT_MEMORY_MODEL (default: the chat model)

	// AgentChatModelAllowlist names the models a chat request may ask for by
	// hand ("model" in the request body), comma-separated. It exists so a
	// model swap can be MEASURED against the live tool catalog before it is
	// made the default -- the 2026-08-03 sonnet attempt shipped to every user
	// and had to be reverted the same hour when the provider answered 429.
	// Empty (the default) means no request may override AgentChatModel.
	AgentChatModelAllowlist []string // AGENT_CHAT_MODEL_ALLOWLIST

	// AgentChatStore picks where the chat transcript is kept: "postgres" (the
	// default) or "langfuse". Postgres is the default because it is the only one
	// of the two that can be read back immediately after a write -- Langfuse
	// ingestion is asynchronous and its read API eventually consistent, so on
	// Langfuse a conversation can briefly not include what was just said. The
	// sessions, the memory summary and the confirmation queue stay in Postgres
	// under either value; they are locks, not a transcript.
	AgentChatStore string // AGENT_CHAT_STORE (default "postgres")

	// Langfuse turn tracing for the console agent. An empty public or secret
	// key means disabled: the client is a no-op and the SSE path never waits on
	// it. Turn rows in agent_chat_turns are written either way, so the database
	// side of observability does not depend on Langfuse being reachable.
	LangfuseHost      string // LANGFUSE_HOST
	LangfusePublicKey string // LANGFUSE_PUBLIC_KEY
	LangfuseSecretKey string // LANGFUSE_SECRET_KEY
	LangfuseEnabled   bool   // LANGFUSE_ENABLED (default true; keys still required)

	// DadaAgent cloud-task integration (ADR-cloud-task). The console fires
	// autonomous agent tasks from an app chip: it mints a short-lived GitHub App
	// install token + a Keycloak client-credentials token, submits + executes a
	// DadaAgent intent, and receives status/artifacts via a JWKS-gated webhook.
	// Empty DadaAgentBaseURL/CloudAgentClientID disables the create handler (503).
	//   DadaAgentBaseURL       — base HTTP URL of the agent (e.g. http://dadagent.agent.svc:8080)
	//   KeycloakTokenURL       — issuer + /protocol/openid-connect/token
	//   CloudAgentClientID     — Keycloak SA client (dada-cloud-backend) for client-credentials
	//   CloudAgentClientSecret — secret for the SA client (never logged)
	//   CloudTaskCallbackURL   — public webhook URL the agent calls back
	//   GithubAppID            — numeric app id of argocd-dada
	//   GithubAppPrivateKey    — PEM (PKCS1/PKCS8) signing key for the App JWT (never logged)
	//   MetrikaOAuthToken      — Yandex Metrika mgmt API token (never logged)
	DadaAgentBaseURL       string // DADA_AGENT_BASE_URL
	KeycloakTokenURL       string // KEYCLOAK_TOKEN_URL
	CloudAgentClientID     string // CLOUD_AGENT_CLIENT_ID
	CloudAgentClientSecret string // CLOUD_AGENT_CLIENT_SECRET
	CloudTaskCallbackURL   string // CLOUD_TASK_CALLBACK_URL
	GithubAppID            string // GITHUB_APP_ID
	GithubAppPrivateKey    string // GITHUB_APP_PRIVATE_KEY
	GithubAppClientID      string // GITHUB_APP_CLIENT_ID (public; builds user-authorize URL, secret lives in build-agent)
	GithubOAuthRedirectURI string // GITHUB_OAUTH_REDIRECT_URI (absolute callback; disambiguates the App's multiple callback URLs, must be in the App allowlist)
	MetrikaOAuthToken      string // METRIKA_OAUTH_TOKEN

	// ReactivationCampaignEnabled (REACTIVATION_CAMPAIGN_ENABLED, default
	// false) arms the dormant-account campaign sweeper. Off by default because
	// the sweeper's first tick mails real customers: deploying the code and
	// deciding to send are separate decisions, and only the second one is
	// irreversible.
	ReactivationCampaignEnabled bool

	// ReactivationFixWaveEnabled (REACTIVATION_FIX_WAVE_ENABLED, default
	// false) arms the second letter: first-wave recipients who redeemed the
	// plan and then never built anything get told the git-connect blocker they
	// hit is fixed. Same deploy-vs-send separation as the first wave.
	ReactivationFixWaveEnabled bool

	// SignupEnabled (SIGNUP_ENABLED, default false) gates whether an unknown
	// Keycloak identity may provision a new local users row. Default false is
	// current owner policy — registration stays closed until the first paid
	// plan ships — enforced server-side in auth.ResolveUser, not just by
	// hiding the signup button in the frontend. Existing users keep resolving
	// and refreshing regardless of this flag.
	SignupEnabled bool // SIGNUP_ENABLED (default false)

	BillingEnabled          bool  // BILLING_ENABLED (default false) — guards metering ticker; billing endpoints always load plans read-only
	BillingMeterIntervalSec int64 // BILLING_METER_INTERVAL_SECS (default 3600)
	// BillingExemptOrgs (BILLING_EXEMPT_ORGS, comma-separated) never hit a quota
	// wall. The platform's own org lives here: it carries the demo/e2e estate
	// (dozens of apps) and must not be gated by the customer plan ladder.
	BillingExemptOrgs []string

	// DBQuotaExemptOrgs (DB_QUOTA_EXEMPT_ORGS, comma-separated) owns the
	// platform's own databases: cloud-console, keycloak, nexus, powerdns, the
	// gitops agent's. Their ServiceDatabaseV2 tier is "internal" whatever plan
	// the org happens to be on, so the storage-quota ladder can never put the
	// control plane or SSO into read-only.
	//
	// It defaults to the platform org rather than to empty on purpose: a
	// protection that holds only once somebody remembers to set an env var is
	// not a protection. It is deliberately not BillingExemptOrgs, which gates
	// app and box quotas and is owner policy.
	DBQuotaExemptOrgs []string

	BillingOverageBlockFactor float64

	AppUsageBackfillDays   int
	AppUsageBackfillTenant string

	// Dada Box metering knobs (internal/api/box_meter.go, box_reaper.go). Named
	// BOX_* rather than BILLING_BOX_* because the same window also drives the
	// reaper's sleep decision, which is a lifecycle concern and not a billing one.
	//
	// BoxMeterIntervalSecs is the meter's tick period. 60 by default and it should
	// stay there: the ledger's grain is one minute, so a longer period does not
	// batch work, it drops minutes nobody bills. Shorter is harmless (the PK makes
	// a re-tick a no-op upsert) but pointless.
	BoxMeterIntervalSecs int64 // BOX_METER_INTERVAL_SECS (default 60)
	// BoxActiveWindowSecs is how stale an activity signal may be and still count
	// for the minute being metered. 120 = two meter ticks, so ONE lost box-agent
	// sample does not bill an active box as idle. Deliberately biased toward
	// billing: the sample comes from outside the guest and a dropped one is our
	// packet loss, not the customer going idle.
	BoxActiveWindowSecs int64 // BOX_ACTIVE_WINDOW_SECS (default 120)
	// BoxActiveCPUPercent is the guest-CPU floor, in percent of ONE core, above
	// which a minute is active even with nobody attached. 5% bills a detached
	// `cargo build` or a model download — the work an agent leaves running is
	// exactly the work worth paying for — while staying above the idle noise of
	// sshd, cron and the box daemon itself.
	BoxActiveCPUPercent float64 // BOX_ACTIVE_CPU_PERCENT (default 5)
	// BoxDefaultSpendCapRub is the cap applied to a box whose creator named none
	// (boxes.spend_cap_rub IS NULL). A default rather than "unlimited" on purpose:
	// the runaway this protects against is an agent in a loop, and an agent in a
	// loop belongs to a customer who did not think to set a cap. Reaching it
	// suspends the box; it never deletes it.
	BoxDefaultSpendCapRub float64 // BOX_DEFAULT_SPEND_CAP_RUB (default 500)

	// PublicBaseURL is the console's own public origin. Used to build absolute
	// URLs handed back to the caller, e.g. the deploy-hook consumption endpoint
	// (POST {PublicBaseURL}/api/v1/deploy) shown once when a deploy hook is created.
	PublicBaseURL string // PUBLIC_BASE_URL

	// AIGatewayPublicURL is the OpenAI-compatible base_url a customer points an
	// SDK at. Handed back verbatim with a minted AI Gateway key and shown in the
	// console quickstart, so it must be the gateway's own public ingress
	// (ADR-015: the gateway is a separate data plane, never behind the console
	// origin).
	AIGatewayPublicURL string // AI_GATEWAY_PUBLIC_URL

	// AIRoutingMarkup is the multiplier applied to a routed call's provider cost
	// to get what the customer is billed when the project runs on the platform's
	// own provider key. 1.0 means routing is passed through at cost. Pricing
	// lives here and never in the gateway: the gateway routes, the platform
	// decides what a route is worth (ADR-015).
	AIRoutingMarkup float64 // AI_ROUTING_MARKUP

	// YooKassa (payments slice 1, own shop/keys, no multi-tenant OAuth). Empty
	// YooKassaShopID/YooKassaSecretKey means payments are unconfigured and
	// checkout returns 409 payments_not_configured instead of attempting a call.
	YooKassaShopID      string // YOOKASSA_SHOP_ID
	YooKassaSecretKey   string // YOOKASSA_SECRET_KEY
	YooKassaReturnURL   string // YOOKASSA_RETURN_URL (default https://console.dada-tuda.ru/billing/return)
	YooKassaSendReceipt bool   // YOOKASSA_SEND_RECEIPT (default false; 54-FZ fiscal receipt block)

	// Fiscal receipt shape. Both depend on the merchant's own tax
	// registration, so neither has a defensible hardcoded value:
	// YooKassaVatCode 1 is "no VAT" (simplified regime), 2/3/4 are the 0/10/20
	// percent rates. YooKassaTaxSystemCode is required only for a merchant
	// registered under more than one tax system; 0 omits it, which is what a
	// single-regime shop needs.
	YooKassaVatCode       int // YOOKASSA_VAT_CODE (default 1)
	YooKassaTaxSystemCode int // YOOKASSA_TAX_SYSTEM_CODE (default 0 = omit)

	// YooKassa Partners API OAuth (payments slice 2, "Connect payments" app
	// resource -- a merchant connects their OWN YooKassa shop via OAuth, no
	// platform shop/keys involved). Empty YooKassaPartnerClientID/Secret means
	// connect returns 409 payments_not_configured instead of attempting OAuth.
	YooKassaPartnerClientID     string // YOOKASSA_PARTNER_CLIENT_ID
	YooKassaPartnerClientSecret string // YOOKASSA_PARTNER_CLIENT_SECRET

	// T-Bank Business invoice payments (B2B "pay by bank transfer" method).
	// Empty TBankBusinessToken/TBankAccountNumber means the invoice method is
	// unconfigured: CreateInvoice returns 409 payments_not_configured and the
	// statement-reconcile ticker never starts, the same degrade-off shape as
	// YooKassa above.
	TBankBusinessToken         string        // TBANK_BUSINESS_TOKEN
	TBankAccountNumber         string        // TBANK_ACCOUNT_NUMBER
	TBankSandbox               bool          // TBANK_SANDBOX (default false)
	TBankStatementPollInterval time.Duration // TBANK_STATEMENT_POLL_INTERVAL_SECONDS (default 15m)

	// Platform requisites printed on every generated invoice (payer sees these
	// as who they are paying). No defensible hardcoded value for any of
	// these -- they are the operator's own legal entity details -- so every
	// one is blank until set in the environment.
	PlatformINN          string // PLATFORM_INN
	PlatformKPP          string // PLATFORM_KPP
	PlatformOGRN         string // PLATFORM_OGRN
	PlatformName         string // PLATFORM_NAME
	PlatformLegalAddress string // PLATFORM_LEGAL_ADDRESS
	PlatformBankAccount  string // PLATFORM_BANK_ACCOUNT
	PlatformBankBIC      string // PLATFORM_BANK_BIC
	PlatformBankName     string // PLATFORM_BANK_NAME
	PlatformCorrAccount  string // PLATFORM_CORR_ACCOUNT

	// Dada Box runtime (ADR-019). ONE switch turns the whole feature on:
	// BoxLocalRoot empty means every box verb answers 503 with a reason, the same
	// way Portainer, Kanister and the S3 resolvers degrade when unconfigured.
	//
	// The variable is named BOX_LOCAL_ROOT rather than BOX_ROOT on purpose. It
	// enables internal/box.LocalRuntime, the single-host adapter — NOT the
	// production adapter, which runs a box as a Pod in the existing cluster. A
	// values file must not be able to switch the local adapter on while reading as
	// though it switched on "the box runtime".
	// DemoAppTTLHours is how long an app deployed from a platform starter template
	// survives before the reaper deletes it. Read with getEnvIntAllowZero because
	// 0 is a real setting that turns the reaper off, and the whole point of the
	// field is that an operator can stop automatic deletion of user-visible apps
	// without a redeploy.
	DemoAppTTLHours int // DEMO_APP_TTL_HOURS (default 24; 0 disables the reaper)

	BoxLocalRoot string // BOX_LOCAL_ROOT (empty = box runtime disabled)
	// BoxWarmPoolSize is the number of parked bodies the fleet keeps hot. Zero is
	// an EXPLICIT, honourable setting rather than an unset one: it turns the pool
	// off and makes every spawn pay the cold start. That distinction is the whole
	// reason this field is not read with getEnvInt — a warm pool is billed around
	// the clock in pods and volumes whether or not anyone claims from it, so the
	// operator needs a way to say "nobody is buying this yet, stop paying for it"
	// that does not require deleting the deployment.
	BoxWarmPoolSize int    // BOX_WARM_POOL_SIZE (default 2; 0 disables the pool)
	BoxWarmImage    string // BOX_WARM_IMAGE (default warm-v1, must exist in boxcatalog)
	BoxRegion       string // BOX_REGION (pool key; default "")
	BoxHostnameBase string // BOX_HOSTNAME_BASE (platform wildcard for expose)
	// BoxCrystallizeDomainBase is the suffix a promoted artifact answers on when the
	// caller names no domain. It defaults to the platform wildcard rather than to
	// the bare apex, because that is the only suffix the cluster holds a certificate
	// for: an artifact addressed outside it is served the ingress default cert, and
	// the promotion's own end-to-end HTTPS check fails on a name mismatch while the
	// workload itself is perfectly healthy.
	BoxCrystallizeDomainBase string // BOX_CRYSTALLIZE_DOMAIN_BASE
	// BoxSessionBaseURL is where the box's own session surface answers. Defaults to
	// MCPSelfURL and then to loopback, so there is one answer to "where does this
	// process serve requests" rather than two that can disagree.
	BoxSessionBaseURL string // BOX_SESSION_BASE_URL

	// BoxBrokerDir is a directory containing the box-broker binary, bind-mounted
	// read-only into every box so the box has an endpoint of its own (D6). Empty
	// means the boxes of this host have no door of their own and `box up` says so
	// in its response rather than publishing the control-plane fallback as if it
	// were the box's.
	//
	// A DIRECTORY and not a path to the binary, because the value is a bind-mount
	// source: pointing it at /usr/local/bin would mount the host's whole bin
	// directory into every tenant's body, which is the kind of accident a type
	// cannot catch but a name can.
	BoxBrokerDir string // BOX_BROKER_DIR

	// Managed Postgres the attach path provisions into. It lives OUTSIDE the box on
	// purpose: a disposable body must not own the customer's database.
	// BoxManagedPGURL is a superuser DSN used by the control plane and never
	// injected anywhere; BoxManagedPGHost/Port are how the BOX reaches the cluster,
	// kept separate because the platform's path to a database and the tenant's are
	// routinely different and conflating them yields a DSN that works from the
	// control plane and fails inside the guest.
	BoxManagedPGURL  string // BOX_MANAGED_PG_URL
	BoxManagedPGHost string // BOX_MANAGED_PG_HOST
	BoxManagedPGPort int    // BOX_MANAGED_PG_PORT

	// The cluster box adapter (ADR-019), which is the production one: a box is a
	// Pod in the existing cluster. BoxClusterNamespace empty means this adapter is
	// off, exactly like BoxLocalRoot for the local one. Setting BOTH is a
	// misconfiguration rather than a merge, and the wiring resolves it by
	// preferring the local adapter and saying so, because a host that names a
	// local root is a developer machine.
	//
	// BoxClusterPullSecret is an imagePullSecret name inside the namespace. It is a
	// NAME and never a credential: the secret is created by whoever administers the
	// cluster, and this process only points pods at it.
	BoxClusterNamespace    string // BOX_CLUSTER_NAMESPACE (empty = cluster adapter off)
	BoxClusterPullSecret   string // BOX_CLUSTER_PULL_SECRET
	BoxClusterStorageClass string // BOX_CLUSTER_STORAGE_CLASS (default longhorn-box)
	BoxClusterTLSSecret    string // BOX_CLUSTER_TLS_SECRET (default box-wildcard-tls)
}

// Load reads configuration from environment variables.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		DBURL:                       getEnv("DB_URL", getEnv("DATABASE_URL", "")),
		JWTSecret:                   getEnv("JWT_SECRET", ""),
		Port:                        getEnv("PORT", getEnv("HTTP_PORT", "8080")),
		LogLevel:                    getEnv("LOG_LEVEL", "info"),
		DevMode:                     getEnv("DEV_MODE", "false") == "true",
		ClusterLBIP:                 getEnv("CLUSTER_LB_IP", "155.212.223.198"),
		CustomDomainATarget:         getEnv("CUSTOM_DOMAIN_A_TARGET", getEnv("CLUSTER_LB_IP", "155.212.223.198")),
		CustomDomainCNAMETarget:     getEnv("CUSTOM_DOMAIN_CNAME_TARGET", "cloud.dada-tuda.ru"),
		CustomDomainVerifyLabel:     getEnv("CUSTOM_DOMAIN_VERIFY_LABEL", "_dada-verify"),
		IngressTLSProbeAddr:         getEnv("INGRESS_TLS_PROBE_ADDR", "ingress-nginx-pub-controller.network.svc.cluster.local:443"),
		PowerDNSAPIURL:              getEnv("POWERDNS_API_URL", "http://powerdns-api.powerdns.svc:8081"),
		PowerDNSAPIKey:              getEnv("POWERDNS_API_KEY", ""),
		PlatformNameservers:         splitList(getEnv("PLATFORM_NAMESERVERS", "ns1.dada-tuda.ru,ns2.dada-tuda.ru")),
		DefaultDomainEnabled:        getEnv("DEFAULT_DOMAIN_ENABLED", "true") == "true",
		DefaultDomainBase:           getEnv("DEFAULT_DOMAIN_BASE", "dada-tuda.ru"),
		PreviewHostBase:             getEnv("PREVIEW_HOST_BASE", "pv.dada-tuda.ru"),
		PreviewHostSecret:           getEnv("PREVIEW_HOST_SECRET", getEnv("JWT_SECRET", "")),
		AuthMode:                    getEnv("AUTH_MODE", "local"),
		KeycloakIssuer:              getEnv("KEYCLOAK_ISSUER", "https://id.dada-tuda.ru/realms/master"),
		KeycloakVerifyAud:           getEnv("KEYCLOAK_VERIFY_AUD", "false") == "true",
		KeycloakAudience:            getEnv("KEYCLOAK_AUDIENCE", "account"),
		KeycloakRolesClient:         getEnv("KEYCLOAK_ROLES_CLIENT", "service-client"),
		AIStudioEnabled:             getEnv("AI_STUDIO_ENABLED", "true") == "true",
		MLflowBaseURL:               getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader:            getEnv("MLFLOW_AUTH_HEADER", ""),
		InferenceMaxBodyBytes:       getEnvInt64("INFERENCE_MAX_BODY_BYTES", 10*1024*1024),
		GitopsEncryptionKey:         getEnv("GITOPS_ENCRYPTION_KEY", ""),
		InternalAuthToken:           getEnv("INTERNAL_AUTH_TOKEN", ""),
		GitopsValuesTokenSecret:     getEnv("GITOPS_VALUES_TOKEN_SECRET", ""),
		GitopsAgentWSURL:            getEnv("GITOPS_AGENT_WS_URL", ""),
		BuildAgentURL:               getEnv("BUILD_AGENT_URL", ""),
		BuildAgentWSURL:             getEnv("BUILD_AGENT_WS_URL", ""),
		BuildAgentTokenSecret:       getEnv("BUILD_AGENT_TOKEN_SECRET", ""),
		GitAppSlug:                  getEnv("GIT_APP_SLUG", ""),
		NexusRawURL:                 getEnv("NEXUS_RAW_URL", ""),
		NexusUser:                   getEnv("NEXUS_USER", ""),
		NexusToken:                  getEnv("NEXUS_TOKEN", ""),
		CrossplaneSecretNamespace:   getEnv("CROSSPLANE_SECRET_NAMESPACE", "crossplane-system"),
		DBBackupNamespace:           getEnv("DB_BACKUP_NAMESPACE", "databases"),
		DBBackupStatefulSet:         getEnv("DB_BACKUP_STATEFULSET", "postgresql"),
		DBBackupProfile:             getEnv("DB_BACKUP_PROFILE", "dada-db-backups"),
		DBBackupBlueprint:           getEnv("DB_BACKUP_BLUEPRINT", "postgres-logical-db-blueprint"),
		DBBackupRetentionDays:       getEnvInt("DB_BACKUP_RETENTION_DAYS", 14),
		DBBackupScheduleEnabled:     getEnv("DB_BACKUP_SCHEDULE_ENABLED", "false") == "true",
		DBBackupS3Endpoint:          getEnv("DB_BACKUP_S3_ENDPOINT", ""),
		DBBackupS3Bucket:            getEnv("DB_BACKUP_S3_BUCKET", ""),
		DBBackupS3Region:            getEnv("DB_BACKUP_S3_REGION", "us-east-1"),
		DBBackupS3AccessKey:         getEnv("DB_BACKUP_S3_ACCESS_KEY", ""),
		DBBackupS3SecretKey:         getEnv("DB_BACKUP_S3_SECRET_KEY", ""),
		DBBackupS3Insecure:          getEnv("DB_BACKUP_S3_INSECURE", "false") == "true",
		DBBackupS3Prefix:            getEnv("DB_BACKUP_S3_PREFIX", "k10/postgresql-logical"),
		DBShardAdminDSNs:            getEnv("DB_SHARD_ADMIN_DSNS", ""),
		DBArchiveExportImage:        getEnv("DB_ARCHIVE_EXPORT_IMAGE", ""),
		DBArchiveRepackImage:        getEnv("DB_ARCHIVE_REPACK_IMAGE", ""),
		DBRouterAdminHost:           getEnv("DB_ROUTER_ADMIN_HOST", ""),
		DBRouterAdminPort:           getEnvInt("DB_ROUTER_ADMIN_PORT", 5432),
		DBRouterAdminUser:           getEnv("DB_ROUTER_ADMIN_USER", "pgbouncer_auth"),
		DBRouterAdminPassword:       getEnv("DB_ROUTER_ADMIN_PASSWORD", ""),
		SourceUploadS3Endpoint:      getEnv("SOURCE_UPLOAD_S3_ENDPOINT", ""),
		SourceUploadS3Bucket:        getEnv("SOURCE_UPLOAD_S3_BUCKET", ""),
		SourceUploadS3Region:        getEnv("SOURCE_UPLOAD_S3_REGION", "us-east-1"),
		SourceUploadS3AccessKey:     getEnv("SOURCE_UPLOAD_S3_ACCESS_KEY", ""),
		SourceUploadS3SecretKey:     getEnv("SOURCE_UPLOAD_S3_SECRET_KEY", ""),
		SourceUploadS3Insecure:      getEnv("SOURCE_UPLOAD_S3_INSECURE", "false") == "true",
		BoxWorkspaceS3Endpoint:      getEnv("BOX_WORKSPACE_S3_ENDPOINT", getEnv("SOURCE_UPLOAD_S3_ENDPOINT", "")),
		BoxWorkspaceS3Bucket:        getEnv("BOX_WORKSPACE_S3_BUCKET", getEnv("SOURCE_UPLOAD_S3_BUCKET", "")),
		BoxWorkspaceS3Region:        getEnv("BOX_WORKSPACE_S3_REGION", getEnv("SOURCE_UPLOAD_S3_REGION", "us-east-1")),
		BoxWorkspaceS3AccessKey:     getEnv("BOX_WORKSPACE_S3_ACCESS_KEY", getEnv("SOURCE_UPLOAD_S3_ACCESS_KEY", "")),
		BoxWorkspaceS3SecretKey:     getEnv("BOX_WORKSPACE_S3_SECRET_KEY", getEnv("SOURCE_UPLOAD_S3_SECRET_KEY", "")),
		BoxWorkspaceS3Prefix:        getEnv("BOX_WORKSPACE_S3_PREFIX", "box-workspaces"),
		BoxWorkspaceS3Insecure:      getEnv("BOX_WORKSPACE_S3_INSECURE", getEnv("SOURCE_UPLOAD_S3_INSECURE", "false")) == "true",
		PortainerURL:                getEnv("PORTAINER_URL", ""),
		PortainerAPIToken:           getEnv("PORTAINER_API_TOKEN", ""),
		PrometheusQueryURL:          getEnv("PROMETHEUS_QUERY_URL", ""),
		PrometheusQueryUser:         getEnv("PROMETHEUS_QUERY_USER", ""),
		PrometheusQueryPass:         getEnv("PROMETHEUS_QUERY_PASS", ""),
		OpenCostURL:                 getEnv("OPENCOST_URL", ""),
		RedisAddr:                   getEnv("REDIS_ADDR", ""),
		CacheCostTTL:                time.Duration(getEnvInt64("CACHE_COST_TTL_SECONDS", 300)) * time.Second,
		CacheCostSlowTTL:            time.Duration(getEnvInt64("CACHE_COST_SLOW_TTL_SECONDS", 5400)) * time.Second,
		CostWarmInterval:            time.Duration(getEnvInt64("COST_WARM_INTERVAL_SECONDS", 150)) * time.Second,
		CostSlowWarmInterval:        time.Duration(getEnvInt64("COST_SLOW_WARM_INTERVAL_SECONDS", 1800)) * time.Second,
		CostWarmTimeout:             time.Duration(getEnvInt64("COST_WARM_TIMEOUT_SECONDS", 240)) * time.Second,
		CacheMetricsTTL:             time.Duration(getEnvInt64("CACHE_METRICS_TTL_SECONDS", 20)) * time.Second,
		CacheLogsTTL:                time.Duration(getEnvInt64("CACHE_LOGS_TTL_SECONDS", 10)) * time.Second,
		BillingMargin:               getEnvFloat("BILLING_MARGIN", 1.4),
		BillingMinUtilization:       getEnvFloat("BILLING_MIN_UTILIZATION", 0.30),
		HardwareMonthlyCostRUB:      getEnvFloat("HARDWARE_MONTHLY_COST_RUB", 0),
		AgentTokenUSDToRUB:          getEnvFloat("AGENT_TOKEN_USD_RUB_RATE", 80.0),
		AgentTokenMarkup:            getEnvFloat("AGENT_TOKEN_MARKUP", 2.7),
		BegetK8SToken:               getEnv("BEGET_K8S_TOKEN", ""),
		BegetK8SClusterSlug:         getEnv("BEGET_K8S_CLUSTER_SLUG", ""),
		UserMetricsQueryURL:         getEnv("USER_METRICS_QUERY_URL", ""),
		UserMetricsQueryUser:        getEnv("USER_METRICS_QUERY_USER", ""),
		UserMetricsQueryPass:        getEnv("USER_METRICS_QUERY_PASS", ""),
		ElasticsearchURL:            getEnv("ELASTICSEARCH_URL", ""),
		ElasticsearchAPIKey:         getEnv("ELASTICSEARCH_API_KEY", ""),
		ElasticsearchIndex:          getEnv("ELASTICSEARCH_LOG_INDEX", "filebeat-*"),
		ElasticsearchInfraIndex:     getEnv("ELASTICSEARCH_INFRA_LOG_INDEX", "filebeat-*"),
		GrafanaBaseURL:              getEnv("GRAFANA_BASE_URL", ""),
		GrafanaPublicURL:            getEnv("GRAFANA_PUBLIC_URL", ""),
		GrafanaAPIToken:             getEnv("GRAFANA_API_TOKEN", ""),
		GrafanaAdminUser:            getEnv("GRAFANA_ADMIN_USER", ""),
		GrafanaAdminPassword:        getEnv("GRAFANA_ADMIN_PASSWORD", ""),
		GrafanaPromDatasourceUID:    getEnv("GRAFANA_PROM_DATASOURCE_UID", ""),
		GrafanaMimirQueryURL:        getEnv("GRAFANA_MIMIR_QUERY_URL", ""),
		GrafanaEmbedSecret:          getEnv("GRAFANA_EMBED_SECRET", ""),
		GrafanaEmbedInternalURL:     getEnv("GRAFANA_EMBED_INTERNAL_URL", ""),
		GrafanaEmbedUpstreamHost:    getEnv("GRAFANA_EMBED_UPSTREAM_HOST", "grafana.dada-tuda.ru"),
		GrafanaEmbedCookieDomain:    getEnv("GRAFANA_EMBED_COOKIE_DOMAIN", ""),
		GrafanaEmbedListenAddr:      getEnv("GRAFANA_EMBED_LISTEN_ADDR", ":8080"),
		MonitoringLogIndex:          getEnv("MONITORING_LOG_INDEX", "dada-app-logs-*"),
		PrometheusRemoteWriteURL:    getEnv("PROMETHEUS_REMOTE_WRITE_URL", ""),
		PrometheusRemoteWriteUser:   getEnv("PROMETHEUS_REMOTE_WRITE_USER", ""),
		PrometheusRemoteWritePass:   getEnv("PROMETHEUS_REMOTE_WRITE_PASS", ""),
		MonitoringRateLimitPerMin:   getEnvInt("MONITORING_RATE_LIMIT_PER_MIN", 120),
		MonitoringMaxLabels:         getEnvInt("MONITORING_MAX_LABELS", 30),
		MonitoringMaxSeriesPerReq:   getEnvInt("MONITORING_MAX_SERIES_PER_REQUEST", 2000),
		GatewayDBURL:                getEnv("GATEWAY_DB_URL", ""),
		UserServiceURL:              getEnv("USER_SERVICE_URL", ""),
		GatewayPort:                 getEnv("GATEWAY_PORT", "8081"),
		SMTPHost:                    getEnv("SMTP_HOST", ""),
		SMTPPort:                    getEnvInt("SMTP_PORT", 587),
		SMTPUser:                    getEnv("SMTP_USER", ""),
		SMTPPass:                    getEnv("SMTP_PASS", ""),
		SMTPFrom:                    getEnv("SMTP_FROM", ""),
		SignupNotifyEmail:           getEnv("SIGNUP_NOTIFY_EMAIL", "alexkekiy@icloud.com"),
		AuditNotifyEmail:            getEnv("AUDIT_NOTIFY_EMAIL", "alexkekiy@icloud.com"),
		DeployHookNotifyEmail:       getEnv("DEPLOY_HOOK_NOTIFY_EMAIL", ""),
		MCPEnabled:                  getEnv("MCP_ENABLED", "true") == "true",
		MCPSelfURL:                  getEnv("MCP_SELF_URL", ""),
		MCPOverridesPath:            getEnv("MCP_OVERRIDES_PATH", "overrides.yaml"),
		MCPResourceURL:              getEnv("MCP_RESOURCE_URL", "https://console.dada-tuda.ru/mcp"),
		AgentChatGatewayURL:         getEnv("AGENT_CHAT_GATEWAY_URL", "http://ai-gateway-service.argocd-prod.svc.cluster.local"),
		AgentChatGatewayKey:         getEnv("AGENT_CHAT_GATEWAY_KEY", ""),
		AgentChatModel:              getEnv("AGENT_CHAT_MODEL", "or-gpt-41-mini"),
		AgentChatModelB:             getEnv("AGENT_CHAT_MODEL_B", ""),
		AgentChatModelBPercent:      getEnvIntAllowZero("AGENT_CHAT_MODEL_B_PERCENT", 0),
		AgentChatDailyMsgCap:        getEnvInt64("AGENT_CHAT_DAILY_MSG_CAP", 50),
		AgentChatSessionIdleMinutes: getEnvInt("AGENT_CHAT_SESSION_IDLE_MINUTES", 60),
		AgentChatHistoryMaxChars:    getEnvInt("AGENT_CHAT_HISTORY_MAX_CHARS", 120000),
		AgentChatMemoryMaxChars:     getEnvIntAllowZero("AGENT_CHAT_MEMORY_MAX_CHARS", 1200),
		AgentChatMemoryModel:        getEnv("AGENT_CHAT_MEMORY_MODEL", ""),
		AgentChatModelAllowlist:     splitList(getEnv("AGENT_CHAT_MODEL_ALLOWLIST", "")),
		AgentChatStore:              getEnv("AGENT_CHAT_STORE", "postgres"),
		LangfuseHost:                getEnv("LANGFUSE_HOST", "https://cloud.langfuse.com"),
		LangfusePublicKey:           getEnv("LANGFUSE_PUBLIC_KEY", ""),
		LangfuseSecretKey:           getEnv("LANGFUSE_SECRET_KEY", ""),
		LangfuseEnabled:             getEnv("LANGFUSE_ENABLED", "true") == "true",
		DadaAgentBaseURL:            getEnv("DADA_AGENT_BASE_URL", ""),
		KeycloakTokenURL:            getEnv("KEYCLOAK_TOKEN_URL", ""),
		CloudAgentClientID:          getEnv("CLOUD_AGENT_CLIENT_ID", ""),
		CloudAgentClientSecret:      getEnv("CLOUD_AGENT_CLIENT_SECRET", ""),
		CloudTaskCallbackURL:        getEnv("CLOUD_TASK_CALLBACK_URL", ""),
		GithubAppID:                 getEnv("GITHUB_APP_ID", ""),
		GithubAppPrivateKey:         getEnv("GITHUB_APP_PRIVATE_KEY", ""),
		GithubAppClientID:           getEnv("GITHUB_APP_CLIENT_ID", ""),
		GithubOAuthRedirectURI:      getEnv("GITHUB_OAUTH_REDIRECT_URI", ""),
		MetrikaOAuthToken:           getEnv("METRIKA_OAUTH_TOKEN", ""),
		SignupEnabled:               getEnv("SIGNUP_ENABLED", "false") == "true",
		BillingEnabled:              getEnv("BILLING_ENABLED", "false") == "true",
		ReactivationCampaignEnabled: getEnv("REACTIVATION_CAMPAIGN_ENABLED", "false") == "true",
		ReactivationFixWaveEnabled:  getEnv("REACTIVATION_FIX_WAVE_ENABLED", "false") == "true",
		BillingMeterIntervalSec:     getEnvInt64("BILLING_METER_INTERVAL_SECS", 3600),
		BillingExemptOrgs:           splitList(getEnv("BILLING_EXEMPT_ORGS", "")),
		DBQuotaExemptOrgs:           splitList(getEnv("DB_QUOTA_EXEMPT_ORGS", "dada")),
		BillingOverageBlockFactor:   getEnvFloat("BILLING_OVERAGE_BLOCK_FACTOR", 3),
		AppUsageBackfillDays:        getEnvIntAllowZero("APP_USAGE_BACKFILL_DAYS", 21),
		AppUsageBackfillTenant:      getEnv("APP_USAGE_BACKFILL_TENANT", "opencost"),
		BoxMeterIntervalSecs:        getEnvInt64("BOX_METER_INTERVAL_SECS", 60),
		BoxActiveWindowSecs:         getEnvInt64("BOX_ACTIVE_WINDOW_SECS", 120),
		BoxActiveCPUPercent:         getEnvFloat("BOX_ACTIVE_CPU_PERCENT", 5),
		BoxDefaultSpendCapRub:       getEnvFloat("BOX_DEFAULT_SPEND_CAP_RUB", 500),
		PublicBaseURL:               getEnv("PUBLIC_BASE_URL", "https://console.dada-tuda.ru"),
		AIGatewayPublicURL:          getEnv("AI_GATEWAY_PUBLIC_URL", "https://ai.dada-tuda.ru/v1"),
		AIRoutingMarkup:             getEnvFloat("AI_ROUTING_MARKUP", 1.3),
		YooKassaShopID:              getEnv("YOOKASSA_SHOP_ID", ""),
		YooKassaSecretKey:           getEnv("YOOKASSA_SECRET_KEY", ""),
		YooKassaReturnURL:           getEnv("YOOKASSA_RETURN_URL", "https://console.dada-tuda.ru/billing/return"),
		YooKassaSendReceipt:         getEnv("YOOKASSA_SEND_RECEIPT", "false") == "true",
		YooKassaVatCode:             getEnvInt("YOOKASSA_VAT_CODE", 1),
		YooKassaTaxSystemCode:       getEnvInt("YOOKASSA_TAX_SYSTEM_CODE", 0),
		YooKassaPartnerClientID:     getEnv("YOOKASSA_PARTNER_CLIENT_ID", ""),
		YooKassaPartnerClientSecret: getEnv("YOOKASSA_PARTNER_CLIENT_SECRET", ""),

		TBankBusinessToken:         getEnv("TBANK_BUSINESS_TOKEN", ""),
		TBankAccountNumber:         getEnv("TBANK_ACCOUNT_NUMBER", ""),
		TBankSandbox:               getEnv("TBANK_SANDBOX", "false") == "true",
		TBankStatementPollInterval: time.Duration(getEnvInt64("TBANK_STATEMENT_POLL_INTERVAL_SECONDS", 900)) * time.Second,

		PlatformINN:          getEnv("PLATFORM_INN", ""),
		PlatformKPP:          getEnv("PLATFORM_KPP", ""),
		PlatformOGRN:         getEnv("PLATFORM_OGRN", ""),
		PlatformName:         getEnv("PLATFORM_NAME", ""),
		PlatformLegalAddress: getEnv("PLATFORM_LEGAL_ADDRESS", ""),
		PlatformBankAccount:  getEnv("PLATFORM_BANK_ACCOUNT", ""),
		PlatformBankBIC:      getEnv("PLATFORM_BANK_BIC", ""),
		PlatformBankName:     getEnv("PLATFORM_BANK_NAME", ""),
		PlatformCorrAccount:  getEnv("PLATFORM_CORR_ACCOUNT", ""),

		DemoAppTTLHours: getEnvIntAllowZero("DEMO_APP_TTL_HOURS", 24),

		BoxLocalRoot:             getEnv("BOX_LOCAL_ROOT", ""),
		BoxWarmPoolSize:          getEnvIntAllowZero("BOX_WARM_POOL_SIZE", 2),
		BoxWarmImage:             getEnv("BOX_WARM_IMAGE", "warm-v1"),
		BoxBrokerDir:             getEnv("BOX_BROKER_DIR", ""),
		BoxRegion:                getEnv("BOX_REGION", ""),
		BoxHostnameBase:          getEnv("BOX_HOSTNAME_BASE", "box.dada-tuda.ru"),
		BoxCrystallizeDomainBase: getEnv("BOX_CRYSTALLIZE_DOMAIN_BASE", getEnv("BOX_HOSTNAME_BASE", "box.dada-tuda.ru")),
		BoxSessionBaseURL:        getEnv("BOX_SESSION_BASE_URL", ""),
		BoxManagedPGURL:          getEnv("BOX_MANAGED_PG_URL", ""),
		BoxManagedPGHost:         getEnv("BOX_MANAGED_PG_HOST", "127.0.0.1"),
		BoxManagedPGPort:         getEnvInt("BOX_MANAGED_PG_PORT", 5432),
		BoxClusterNamespace:      getEnv("BOX_CLUSTER_NAMESPACE", ""),
		BoxClusterPullSecret:     getEnv("BOX_CLUSTER_PULL_SECRET", ""),
		BoxClusterStorageClass:   getEnv("BOX_CLUSTER_STORAGE_CLASS", "longhorn-box"),
		BoxClusterTLSSecret:      getEnv("BOX_CLUSTER_TLS_SECRET", "box-wildcard-tls"),
	}

	if cfg.DBURL == "" {
		return nil, fmt.Errorf("DB_URL is required")
	}
	// JWT_SECRET is only required in local auth mode (it signs/validates the
	// HS256 tokens). In keycloak mode validation is done via JWKS, so the secret
	// is irrelevant and need not be set.
	if cfg.AuthMode != "keycloak" && cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	return cfg, nil
}

// GatewayEmbedConfig is the minimal configuration the grafana-embed-gateway
// needs. The gateway is a stateless reverse proxy: it never opens the database
// or validates console JWTs, so unlike Load() it does NOT require DB_URL or
// JWT_SECRET. Field names mirror the matching Config fields.
type GatewayEmbedConfig struct {
	LogLevel                 string
	GrafanaEmbedSecret       string
	GrafanaEmbedInternalURL  string
	GrafanaEmbedUpstreamHost string
	GrafanaEmbedCookieDomain string
	GrafanaEmbedListenAddr   string
}

// LoadGatewayEmbed reads only the env the grafana-embed-gateway uses and
// validates the two that are mandatory (the shared HMAC secret and the upstream
// Grafana URL). It deliberately omits the DB_URL / JWT_SECRET requirements of
// Load() so the gateway can run without database credentials.
func LoadGatewayEmbed() (*GatewayEmbedConfig, error) {
	cfg := &GatewayEmbedConfig{
		LogLevel:                 getEnv("LOG_LEVEL", "info"),
		GrafanaEmbedSecret:       getEnv("GRAFANA_EMBED_SECRET", ""),
		GrafanaEmbedInternalURL:  getEnv("GRAFANA_EMBED_INTERNAL_URL", ""),
		GrafanaEmbedUpstreamHost: getEnv("GRAFANA_EMBED_UPSTREAM_HOST", "grafana.dada-tuda.ru"),
		GrafanaEmbedCookieDomain: getEnv("GRAFANA_EMBED_COOKIE_DOMAIN", ""),
		GrafanaEmbedListenAddr:   getEnv("GRAFANA_EMBED_LISTEN_ADDR", ":8080"),
	}
	if cfg.GrafanaEmbedSecret == "" {
		return nil, fmt.Errorf("GRAFANA_EMBED_SECRET is required")
	}
	if cfg.GrafanaEmbedInternalURL == "" {
		return nil, fmt.Errorf("GRAFANA_EMBED_INTERNAL_URL is required (internal Grafana svc base URL)")
	}
	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// splitList parses a comma-separated env value into a trimmed, non-empty slice.
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// getEnvInt reads a positive int-sized setting.
//
// It exists so no call site has to write int(getEnvInt64(...)). That conversion is
// not merely ugly: on a 32-bit build it silently truncates a 64-bit value, so
// SMTP_PORT=4294967883 would land next to 587 instead of being rejected. CodeQL
// flags every one of those call sites, and it is right to. strconv.Atoi parses into
// a platform int and errors on overflow, which makes the whole class impossible
// instead of guarded seven times.
//
// Non-positive and unparseable values fall back to the default, matching
// getEnvInt64's contract — every setting using this is a count, a port or an
// interval, and zero has never meant "zero" for any of them.
func getEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

// getEnvIntAllowZero reads a non-negative int env var, treating an explicit 0 as
// a value rather than as garbage.
//
// getEnvInt cannot be used for a setting whose zero means "off", because it
// folds n <= 0 into the default and so silently answers 2 to an operator who
// wrote 0. That is not a cosmetic difference for anything the platform pays for
// while idle: BOX_WARM_POOL_SIZE=0 read as 2 keeps two pods and two volumes
// billed around the clock, and the values file, the ConfigMap and the pod env
// all read 0 while the process disagrees — which makes the money look like a
// cluster fault instead of a parse.
//
// Negatives and unparseable text still fall back to the default: a pool cannot
// be smaller than empty, and a typo should not disable a feature silently.
func getEnvIntAllowZero(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}

func getEnvInt64(key string, defaultVal int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

// getEnvFloat reads a positive float env var, returning defaultVal when unset,
// unparseable, or non-positive.
func getEnvFloat(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return defaultVal
	}
	return f
}
