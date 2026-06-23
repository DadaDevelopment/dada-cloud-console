package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL string

	// Default git target — used when project has no git_integrations row.
	DefaultRepoURL  string
	DefaultBranch   string
	DefaultUsername string
	DefaultToken    string

	// Local directory where repos are cloned (one subdir per repo).
	RepoLocalPath string

	BotName  string
	BotEmail string

	PollIntervalDB     time.Duration
	PollIntervalGit    time.Duration
	PollIntervalStatus time.Duration

	// StatusReconcile enables the k8s live-state reconciler: it reads Deployment
	// status from each k8s environment's namespace and writes phase + image +
	// replicas back into resource_snapshots. Disabled automatically when no
	// in-cluster config is available (e.g. local runs).
	StatusReconcileEnabled bool

	// Webhook server — only started when port is non-empty.
	WebhookPort string

	// AES-GCM key (hex-encoded 32 bytes) for encrypting tokens in git_integrations.
	EncryptionKey string

	// Load-balancer IP written into PublicApi manifests.
	ClusterLBIP string

	// MLflow registry — used to resolve <name, version> → s3:// source URI
	// when rendering AIModel manifests pinned to MLflow. Empty disables the
	// resolver and any MLflow-pinned op fails with an actionable error.
	MLflowBaseURL    string
	MLflowAuthHeader string

	// Values editor WebSocket. GITOPS_VALUES_TOKEN_SECRET must match the
	// GITOPS_AGENT_TOKEN_SECRET in the console backend. Empty disables /ws/values.
	ValuesTokenSecret string

	// PreviewEnvTTL is how long a preview (ephemeral) environment lives before the
	// reaper enqueues its teardown. Written to environments.expires_at on creation.
	PreviewEnvTTL time.Duration
}

func Load() (*Config, error) {
	dbInterval, err := time.ParseDuration(getEnv("GITOPS_POLL_INTERVAL_DB", "3s"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_POLL_INTERVAL_DB: %w", err)
	}
	gitInterval, err := time.ParseDuration(getEnv("GITOPS_POLL_INTERVAL_GIT", "30s"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_POLL_INTERVAL_GIT: %w", err)
	}
	previewTTL, err := time.ParseDuration(getEnv("GITOPS_PREVIEW_ENV_TTL", "168h"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_PREVIEW_ENV_TTL: %w", err)
	}
	statusInterval, err := time.ParseDuration(getEnv("GITOPS_POLL_INTERVAL_STATUS", "30s"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_POLL_INTERVAL_STATUS: %w", err)
	}

	cfg := &Config{
		DatabaseURL:       getEnv("DATABASE_URL", getEnv("DB_URL", "")),
		DefaultRepoURL:    getEnv("GITOPS_DEFAULT_REPO_URL", ""),
		DefaultBranch:     getEnv("GITOPS_DEFAULT_BRANCH", "main"),
		DefaultUsername:   getEnv("GITOPS_DEFAULT_USERNAME", getEnv("GIT_USERNAME", "")),
		DefaultToken:      getEnv("GITOPS_DEFAULT_TOKEN", getEnv("GIT_TOKEN", "")),
		RepoLocalPath:     getEnv("GITOPS_REPO_LOCAL_PATH", "/var/lib/gitops-repos"),
		BotName:           getEnv("GITOPS_BOT_NAME", "DADA Platform Bot"),
		BotEmail:          getEnv("GITOPS_BOT_EMAIL", "bot@dada-tuda.ru"),
		PollIntervalDB:     dbInterval,
		PollIntervalGit:    gitInterval,
		PollIntervalStatus: statusInterval,

		StatusReconcileEnabled: getEnv("GITOPS_STATUS_RECONCILE_ENABLED", "true") == "true",
		WebhookPort:       getEnv("GITOPS_WEBHOOK_PORT", ""),
		EncryptionKey:     getEnv("GITOPS_ENCRYPTION_KEY", ""),
		ClusterLBIP:       getEnv("CLUSTER_LB_IP", "93.189.231.60"),
		MLflowBaseURL:     getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader:  getEnv("MLFLOW_AUTH_HEADER", ""),
		ValuesTokenSecret: getEnv("GITOPS_VALUES_TOKEN_SECRET", ""),
		PreviewEnvTTL:     previewTTL,
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.DefaultRepoURL == "" {
		return nil, fmt.Errorf("GITOPS_DEFAULT_REPO_URL is required")
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
