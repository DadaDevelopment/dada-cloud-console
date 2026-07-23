package main

import "testing"

func TestLoadConfigDerivesTargetNamespace(t *testing.T) {
	cfg, err := LoadConfig("configs/telemost-bot.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.TargetNamespace != "internal-prod" {
		t.Fatalf("target ns = %q, want internal-prod", cfg.TargetNamespace)
	}
	if len(cfg.Volumes) != 0 {
		t.Fatalf("telemost should have no volumes, got %d", len(cfg.Volumes))
	}
	if len(cfg.OOBSecrets) != 2 {
		t.Fatalf("want 2 oob secrets, got %d", len(cfg.OOBSecrets))
	}
}

func TestLoadConfigN8nHasTwoVolumes(t *testing.T) {
	cfg, err := LoadConfig("configs/n8n.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Volumes) != 2 || cfg.Volumes[0].PVCName != "n8n-data" {
		t.Fatalf("n8n volumes wrong: %+v", cfg.Volumes)
	}
	if cfg.TargetNamespace != "platform-prod" {
		t.Fatalf("target ns = %q", cfg.TargetNamespace)
	}
}

func TestLoadConfigRejectsMissingApp(t *testing.T) {
	if _, err := loadConfigBytes([]byte("srcProject: x")); err == nil {
		t.Fatal("expected error for missing app")
	}
}
