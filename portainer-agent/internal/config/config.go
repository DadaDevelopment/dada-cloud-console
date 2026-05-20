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

	BegetToken      string
	BegetRegion     string
	BegetSoftwareID string
	BegetSSHKeyID   string

	// AgentSSHPrivateKey is the PEM-encoded private key matching BegetSSHKeyID public key.
	AgentSSHPrivateKey string

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

	DevMode bool
}

// Load reads env vars and returns a Config or a descriptive error.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:       getEnv("DATABASE_URL", ""),
		PortainerURL:      getEnv("PORTAINER_URL", ""),
		PortainerAPIToken: getEnv("PORTAINER_API_TOKEN", ""),

		BegetToken:      getEnv("BEGET_TOKEN", ""),
		BegetRegion:     getEnv("BEGET_REGION", "ru1"),
		BegetSoftwareID: getEnv("BEGET_SOFTWARE_ID", ""),
		BegetSSHKeyID:   getEnv("BEGET_SSH_KEY_ID", ""),

		AgentSSHPrivateKey: getEnv("AGENT_SSH_PRIVATE_KEY", ""),

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
