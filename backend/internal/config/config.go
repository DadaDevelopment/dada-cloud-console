package config

import (
	"fmt"
	"os"
	"strconv"
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

	// Elasticsearch log search (read-only). VMs ship container logs via filebeat;
	// this is the read side for aggregated log search. Empty ElasticsearchURL
	// disables the /logs search endpoint.
	ElasticsearchURL    string // ELASTICSEARCH_URL
	ElasticsearchAPIKey string // ELASTICSEARCH_API_KEY
	ElasticsearchIndex  string // ELASTICSEARCH_LOG_INDEX (default "filebeat-*")

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

	// SMTP for the Email contact point (shared with IAM invitations). Wired into
	// Grafana's email contact point settings at provision time.
	SMTPHost string // SMTP_HOST
	SMTPPort int    // SMTP_PORT (default 587)
	SMTPUser string // SMTP_USER
	SMTPPass string // SMTP_PASS
	SMTPFrom string // SMTP_FROM

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
	MetrikaOAuthToken      string // METRIKA_OAUTH_TOKEN
}

// Load reads configuration from environment variables.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		DBURL:                   getEnv("DB_URL", getEnv("DATABASE_URL", "")),
		JWTSecret:               getEnv("JWT_SECRET", ""),
		Port:                    getEnv("PORT", getEnv("HTTP_PORT", "8080")),
		LogLevel:                getEnv("LOG_LEVEL", "info"),
		DevMode:                 getEnv("DEV_MODE", "false") == "true",
		ClusterLBIP:             getEnv("CLUSTER_LB_IP", "93.189.231.60"),
		CustomDomainATarget:     getEnv("CUSTOM_DOMAIN_A_TARGET", getEnv("CLUSTER_LB_IP", "93.189.231.60")),
		CustomDomainCNAMETarget: getEnv("CUSTOM_DOMAIN_CNAME_TARGET", "ingress.dada-tuda.ru"),
		CustomDomainVerifyLabel: getEnv("CUSTOM_DOMAIN_VERIFY_LABEL", "_dada-verify"),
		AuthMode:                getEnv("AUTH_MODE", "local"),
		KeycloakIssuer:          getEnv("KEYCLOAK_ISSUER", "https://id.dada-tuda.ru/realms/master"),
		KeycloakVerifyAud:       getEnv("KEYCLOAK_VERIFY_AUD", "false") == "true",
		KeycloakAudience:        getEnv("KEYCLOAK_AUDIENCE", "account"),
		KeycloakRolesClient:     getEnv("KEYCLOAK_ROLES_CLIENT", "service-client"),
		AIStudioEnabled:         getEnv("AI_STUDIO_ENABLED", "true") == "true",
		MLflowBaseURL:           getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader:        getEnv("MLFLOW_AUTH_HEADER", ""),
		InferenceMaxBodyBytes:   getEnvInt64("INFERENCE_MAX_BODY_BYTES", 10*1024*1024),
		GitopsEncryptionKey:     getEnv("GITOPS_ENCRYPTION_KEY", ""),
		InternalAuthToken:       getEnv("INTERNAL_AUTH_TOKEN", ""),
		GitopsValuesTokenSecret: getEnv("GITOPS_VALUES_TOKEN_SECRET", ""),
		GitopsAgentWSURL:        getEnv("GITOPS_AGENT_WS_URL", ""),
		BuildAgentURL:           getEnv("BUILD_AGENT_URL", ""),
		BuildAgentWSURL:         getEnv("BUILD_AGENT_WS_URL", ""),
		BuildAgentTokenSecret:   getEnv("BUILD_AGENT_TOKEN_SECRET", ""),
		GitAppSlug:              getEnv("GIT_APP_SLUG", ""),
		NexusRawURL:             getEnv("NEXUS_RAW_URL", ""),
		NexusUser:               getEnv("NEXUS_USER", ""),
		NexusToken:              getEnv("NEXUS_TOKEN", ""),
		PortainerURL:            getEnv("PORTAINER_URL", ""),
		PortainerAPIToken:       getEnv("PORTAINER_API_TOKEN", ""),
		PrometheusQueryURL:      getEnv("PROMETHEUS_QUERY_URL", ""),
		PrometheusQueryUser:     getEnv("PROMETHEUS_QUERY_USER", ""),
		PrometheusQueryPass:     getEnv("PROMETHEUS_QUERY_PASS", ""),
		ElasticsearchURL:        getEnv("ELASTICSEARCH_URL", ""),
		ElasticsearchAPIKey:     getEnv("ELASTICSEARCH_API_KEY", ""),
		ElasticsearchIndex:      getEnv("ELASTICSEARCH_LOG_INDEX", "filebeat-*"),
		GrafanaBaseURL:           getEnv("GRAFANA_BASE_URL", ""),
		GrafanaPublicURL:         getEnv("GRAFANA_PUBLIC_URL", ""),
		GrafanaAPIToken:          getEnv("GRAFANA_API_TOKEN", ""),
		GrafanaAdminUser:         getEnv("GRAFANA_ADMIN_USER", ""),
		GrafanaAdminPassword:     getEnv("GRAFANA_ADMIN_PASSWORD", ""),
		GrafanaPromDatasourceUID: getEnv("GRAFANA_PROM_DATASOURCE_UID", ""),
		MonitoringLogIndex:       getEnv("MONITORING_LOG_INDEX", "dada-app-logs-*"),
		PrometheusRemoteWriteURL:  getEnv("PROMETHEUS_REMOTE_WRITE_URL", ""),
		PrometheusRemoteWriteUser: getEnv("PROMETHEUS_REMOTE_WRITE_USER", ""),
		PrometheusRemoteWritePass: getEnv("PROMETHEUS_REMOTE_WRITE_PASS", ""),
		MonitoringRateLimitPerMin: int(getEnvInt64("MONITORING_RATE_LIMIT_PER_MIN", 120)),
		MonitoringMaxLabels:       int(getEnvInt64("MONITORING_MAX_LABELS", 30)),
		MonitoringMaxSeriesPerReq: int(getEnvInt64("MONITORING_MAX_SERIES_PER_REQUEST", 2000)),
		GatewayDBURL:              getEnv("GATEWAY_DB_URL", ""),
		GatewayPort:               getEnv("GATEWAY_PORT", "8081"),
		SMTPHost:                 getEnv("SMTP_HOST", ""),
		SMTPPort:                 int(getEnvInt64("SMTP_PORT", 587)),
		SMTPUser:                 getEnv("SMTP_USER", ""),
		SMTPPass:                 getEnv("SMTP_PASS", ""),
		SMTPFrom:                 getEnv("SMTP_FROM", ""),
		MCPEnabled:               getEnv("MCP_ENABLED", "true") == "true",
		MCPSelfURL:               getEnv("MCP_SELF_URL", ""),
		MCPOverridesPath:         getEnv("MCP_OVERRIDES_PATH", "overrides.yaml"),
		MCPResourceURL:           getEnv("MCP_RESOURCE_URL", "https://console.dada-tuda.ru/mcp"),
		DadaAgentBaseURL:       getEnv("DADA_AGENT_BASE_URL", ""),
		KeycloakTokenURL:       getEnv("KEYCLOAK_TOKEN_URL", ""),
		CloudAgentClientID:     getEnv("CLOUD_AGENT_CLIENT_ID", ""),
		CloudAgentClientSecret: getEnv("CLOUD_AGENT_CLIENT_SECRET", ""),
		CloudTaskCallbackURL:   getEnv("CLOUD_TASK_CALLBACK_URL", ""),
		GithubAppID:            getEnv("GITHUB_APP_ID", ""),
		GithubAppPrivateKey:    getEnv("GITHUB_APP_PRIVATE_KEY", ""),
		MetrikaOAuthToken:      getEnv("METRIKA_OAUTH_TOKEN", ""),
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

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
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
