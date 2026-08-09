package renderer_test

import (
	"testing"

	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"gopkg.in/yaml.v3"
)

// "@daily" is the shorthand a cron schedule is normally written in, and "@" is
// a reserved indicator in YAML: unquoted, it made the rendered manifest
// unparseable and the CreateServiceDatabase operation failed with
// "found character that cannot start any token" instead of creating a database.
func TestRenderServiceDatabaseQuotesCronShorthand(t *testing.T) {
	out, err := renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:            "myapp-db",
		Namespace:       "alpha-prod",
		ProjectSlug:     "alpha",
		EnvSlug:         "prod",
		Database:        "myapp_db",
		BackupEnabled:   true,
		BackupSchedule:  "@daily",
		BackupRetention: "7d",
		OperationID:     "op-1",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var manifest struct {
		Spec struct {
			Backup struct {
				Frequency string `yaml:"frequency"`
				Retention string `yaml:"retention"`
			} `yaml:"backup"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("rendered manifest does not parse: %v\n%s", err, out)
	}
	if manifest.Spec.Backup.Frequency != "@daily" {
		t.Errorf("frequency = %q, want @daily", manifest.Spec.Backup.Frequency)
	}
	if manifest.Spec.Backup.Retention != "7d" {
		t.Errorf("retention = %q, want 7d", manifest.Spec.Backup.Retention)
	}
}

// An empty schedule must stay an empty string rather than becoming the literal
// characters that a bare template value would leave behind.
func TestRenderServiceDatabaseKeepsAnEmptyScheduleEmpty(t *testing.T) {
	out, err := renderer.RenderServiceDatabase(renderer.ServiceDatabaseSpec{
		Name:        "myapp-db",
		Namespace:   "alpha-prod",
		ProjectSlug: "alpha",
		EnvSlug:     "prod",
		Database:    "myapp_db",
		OperationID: "op-2",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var manifest map[string]any
	if err := yaml.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("rendered manifest does not parse: %v\n%s", err, out)
	}
}
