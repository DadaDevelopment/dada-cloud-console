package config

import (
	"fmt"
	"os"
	"strings"
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

	// ClusterDiscoveryEnabled controls whether the status reconciler also mirrors
	// EVERY custom platform CR found on the cluster into resource_snapshots
	// (discover pass). Default false: resources become visible only through the
	// two git paths (an app's resources.values.yaml and its chart/templates),
	// where project+env come from the git path — so a resource can never leak
	// into a project it wasn't committed under. Enable only to fall back to
	// cluster-truth mirroring. Live App/AIModel phase updates are unaffected
	// (they update existing rows regardless of this flag).
	ClusterDiscoveryEnabled bool

	// Webhook server — only started when port is non-empty.
	WebhookPort string

	// AES-GCM key (hex-encoded 32 bytes) for encrypting tokens in git_integrations.
	EncryptionKey string

	// Load-balancer IP written into PublicApi manifests.
	ClusterLBIP string

	// DefaultDomainDNSTarget is the A-record value published for managed default
	// hostnames: the ingress-nginx-pub load-balancer IP that serves them.
	DefaultDomainDNSTarget string

	// DefaultDomainBase is the suffix of an auto-created surrogate hostname
	// (e.g. <app>-<hash>.dada-tuda.ru). Mirrors the console backend's
	// DEFAULT_DOMAIN_BASE so the status reconciler can tell a surrogate apart
	// from a user-owned custom domain when picking which hostname to surface.
	DefaultDomainBase string

	// DefaultDomainTLSSecret, when set, makes managed default-domain Ingresses
	// reference a shared wildcard TLS secret (requires that secret to be
	// replicated into every app namespace). Empty (the default) makes each
	// surrogate host obtain its own per-host cert via cert-manager HTTP-01,
	// exactly like user-owned custom domains -- no wildcard cert or reflector.
	DefaultDomainTLSSecret string

	// MLflow registry — used to resolve <name, version> → s3:// source URI
	// when rendering AIModel manifests pinned to MLflow. Empty disables the
	// resolver and any MLflow-pinned op fails with an actionable error.
	MLflowBaseURL    string
	MLflowAuthHeader string

	// Values editor WebSocket. GITOPS_VALUES_TOKEN_SECRET must match the
	// GITOPS_AGENT_TOKEN_SECRET in the console backend. Empty disables /ws/values.
	ValuesTokenSecret string

	// PreviewReapInterval is how often the TTL reaper scans for expired legacy
	// preview environments and enqueues their teardown (DeletePreviewEnv).
	// Previews are no longer a product feature, so nothing sets expires_at any
	// more; the reaper survives only to finish off rows created before removal.
	PreviewReapInterval time.Duration

	// OrphanGC garbage-collects App snapshots that no DeleteApp op ever cleaned
	// up — rows left behind when an app is re-homed/renamed between projects and
	// the incremental git-watcher missed the delete side of the diff. A k8s App
	// snapshot with no live Deployment AND no app.yaml in git is an orphan. It is
	// first soft-marked (phase=Orphaned) after OrphanMarkAfter, then physically
	// deleted OrphanPurgeAfter later — a two-stage grace so a transient git/pod
	// gap can never lose data. Compose (VM) apps are never touched: their desired
	// spec lives in the DB, not git, so absence-from-git is not absence.
	OrphanGCEnabled  bool
	OrphanMarkAfter  time.Duration
	OrphanPurgeAfter time.Duration

	MoveVolumeEnabled bool

	// DBRouterHost is the connection router (pg-router) every application should
	// reach its managed Postgres through. provider-sql publishes the shard's own
	// address into "<appRef>-db-credentials" once, when the role is created, and
	// never rewrites it afterwards - so a database that later moves to another
	// shard would keep a DSN pointing at the instance it no longer lives on. When
	// this is set, the status reconciler rewrites that secret's endpoint to the
	// router, which is what makes a shard address a private detail.
	//
	// Empty (the default) disables the rewrite entirely: every secret keeps the
	// exact endpoint provider-sql wrote, which is today's behaviour.
	DBRouterHost string
	DBRouterPort string

	// DBRouterDirectShards are shards whose databases keep their direct address
	// even when the router is enabled.
	//
	// Empty is the right default. The exception exists for the platform's own
	// connections (console, Keycloak), and those are not wired by this
	// reconciler at all -- they come from the release's shared secrets. Naming
	// a shard here only strips router indirection from the tenant databases
	// that happen to sit on it, which is exactly what the router is for: after
	// the move to shard-0 the old default handed every new tenant a shard
	// address again.
	DBRouterDirectShards []string
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
	previewReapInterval, err := time.ParseDuration(getEnv("GITOPS_PREVIEW_REAP_INTERVAL", "10m"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_PREVIEW_REAP_INTERVAL: %w", err)
	}
	statusInterval, err := time.ParseDuration(getEnv("GITOPS_POLL_INTERVAL_STATUS", "30s"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_POLL_INTERVAL_STATUS: %w", err)
	}
	orphanMark, err := time.ParseDuration(getEnv("GITOPS_ORPHAN_MARK_AFTER", "15m"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_ORPHAN_MARK_AFTER: %w", err)
	}
	orphanPurge, err := time.ParseDuration(getEnv("GITOPS_ORPHAN_PURGE_AFTER", "2h"))
	if err != nil {
		return nil, fmt.Errorf("GITOPS_ORPHAN_PURGE_AFTER: %w", err)
	}

	cfg := &Config{
		DatabaseURL:        getEnv("DATABASE_URL", getEnv("DB_URL", "")),
		DefaultRepoURL:     getEnv("GITOPS_DEFAULT_REPO_URL", ""),
		DefaultBranch:      getEnv("GITOPS_DEFAULT_BRANCH", "main"),
		DefaultUsername:    getEnv("GITOPS_DEFAULT_USERNAME", getEnv("GIT_USERNAME", "")),
		DefaultToken:       getEnv("GITOPS_DEFAULT_TOKEN", getEnv("GIT_TOKEN", "")),
		RepoLocalPath:      getEnv("GITOPS_REPO_LOCAL_PATH", "/var/lib/gitops-repos"),
		BotName:            getEnv("GITOPS_BOT_NAME", "DADA Platform Bot"),
		BotEmail:           getEnv("GITOPS_BOT_EMAIL", "bot@dada-tuda.ru"),
		PollIntervalDB:     dbInterval,
		PollIntervalGit:    gitInterval,
		PollIntervalStatus: statusInterval,

		StatusReconcileEnabled:  getEnv("GITOPS_STATUS_RECONCILE_ENABLED", "true") == "true",
		ClusterDiscoveryEnabled: getEnv("GITOPS_CLUSTER_DISCOVERY_ENABLED", "false") == "true",
		WebhookPort:             getEnv("GITOPS_WEBHOOK_PORT", ""),
		EncryptionKey:           getEnv("GITOPS_ENCRYPTION_KEY", ""),
		ClusterLBIP:             getEnv("CLUSTER_LB_IP", "93.189.231.60"),
		MLflowBaseURL:           getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader:        getEnv("MLFLOW_AUTH_HEADER", ""),
		ValuesTokenSecret:       getEnv("GITOPS_VALUES_TOKEN_SECRET", ""),
		PreviewReapInterval:     previewReapInterval,
		DefaultDomainTLSSecret:  getEnv("GITOPS_DEFAULT_DOMAIN_TLS_SECRET", ""),
		DefaultDomainDNSTarget:  getEnv("GITOPS_DEFAULT_DOMAIN_DNS_TARGET", "155.212.223.198"),
		DefaultDomainBase:       getEnv("DEFAULT_DOMAIN_BASE", "dada-tuda.ru"),

		OrphanGCEnabled:  getEnv("GITOPS_ORPHAN_GC_ENABLED", "true") == "true",
		OrphanMarkAfter:  orphanMark,
		OrphanPurgeAfter: orphanPurge,

		MoveVolumeEnabled: getEnv("MOVE_VOLUME_ENABLED", "false") == "true",

		DBRouterHost:         getEnv("DB_ROUTER_HOST", ""),
		DBRouterPort:         getEnv("DB_ROUTER_PORT", "5432"),
		DBRouterDirectShards: splitList(os.Getenv("DB_ROUTER_DIRECT_SHARDS")),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.DefaultRepoURL == "" {
		return nil, fmt.Errorf("GITOPS_DEFAULT_REPO_URL is required")
	}

	return cfg, nil
}

// splitList parses a comma-separated env value into a trimmed, non-empty list.
func splitList(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
