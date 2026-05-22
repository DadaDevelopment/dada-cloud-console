package config

import (
	"fmt"
	"os"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	DBURL       string
	JWTSecret   string
	Port        string
	LogLevel    string
	DevMode     bool
	ClusterLBIP string

	// AI Studio (v2). MLflowBaseURL empty disables the registry browser
	// (the wizard falls back to "paste artifactURI"). AIStudioEnabled is
	// the master feature flag; routes are hidden when false.
	AIStudioEnabled  bool
	MLflowBaseURL    string
	MLflowAuthHeader string // optional, forwarded as-is on every request
}

// Load reads configuration from environment variables.
// Returns an error if any required variable is missing.
func Load() (*Config, error) {
	cfg := &Config{
		DBURL:            getEnv("DB_URL", getEnv("DATABASE_URL", "")),
		JWTSecret:        getEnv("JWT_SECRET", ""),
		Port:             getEnv("PORT", getEnv("HTTP_PORT", "8080")),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		DevMode:          getEnv("DEV_MODE", "false") == "true",
		ClusterLBIP:      getEnv("CLUSTER_LB_IP", "93.189.231.60"),
		AIStudioEnabled:  getEnv("AI_STUDIO_ENABLED", "false") == "true",
		MLflowBaseURL:    getEnv("MLFLOW_BASE_URL", ""),
		MLflowAuthHeader: getEnv("MLFLOW_AUTH_HEADER", ""),
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
