package models

import (
	"time"

	"github.com/google/uuid"
)

// MonitoringApp is a project resource kind (alongside DB/VM/App) that acts as an
// ingest target for externally pushed metrics and logs. See ADR-011.
type MonitoringApp struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	EnvironmentID uuid.UUID `json:"environment_id"`
	Name          string    `json:"name"`
	// Ingestion (write path). APIKeyPrefix is the short displayable prefix of the
	// scoped key (the plaintext is shown once at creation, never persisted).
	// Scopes are the key's granted scopes (e.g. metrics:write, logs:write).
	APIKeyPrefix string   `json:"api_key_prefix,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	// Environment is the resolved environment name (populated by list joins).
	Environment string `json:"environment,omitempty"`
	// Read/alert layer (Grafana provisioning handles).
	GrafanaFolderUID    string    `json:"grafana_folder_uid,omitempty"`
	GrafanaDashboardUID string    `json:"grafana_dashboard_uid,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// HealthConfig holds per-resource, tunable thresholds for health computation.
// Zero values mean "use the code default" (see defaults in monitoring_health.go).
type HealthConfig struct {
	DownAfterSeconds  int `json:"down_after_seconds,omitempty"`
	ErrorThreshold15m int `json:"error_threshold_15m,omitempty"`
}

// HealthState is the coarse liveness verdict for a monitoring resource.
type HealthState string

const (
	HealthHealthy  HealthState = "healthy"
	HealthDegraded HealthState = "degraded"
	HealthDown     HealthState = "down"
	HealthUnknown  HealthState = "unknown"
)

// HealthStatus is the computed health of a monitoring resource. `critical` is an
// orthogonal flag raised by a firing Grafana alert; it can co-occur with any state.
type HealthStatus struct {
	State        HealthState `json:"state"`
	Critical     bool        `json:"critical"`
	LastSeen     *time.Time  `json:"last_seen"`
	ErrorRate15m int         `json:"error_rate_15m"` // ERROR-level log count over last 15m
	FiringAlerts int         `json:"firing_alerts"`
	Reasons      []string    `json:"reasons"`
}

// MonitoringChannel mirrors a native Grafana contact point (Telegram/Email/Webhook).
type MonitoringChannel struct {
	ID                     uuid.UUID `json:"id"`
	ProjectID              uuid.UUID `json:"project_id"`
	Name                   string    `json:"name"`
	Type                   string    `json:"type"`
	GrafanaContactpointUID string    `json:"grafana_contactpoint_uid,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

// MonitoringAlertRule mirrors a Grafana alert rule for the native list UI.
type MonitoringAlertRule struct {
	ID              uuid.UUID  `json:"id"`
	MonitoringAppID uuid.UUID  `json:"monitoring_app_id"`
	ChannelID       *uuid.UUID `json:"channel_id,omitempty"`
	ChannelName     string     `json:"channel_name,omitempty"`
	Name            string     `json:"name"`
	Metric          string     `json:"metric"`
	Condition       string     `json:"condition"`
	Threshold       float64    `json:"threshold"`
	Duration        string     `json:"duration"`
	Enabled         bool       `json:"enabled"`
	GrafanaRuleUID  string     `json:"grafana_rule_uid,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
