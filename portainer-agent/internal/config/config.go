package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all configuration loaded from environment variables.
type Config struct {
	DatabaseURL string

	PortainerURL      string
	PortainerAPIToken string
	// PortainerEdgeURL is the PUBLIC Portainer URL advertised to edge agents on
	// external VMs (e.g. https://portainer.dada-tuda.ru). The tunnel address is
	// derived from it (host:8000). Must be externally resolvable — the in-cluster
	// PortainerURL (…svc.cluster.local) is NOT reachable from VMs. Falls back to
	// PortainerURL when unset.
	PortainerEdgeURL string

	BegetLogin    string
	BegetPassword string
	BegetToken    string
	BegetRegion   string
	// BegetSoftwareSlug is the OS slug passed to the Beget "data.beget_software" data source,
	// e.g. "ubuntu-24-04".
	BegetSoftwareSlug string

	// Beget reader (reverse-sync): discover VMs created outside the console and
	// adopt them into Terraform state.
	BegetAPIBaseURL    string // e.g. https://api.beget.com
	BegetReaderEnabled bool
	BegetReaderProject string // console project slug imported VMs land in (e.g. "internal")

	// AgentSSHPrivateKey is the PEM-encoded private key used to SSH into provisioned VMs.
	AgentSSHPrivateKey string
	// AgentSSHPublicKey is the OpenSSH public key (ssh-rsa ...) registered on the VM via Terraform.
	AgentSSHPublicKey string

	TFWorkspaceBase string
	TFStateConnStr  string
	TFBinPath       string

	GitopsRepoURL       string
	GitopsBranch        string
	GitopsUsername      string
	GitopsToken         string
	GitopsRepoLocalPath string
	GitopsBotName       string
	GitopsBotEmail      string

	PrometheusRemoteWriteURL  string
	PrometheusRemoteWriteUser string
	PrometheusRemoteWritePass string

	ElasticsearchURL    string
	ElasticsearchAPIKey string

	PollIntervalDB      time.Duration
	PollIntervalStatus  time.Duration
	AgentConnectTimeout time.Duration
	// PollIntervalReader is how often the beget-reader scans Beget for new VMs.
	PollIntervalReader time.Duration
	// BegetReaderGrace skips VMs younger than this (race guard vs in-flight console creates).
	BegetReaderGrace time.Duration

	DevMode bool
}

// Load reads env vars and returns a Config or a descriptive error.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:       getEnv("DATABASE_URL", ""),
		PortainerURL:      getEnv("PORTAINER_URL", ""),
		PortainerAPIToken: getEnv("PORTAINER_API_TOKEN", ""),
		PortainerEdgeURL:  getEnv("PORTAINER_EDGE_URL", ""),

		BegetLogin:        getEnv("BEGET_LOGIN", ""),
		BegetPassword:     getEnv("BEGET_PASSWORD", ""),
		BegetToken:        getEnv("BEGET_TOKEN", ""),
		BegetRegion:       getEnv("BEGET_REGION", "ru1"),
		BegetSoftwareSlug: getEnv("BEGET_SOFTWARE_SLUG", "ubuntu-24-04"),

		BegetAPIBaseURL:    getEnv("BEGET_API_BASE_URL", "https://api.beget.com"),
		BegetReaderEnabled: getEnv("BEGET_READER_ENABLED", "") == "true",
		BegetReaderProject: getEnv("BEGET_READER_PROJECT", "internal"),

		AgentSSHPrivateKey: getEnv("AGENT_SSH_PRIVATE_KEY", ""),
		AgentSSHPublicKey:  getEnv("AGENT_SSH_PUBLIC_KEY", ""),

		TFWorkspaceBase: getEnv("TF_WORKSPACE_BASE", "/var/lib/tf-workspaces"),
		TFStateConnStr:  getEnv("TF_STATE_CONN_STR", ""),
		TFBinPath:       getEnv("TF_BIN_PATH", "/usr/local/bin/terraform"),

		GitopsRepoURL:       getEnv("GITOPS_REPO_URL", ""),
		GitopsBranch:        getEnv("GITOPS_BRANCH", "main"),
		GitopsUsername:      getEnv("GITOPS_USERNAME", ""),
		GitopsToken:         getEnv("GITOPS_TOKEN", ""),
		GitopsRepoLocalPath: getEnv("GITOPS_REPO_LOCAL_PATH", "/var/lib/gitops-repos"),
		GitopsBotName:       getEnv("GITOPS_BOT_NAME", "DADA Platform Bot"),
		GitopsBotEmail:      getEnv("GITOPS_BOT_EMAIL", "bot@dada-tuda.ru"),

		PrometheusRemoteWriteURL:  getEnv("PROMETHEUS_REMOTE_WRITE_URL", ""),
		PrometheusRemoteWriteUser: getEnv("PROMETHEUS_REMOTE_WRITE_USER", ""),
		PrometheusRemoteWritePass: getEnv("PROMETHEUS_REMOTE_WRITE_PASS", ""),

		ElasticsearchURL:    getEnv("ELASTICSEARCH_URL", ""),
		ElasticsearchAPIKey: getEnv("ELASTICSEARCH_API_KEY", ""),

		DevMode: getEnv("DEV_MODE", "") == "true",
	}

	// Edge endpoints must advertise a PUBLIC, externally-resolvable Portainer
	// address. Fall back to the in-cluster PortainerURL only if not set (works
	// for in-cluster edge agents, NOT external VMs).
	if c.PortainerEdgeURL == "" {
		c.PortainerEdgeURL = c.PortainerURL
	}

	var err error
	c.PollIntervalDB, err = parseDuration("VM_POLL_INTERVAL_DB", "5s")
	if err != nil {
		return nil, err
	}
	c.PollIntervalStatus, err = parseDuration("VM_POLL_INTERVAL_STATUS", "30s")
	if err != nil {
		return nil, err
	}
	c.AgentConnectTimeout, err = parseDuration("AGENT_CONNECT_TIMEOUT", "10m")
	if err != nil {
		return nil, err
	}
	c.PollIntervalReader, err = parseDuration("BEGET_READER_INTERVAL", "1h")
	if err != nil {
		return nil, err
	}
	c.BegetReaderGrace, err = parseDuration("BEGET_READER_GRACE", "15m")
	if err != nil {
		return nil, err
	}

	return c, nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key, def string) (time.Duration, error) {
	s := getEnv(key, def)
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s=%q: %w", key, s, err)
	}
	return d, nil
}
