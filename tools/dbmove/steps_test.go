package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	got, err := restampSecretNamespace(raw, "platform-prod")
	if err != nil {
		t.Fatalf("restamp: %v", err)
	}
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

func TestRestampSecretNamespacePreservesDataKeysNamedLikeMetadata(t *testing.T) {
	raw := "apiVersion: v1\n" +
		"kind: Secret\n" +
		"metadata:\n" +
		"  name: tricky\n" +
		"  namespace: example-project-prod\n" +
		"  uid: abc-123\n" +
		"  resourceVersion: \"42\"\n" +
		"  creationTimestamp: \"2024-01-01T00:00:00Z\"\n" +
		"data:\n" +
		"  namespace: ZXhhbXBsZS1wcm9qZWN0LXByb2Q=\n" +
		"  status: b2s=\n" +
		"status: {}\n"
	got, err := restampSecretNamespace(raw, "platform-prod")
	if err != nil {
		t.Fatalf("restamp: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("output not valid yaml: %v\n%s", err, got)
	}
	meta, _ := doc["metadata"].(map[string]any)
	if meta == nil {
		t.Fatalf("metadata missing:\n%s", got)
	}
	if meta["namespace"] != "platform-prod" {
		t.Fatalf("metadata.namespace = %v, want platform-prod", meta["namespace"])
	}
	if _, ok := meta["uid"]; ok {
		t.Fatalf("metadata.uid not stripped:\n%s", got)
	}
	if _, ok := doc["status"]; ok {
		t.Fatalf("top-level status not stripped:\n%s", got)
	}
	data, _ := doc["data"].(map[string]any)
	if data == nil {
		t.Fatalf("data missing:\n%s", got)
	}
	if data["namespace"] != "ZXhhbXBsZS1wcm9qZWN0LXByb2Q=" {
		t.Fatalf("data.namespace corrupted: %v", data["namespace"])
	}
	if data["status"] != "b2s=" {
		t.Fatalf("data.status corrupted: %v", data["status"])
	}
}

func TestDestFolderRel(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	got := destFolderRel(cfg)
	want := "clusters/beget-prod/projects/platform/environments/prod/apps/n8n"
	if got != want {
		t.Fatalf("dest folder = %q, want %q", got, want)
	}
}

func TestFolderMoveDryRunNoGit(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	fr := newFakeRunner()
	s := &folderMoveStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "git mv") {
			t.Fatalf("dry-run must not git mv")
		}
	}
}

func TestRestoreVolumeYAMLIsRWXFromBackup(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	y := restoreVolumeYAML(cfg, VolumeSpec{PVCName: "n8n-data"}, "pvc-src-123", "backup-abc", "2147483648")
	for _, want := range []string{"kind: Volume", "accessMode: rwx", "backup=backup-abc", "volume=pvc-src-123", "n8n-n8n-data-moved"} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore Volume CR missing %q:\n%s", want, y)
		}
	}
}

func TestRestorePVCYAMLIsRWXInTargetNS(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	y := restorePVCYAML(cfg, VolumeSpec{PVCName: "n8n-data"}, "2147483648")
	for _, want := range []string{"kind: PersistentVolumeClaim", "namespace: platform-prod", "name: n8n-data", "ReadWriteMany", "volumeName: n8n-n8n-data-moved-pv"} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore PVC missing %q:\n%s", want, y)
		}
	}
}

func TestScaleDownTargetsAllDeployments(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	s := &scaleDownStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var scaled int
	for _, c := range fr.calls {
		if strings.Contains(strings.Join(c, " "), "scale deploy") && strings.Contains(strings.Join(c, " "), "--replicas=0") {
			scaled++
		}
	}
	if scaled != 3 {
		t.Fatalf("want 3 scale-to-0 calls, got %d", scaled)
	}
}

func TestVerifyProbeUsesTargetCreds(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	got := psqlProbeArgs(cfg, "select 1")
	j := strings.Join(got, " ")
	for _, want := range []string{"--context 83.222.27.62:26443", "-n internal-prod", "run", "dbmove-probe", "select 1"} {
		if !strings.Contains(j, want) {
			t.Fatalf("probe args missing %q: %v", want, got)
		}
	}
}

type probeOverridesShape struct {
	APIVersion string `json:"apiVersion"`
	Spec       struct {
		Containers []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
			Env   []struct {
				Name      string `json:"name"`
				ValueFrom struct {
					SecretKeyRef struct {
						Name string `json:"name"`
						Key  string `json:"key"`
					} `json:"secretKeyRef"`
				} `json:"valueFrom"`
			} `json:"env"`
			EnvFrom []struct {
				SecretRef struct {
					Name string `json:"name"`
				} `json:"secretRef"`
			} `json:"envFrom"`
		} `json:"containers"`
	} `json:"spec"`
}

func TestPsqlProbeOverridesMapsSecretKeysToPGEnv(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	raw := psqlProbeOverrides(cfg)
	var shape probeOverridesShape
	if err := json.Unmarshal([]byte(raw), &shape); err != nil {
		t.Fatalf("overrides not valid JSON: %v\n%s", err, raw)
	}
	if len(shape.Spec.Containers) != 1 {
		t.Fatalf("want 1 container, got %d:\n%s", len(shape.Spec.Containers), raw)
	}
	c := shape.Spec.Containers[0]
	if len(c.EnvFrom) != 0 {
		t.Fatalf("envFrom must not be used (secret keys are lowercase, PG* never populated):\n%s", raw)
	}
	want := map[string]string{
		"PGHOST":     "endpoint",
		"PGPORT":     "port",
		"PGUSER":     "username",
		"PGPASSWORD": "password",
	}
	got := map[string]string{}
	for _, e := range c.Env {
		got[e.Name] = e.ValueFrom.SecretKeyRef.Key
		if e.ValueFrom.SecretKeyRef.Name != cfg.DBCredSecret {
			t.Fatalf("env %s secretKeyRef.name = %q, want %q", e.Name, e.ValueFrom.SecretKeyRef.Name, cfg.DBCredSecret)
		}
	}
	for envName, secretKey := range want {
		if got[envName] != secretKey {
			t.Fatalf("env %s <- secret key %q, want %q (full: %v)\n%s", envName, got[envName], secretKey, got, raw)
		}
	}
}

func TestVolumeCopyDryRunMakesNoClusterCalls(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	s := &volumeCopyStep{cfg: cfg, vol: cfg.Volumes[0]}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run err: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry-run must not call kubectl, got %v", fr.calls)
	}
}

func TestVolumeCopyLooksUpSizeFromSourcePVC(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	vol := cfg.Volumes[0]
	pvNameKey := "kubectl --context 83.222.27.62:26443 -n example-project-prod get pvc n8n-data -o jsonpath={.spec.volumeName}"
	sizeKey := "kubectl --context 83.222.27.62:26443 -n example-project-prod get pvc n8n-data -o jsonpath={.spec.resources.requests.storage}"
	backupKey := "kubectl --context 83.222.27.62:26443 -n longhorn-system get backupvolume pvc-src-123 -o jsonpath={.status.lastBackupName}"
	healthyKey := "kubectl --context 83.222.27.62:26443 -n longhorn-system get volume n8n-n8n-data-moved -o jsonpath={.status.robustness}"
	fr.out[pvNameKey] = "pvc-src-123"
	fr.out[sizeKey] = "5Gi"
	fr.out[backupKey] = "backup-abc"
	fr.out[healthyKey] = "healthy"
	s := &volumeCopyStep{cfg: cfg, vol: vol}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawSizeLookup bool
	for _, c := range fr.calls {
		if strings.Join(c, " ") == sizeKey {
			sawSizeLookup = true
		}
	}
	if !sawSizeLookup {
		t.Fatalf("Run did not query the source PVC's requested storage size, got calls: %v", fr.calls)
	}
}

func TestScaleDownErrorIncludesCommandOutput(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	key := "kubectl --context 83.222.27.62:26443 -n example-project-prod scale deploy n8n --replicas=0"
	fr.out[key] = "error: deployments.apps \"n8n\" not found"
	fr.err[key] = errors.New("exit status 1")
	s := &scaleDownStep{cfg: cfg}
	err := s.Run(context.Background(), fr, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error must include command output, got: %v", err)
	}
}

func TestCopySecretsApplyErrorIncludesCommandOutput(t *testing.T) {
	cfg, _ := LoadConfig("configs/telemost-bot.yaml")
	fr := newFakeRunner()
	getKey := "kubectl --context 83.222.27.62:26443 -n example-project-prod get secret telemost-bot-llm-keys"
	fr.out[getKey] = "apiVersion: v1\nkind: Secret\nmetadata:\n  name: telemost-bot-llm-keys\n  namespace: example-project-prod\ndata:\n  key: eA==\n"
	applyKey := "kubectl --context 83.222.27.62:26443 -n internal-prod apply -f -"
	fr.out[applyKey] = "error: unable to recognize"
	fr.err[applyKey] = errors.New("exit status 1")
	s := &copySecretsStep{cfg: cfg}
	err := s.Run(context.Background(), fr, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unable to recognize") {
		t.Fatalf("error must include command output, got: %v", err)
	}
}
