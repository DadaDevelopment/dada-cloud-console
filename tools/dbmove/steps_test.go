package main

import (
	"context"
	"strings"
	"testing"
)

func TestSafetyDumpDryRunDoesNotRun(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	fr := newFakeRunner()
	s := &safetyDumpStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run err: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry-run must not call kubectl, got %v", fr.calls)
	}
}

func TestBackupActionSetTargetsSharedPostgres(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	y := backupActionSetYAML(cfg, "db-move-telemostbot")
	for _, want := range []string{"kind: ActionSet", "blueprint: postgres-logical-db-blueprint", "database: telemostbot", "kind: StatefulSet", "name: postgresql", "namespace: databases"} {
		if !strings.Contains(y, want) {
			t.Fatalf("actionset yaml missing %q:\n%s", want, y)
		}
	}
}
