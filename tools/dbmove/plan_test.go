package main

import (
	"strings"
	"testing"
)

func stepIDs(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID()
	}
	return out
}

func TestBuildPlanDBOnly(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	got := strings.Join(stepIDs(BuildPlan(cfg)), ",")
	want := "safety-dump,copy-secrets,capture-db-creds,folder-move,repatch-db-creds,verify,teardown"
	if got != want {
		t.Fatalf("db-only plan = %q, want %q", got, want)
	}
}

func TestBuildPlanWithVolumes(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	got := strings.Join(stepIDs(BuildPlan(cfg)), ",")
	want := "safety-dump,longhorn-backup,scale-down,volume-copy:n8n-data,volume-copy:n8n-worker-data,copy-secrets,capture-db-creds,folder-move,repatch-db-creds,verify,teardown"
	if got != want {
		t.Fatalf("volume plan = %q, want %q", got, want)
	}
}
