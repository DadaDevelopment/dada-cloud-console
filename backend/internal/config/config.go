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
	}

	if cfg.DBURL == "" {
		return nil, fmt.Errorf("DB_URL is required")
	}
	if cfg.JWTSecret == "" {
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
