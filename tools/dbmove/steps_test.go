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

func TestCopySecretsInvokesGetAndApplyPerSecret(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	fr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get secret n8n-runtime"] =
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-runtime\ndata:\n  encryptionKey: eA==\n"
	fr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get secret n8n-smtp"] =
		"apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-smtp\ndata:\n  x: eA==\n"
	s := &copySecretsStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var gets, applies int
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "get secret n8n-") {
			gets++
		}
		if strings.Contains(j, "apply") {
			applies++
		}
	}
	if gets != 2 || applies != 2 {
		t.Fatalf("want 2 gets + 2 applies, got %d/%d (%v)", gets, applies, fr.calls)
	}
}

func TestRestampSecretNamespaceRewritesNS(t *testing.T) {
	raw := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: n8n-runtime\n  namespace: example-project-prod\n  uid: abc\n  resourceVersion: \"9\"\ndata:\n  encryptionKey: eA==\n"
	got := restampSecretNamespace(raw, "platform-prod")
	if !strings.Contains(got, "namespace: platform-prod") {
		t.Fatalf("ns not rewritten:\n%s", got)
	}
	if strings.Contains(got, "uid:") || strings.Contains(got, "resourceVersion:") {
		t.Fatalf("server fields not stripped:\n%s", got)
	}
	if !strings.Contains(got, "encryptionKey: eA==") {
		t.Fatalf("data lost:\n%s", got)
	}
}
