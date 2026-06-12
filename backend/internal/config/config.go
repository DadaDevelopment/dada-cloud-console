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

	// Values editor WebSocket. Both values must be set to enable the /values-token
	// endpoint. Same env var name in both backend and gitops-agent: GITOPS_VALUES_TOKEN_SECRET.
	GitopsValuesTokenSecret string // GITOPS_VALUES_TOKEN_SECRET
	GitopsAgentWSURL        string // GITOPS_AGENT_WS_URL  (public WS base, e.g. wss://gitops.example.com)

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
		AuthMode:                getEnv("AUTH_MODE", "local"),
		KeycloakIssuer:          getEnv("KEYCLOAK_ISSUER", "https://id.dada-tuda.ru/realms/master"),
		KeycloakVerifyAud:       getEnv("KEYCLOAK_VERIFY_AUD", "false") == "true",
		KeycloakAudience:        getEnv("KEYCLOAK_AUDIENCE", "account"),
		KeycloakRolesClient:     getEnv("KEYCLOAK_ROLES_CLIENT", "service-client"),
		AIStudioEnabled:         getEnv("AI_STUDIO_ENABLED", "true") == "true",
		MLflowBaseURL:           getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader:        getEnv("MLFLOW_AUTH_HEADER", ""),
		InferenceMaxBodyBytes:   getEnvInt64("INFERENCE_MAX_BODY_BYTES", 10*1024*1024),
		GitopsValuesTokenSecret: getEnv("GITOPS_VALUES_TOKEN_SECRET", ""),
		GitopsAgentWSURL:        getEnv("GITOPS_AGENT_WS_URL", ""),
		PortainerURL:            getEnv("PORTAINER_URL", ""),
		PortainerAPIToken:       getEnv("PORTAINER_API_TOKEN", ""),
		PrometheusQueryURL:      getEnv("PROMETHEUS_QUERY_URL", ""),
		PrometheusQueryUser:     getEnv("PROMETHEUS_QUERY_USER", ""),
		PrometheusQueryPass:     getEnv("PROMETHEUS_QUERY_PASS", ""),
		ElasticsearchURL:        getEnv("ELASTICSEARCH_URL", ""),
		ElasticsearchAPIKey:     getEnv("ELASTICSEARCH_API_KEY", ""),
		ElasticsearchIndex:      getEnv("ELASTICSEARCH_LOG_INDEX", "filebeat-*"),
		MCPEnabled:              getEnv("MCP_ENABLED", "true") == "true",
		MCPSelfURL:              getEnv("MCP_SELF_URL", ""),
		MCPOverridesPath:        getEnv("MCP_OVERRIDES_PATH", "overrides.yaml"),
		MCPResourceURL:          getEnv("MCP_RESOURCE_URL", "https://console.dada-tuda.ru/mcp"),
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
