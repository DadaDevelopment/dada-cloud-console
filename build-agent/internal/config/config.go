package config

import (
	"fmt"
	"os"
	"time"
)

// Config is the flat env-driven configuration for build-agent.
// Mirrors gitops-agent/internal/config conventions: one struct, Load(), getEnv.
// Env-var inventory is taken verbatim from the vercel-flow impl plan §4.
type Config struct {
	DatabaseURL string

	// HTTP server (health + /webhook/github + /ws/build). Empty disables.
	WebhookPort string

	// Poller backstop interval.
	PollInterval time.Duration

	// Build scheduling.
	MaxConcurrent int
	BuildTimeout  time.Duration
	MaxRetries    int

	// k8s Job isolation knobs.
	RuntimeClass   string
	NodePoolLabel  string
	CPULimit       string
	MemLimit       string
	GitEgressCIDRs string

	// Harbor registry.
	HarborURL         string
	HarborAdminUser   string
	HarborAdminSecret string

	// Builder image baked with git+nixpacks+buildctl.
	BuilderImage string

	// GitHub App.
	GitHubAppID         string
	GitHubAppKey        string
	GitHubWebhookSecret string

	// AES-GCM key (hex 32 bytes), shared with gitops-agent, for decrypting
	// GitLab tokens / env-vars stored in the DB.
	EncryptionKey string

	// HMAC secret for /ws/build delegate tokens (matches backend BUILD_AGENT_TOKEN_SECRET).
	TokenSecret string

	// Terminal-log object store + DB-log retention.
	LogObjectStoreURL string
	LogDBRetention    time.Duration
}

func Load() (*Config, error) {
	pollInterval, err := time.ParseDuration(getEnv("BUILD_POLL_INTERVAL", "5s"))
	if err != nil {
		return nil, fmt.Errorf("BUILD_POLL_INTERVAL: %w", err)
	}
	buildTimeout, err := time.ParseDuration(getEnv("BUILD_TIMEOUT", "20m"))
	if err != nil {
		return nil, fmt.Errorf("BUILD_TIMEOUT: %w", err)
	}
	logRetention, err := time.ParseDuration(getEnv("BUILD_LOG_DB_RETENTION", "168h"))
	if err != nil {
		return nil, fmt.Errorf("BUILD_LOG_DB_RETENTION: %w", err)
	}

	cfg := &Config{
		DatabaseURL:    getEnv("DATABASE_URL", getEnv("DB_URL", "")),
		WebhookPort:    getEnv("BUILD_WEBHOOK_PORT", "8091"),
		PollInterval:   pollInterval,
		MaxConcurrent:  getEnvInt("BUILD_MAX_CONCURRENT", 4),
		BuildTimeout:   buildTimeout,
		MaxRetries:     getEnvInt("BUILD_MAX_RETRIES", 2),
		RuntimeClass:   getEnv("BUILD_RUNTIME_CLASS", "gvisor"),
		NodePoolLabel:  getEnv("BUILD_NODE_POOL_LABEL", ""),
		CPULimit:       getEnv("BUILD_CPU_LIMIT", "2"),
		MemLimit:       getEnv("BUILD_MEM_LIMIT", "4Gi"),
		GitEgressCIDRs: getEnv("BUILD_GIT_EGRESS_CIDRS", ""),

		HarborURL:         getEnv("HARBOR_URL", ""),
		HarborAdminUser:   getEnv("HARBOR_ADMIN_USER", ""),
		HarborAdminSecret: getEnv("HARBOR_ADMIN_SECRET", ""),
		BuilderImage:      getEnv("BUILDER_IMAGE", ""),

		GitHubAppID:         getEnv("BUILD_GITHUB_APP_ID", ""),
		GitHubAppKey:        getEnv("BUILD_GITHUB_APP_KEY", ""),
		GitHubWebhookSecret: getEnv("BUILD_GITHUB_WEBHOOK_SECRET", ""),

		EncryptionKey: getEnv("GITOPS_ENCRYPTION_KEY", ""),
		TokenSecret:   getEnv("BUILD_AGENT_TOKEN_SECRET", ""),

		LogObjectStoreURL: getEnv("BUILD_LOG_OBJECT_STORE_URL", ""),
		LogDBRetention:    logRetention,
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}
