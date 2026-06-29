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

	// Jenkins controller (control-plane trigger + poll + progressiveText).
	// One parameterized pipeline job; the framework param (web|android|auto)
	// selects the branch inside jenkins-lib. Specific presets like nextjs /
	// nuxt / react / fastapi / spring / express collapse to the web family
	// here. No per-repo Jenkinsfile.
	JenkinsURL   string
	JenkinsUser  string
	JenkinsToken string
	JenkinsJob   string // parameterized job full name (e.g. "dada-build")

	// Nexus registry (Docker images + raw APK/AAB). Push is owned by Jenkins;
	// the control plane only reads to confirm artifacts.
	NexusDockerHost string // host[:port] for image refs + /v2 API
	NexusRawURL     string // base URL of the raw-hosted repo (download proxy)
	NexusUser       string
	NexusToken      string

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
		DatabaseURL:   getEnv("DATABASE_URL", getEnv("DB_URL", "")),
		WebhookPort:   getEnv("BUILD_WEBHOOK_PORT", "8091"),
		PollInterval:  pollInterval,
		MaxConcurrent: getEnvInt("BUILD_MAX_CONCURRENT", 4),
		BuildTimeout:  buildTimeout,
		MaxRetries:    getEnvInt("BUILD_MAX_RETRIES", 2),

		JenkinsURL:   getEnv("JENKINS_URL", ""),
		JenkinsUser:  getEnv("JENKINS_USER", ""),
		JenkinsToken: getEnv("JENKINS_TOKEN", ""),
		JenkinsJob:   getEnv("JENKINS_JOB", "dada-build"),

		NexusDockerHost: getEnv("NEXUS_DOCKER_HOST", ""),
		NexusRawURL:     getEnv("NEXUS_RAW_URL", ""),
		NexusUser:       getEnv("NEXUS_USER", ""),
		NexusToken:      getEnv("NEXUS_TOKEN", ""),

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
