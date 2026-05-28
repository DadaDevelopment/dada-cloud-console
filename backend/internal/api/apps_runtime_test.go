package api

import (
	"testing"

	"github.com/dada-tuda/console/backend/internal/models"
)

func TestCanCreateAppsInRuntime(t *testing.T) {
	if !canCreateAppsInRuntime(models.EnvironmentRuntimeK8s) {
		t.Fatal("k8s runtime should allow app creation")
	}
	if canCreateAppsInRuntime(models.EnvironmentRuntimeVM) {
		t.Fatal("vm runtime should stay blocked until the VM app worker path exists")
	}
	if !canCreateAppsInRuntime("") {
		t.Fatal("empty runtime should stay backward-compatible and behave like k8s")
	}
}
