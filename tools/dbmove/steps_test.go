package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// seqRunner returns a sequence of canned outputs per command prefix, advancing
// one step each matching call (clamped at the last), so a test can model a value
// that changes across polls (e.g. backupvolume.lastBackupAt advancing). Keys not
// in seq fall back to the out map, like fakeRunner.
type seqRunner struct {
	calls [][]string
	seq   map[string][]string
	idx   map[string]int
	out   map[string]string
}

func newSeqRunner() *seqRunner {
	return &seqRunner{seq: map[string][]string{}, idx: map[string]int{}, out: map[string]string{}}
}

func (s *seqRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	s.calls = append(s.calls, call)
	joined := strings.Join(call, " ")
	for k, vs := range s.seq {
		if strings.HasPrefix(joined, k) {
			i := min(s.idx[k], len(vs)-1)
			s.idx[k]++
			return vs[i], nil
		}
	}
	for k, v := range s.out {
		if strings.HasPrefix(joined, k) {
			return v, nil
		}
	}
	return "", nil
}

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
	for _, want := range []string{"kind: Volume", "accessMode: rwx", "frontend: blockdev", "backup=backup-abc", "volume=pvc-src-123", "n8n-n8n-data-moved"} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore Volume CR missing %q:\n%s", want, y)
		}
	}
}

func TestRestorePVYAMLClaimRefBindsTargetPVC(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	y := restorePVYAML(cfg, VolumeSpec{PVCName: "n8n-data"}, "2147483648")
	for _, want := range []string{
		"kind: PersistentVolume",
		"name: n8n-n8n-data-moved-pv",
		"persistentVolumeReclaimPolicy: Retain",
		"ReadWriteMany",
		"claimRef:",
		"namespace: platform-prod",
		"name: n8n-data",
		"volumeHandle: n8n-n8n-data-moved",
	} {
		if !strings.Contains(y, want) {
			t.Fatalf("restore PV missing %q:\n%s", want, y)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(y), "apiVersion: v1\nkind: PersistentVolume\n") {
		t.Fatalf("top-level object must be a PersistentVolume, not a PVC (the relocated chart owns the PVC):\n%s", y)
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
	s := &volumeCopyStep{cfg: cfg, vol: VolumeSpec{PVCName: "n8n-data", HasData: true}}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run err: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry-run must not call kubectl, got %v", fr.calls)
	}
}

func TestVolumeCopySkipsVolumesWithoutData(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	s := &volumeCopyStep{cfg: cfg, vol: VolumeSpec{PVCName: "n8n-data", HasData: false}}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("run err: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("hasData=false volume must be skipped with zero cluster calls, got %v", fr.calls)
	}
}

func TestVolumeCopyLooksUpSizeFromSourcePVC(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	vol := VolumeSpec{PVCName: "n8n-data", HasData: true}
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

func TestLonghornBackupSnapshotAndBackupYAML(t *testing.T) {
	snap := snapshotYAML("pvc-abc", "dbmove-n8n-n8n-data")
	for _, want := range []string{"kind: Snapshot", "volume: pvc-abc", "createSnapshot: true", "name: dbmove-n8n-n8n-data"} {
		if !strings.Contains(snap, want) {
			t.Fatalf("snapshot yaml missing %q:\n%s", want, snap)
		}
	}
	bak := backupYAML("dbmove-n8n-n8n-data", "dbmove-n8n-n8n-data")
	for _, want := range []string{"kind: Backup", "snapshotName: dbmove-n8n-n8n-data"} {
		if !strings.Contains(bak, want) {
			t.Fatalf("backup yaml missing %q:\n%s", want, bak)
		}
	}
}

func TestLonghornBackupDryRunNoCalls(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	fr := newFakeRunner()
	s := &longhornBackupStep{cfg: cfg}
	if err := s.Run(context.Background(), fr, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry-run must make zero calls, got: %v", fr.calls)
	}
}

func TestWaitBackupAdvancedDetectsNewTimestamp(t *testing.T) {
	fr := newFakeRunner()
	fr.out["kubectl --context ctx -n longhorn-system get backupvolume pvc-x -o jsonpath={.status.lastBackupAt}"] = "2026-07-25T10:00:00Z"
	if err := waitBackupAdvanced(context.Background(), fr, "ctx", "pvc-x", "2026-07-25T09:00:00Z", time.Minute); err != nil {
		t.Fatalf("advanced timestamp should satisfy: %v", err)
	}
}

func TestWaitBackupAdvancedTimesOutWhenStale(t *testing.T) {
	fr := newFakeRunner()
	fr.out["kubectl --context ctx -n longhorn-system get backupvolume pvc-x -o jsonpath={.status.lastBackupAt}"] = "2026-07-25T09:00:00Z"
	err := waitBackupAdvanced(context.Background(), fr, "ctx", "pvc-x", "2026-07-25T09:00:00Z", time.Nanosecond)
	if err == nil {
		t.Fatal("stale (unchanged) lastBackupAt must not be accepted as success")
	}
}

func TestWaitSnapshotReady(t *testing.T) {
	fr := newFakeRunner()
	fr.out["kubectl --context ctx -n longhorn-system get snapshot snap-1 -o jsonpath={.status.readyToUse}"] = "true"
	if err := waitSnapshotReady(context.Background(), fr, "ctx", "snap-1", time.Minute); err != nil {
		t.Fatalf("readyToUse=true should satisfy: %v", err)
	}
	frNever := newFakeRunner()
	if err := waitSnapshotReady(context.Background(), frNever, "ctx", "snap-1", time.Nanosecond); err == nil {
		t.Fatal("never-ready snapshot must time out")
	}
}

func TestLonghornBackupJudgesOnBackupVolumeSignal(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	sr := newSeqRunner()
	sr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get pvc n8n-worker-data -o jsonpath={.spec.volumeName}"] = "pvc-w"
	sr.out["kubectl --context 83.222.27.62:26443 -n longhorn-system get snapshot dbmove-n8n-n8n-worker-data -o jsonpath={.status.readyToUse}"] = "true"
	sr.seq["kubectl --context 83.222.27.62:26443 -n longhorn-system get backupvolume pvc-w -o jsonpath={.status.lastBackupAt}"] = []string{"T1", "T2"}
	s := &longhornBackupStep{cfg: cfg}
	if err := s.Run(context.Background(), sr, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawLastBackupAt, sawSnapReady, sawDataPVC bool
	for _, c := range sr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "get backupvolume pvc-w") && strings.Contains(j, "status.lastBackupAt") {
			sawLastBackupAt = true
		}
		if strings.Contains(j, "get snapshot dbmove-n8n-n8n-worker-data") && strings.Contains(j, "readyToUse") {
			sawSnapReady = true
		}
		if strings.Contains(j, "status.state") {
			t.Fatalf("must not judge success on Backup.status.state (races backupvolume): %v", c)
		}
		if strings.Contains(j, "get pvc n8n-data ") {
			sawDataPVC = true
		}
	}
	if !sawLastBackupAt {
		t.Fatalf("gap #1: success must be judged on backupvolume.lastBackupAt, never queried it: %v", sr.calls)
	}
	if !sawSnapReady {
		t.Fatalf("gap #1: must wait for snapshot readyToUse before backing up: %v", sr.calls)
	}
	if sawDataPVC {
		t.Fatalf("hasData=false n8n-data must be skipped, but it was snapshotted: %v", sr.calls)
	}
}

func TestInjectPVCVolumeNameAfterStorageClass(t *testing.T) {
	in := strings.Join([]string{
		"kind: PersistentVolumeClaim",
		"spec:",
		"  accessModes:",
		"    - ReadWriteMany",
		"  storageClassName: longhorn-prod",
		"  resources:",
		"    requests:",
		"      storage: 5Gi",
		"",
	}, "\n")
	got := injectPVCVolumeName(in, "n8n-n8n-worker-data-moved-pv")
	if !strings.Contains(got, "  storageClassName: longhorn-prod\n  volumeName: n8n-n8n-worker-data-moved-pv\n") {
		t.Fatalf("volumeName not injected after storageClassName:\n%s", got)
	}
	again := injectPVCVolumeName(got, "n8n-n8n-worker-data-moved-pv")
	if strings.Count(again, "volumeName:") != 1 {
		t.Fatalf("inject must be idempotent, got %d volumeName lines:\n%s", strings.Count(again, "volumeName:"), again)
	}
}

func TestApplyFolderLiteralEditsRewritesAppYAMLPathAndVolumes(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	dir := t.TempDir()
	appYAML := "spec:\n  source:\n    path: clusters/beget-prod/projects/example-project/environments/prod/apps/n8n/chart\n"
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(appYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	rv := "common:\n  namespace: example-project-prod\n  labels:\n    dada.io/project: example-project\n"
	if err := os.WriteFile(filepath.Join(dir, "resources.values.yaml"), []byte(rv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "chart", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	pvc := "spec:\n  accessModes:\n    - ReadWriteOnce\n  storageClassName: longhorn-prod\n  resources:\n    requests:\n      storage: 5Gi\n"
	if err := os.WriteFile(filepath.Join(dir, "chart", "templates", "worker.yaml"), []byte(pvc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chart", "templates", "deployment.yaml"), []byte(pvc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyFolderLiteralEdits(cfg, dir); err != nil {
		t.Fatalf("edits: %v", err)
	}
	gotApp, _ := os.ReadFile(filepath.Join(dir, "app.yaml"))
	if !strings.Contains(string(gotApp), "projects/platform/environments/prod/apps/n8n/chart") {
		t.Fatalf("gap #3: app.yaml spec.helm.path not rewritten to target:\n%s", gotApp)
	}
	if strings.Contains(string(gotApp), "projects/example-project/environments/prod") {
		t.Fatalf("gap #3: stale source path still in app.yaml:\n%s", gotApp)
	}
	gotRV, _ := os.ReadFile(filepath.Join(dir, "resources.values.yaml"))
	if !strings.Contains(string(gotRV), "namespace: platform-prod") || !strings.Contains(string(gotRV), "dada.io/project: platform") {
		t.Fatalf("resources.values.yaml literals not rewritten:\n%s", gotRV)
	}
	gotWorker, _ := os.ReadFile(filepath.Join(dir, "chart", "templates", "worker.yaml"))
	if !strings.Contains(string(gotWorker), "- ReadWriteMany") {
		t.Fatalf("worker PVC not RWX:\n%s", gotWorker)
	}
	if !strings.Contains(string(gotWorker), "volumeName: n8n-n8n-worker-data-moved-pv") {
		t.Fatalf("gap #5: data volume worker PVC missing injected volumeName:\n%s", gotWorker)
	}
	gotDeploy, _ := os.ReadFile(filepath.Join(dir, "chart", "templates", "deployment.yaml"))
	if !strings.Contains(string(gotDeploy), "- ReadWriteMany") {
		t.Fatalf("deployment PVC not RWX:\n%s", gotDeploy)
	}
	if strings.Contains(string(gotDeploy), "volumeName:") {
		t.Fatalf("hasData=false n8n-data PVC must NOT get a volumeName (chart provisions fresh):\n%s", gotDeploy)
	}
}

func TestDBCredsPatchFromSecretJSON(t *testing.T) {
	raw := `{"apiVersion":"v1","kind":"Secret","data":{"password":"cGFzcw==","endpoint":"aG9zdA=="}}`
	patch, err := dbCredsPatchFromSecretJSON(raw)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	var doc struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(patch), &doc); err != nil {
		t.Fatalf("patch not valid json: %v\n%s", err, patch)
	}
	if doc.Data["password"] != "cGFzcw==" || doc.Data["endpoint"] != "aG9zdA==" {
		t.Fatalf("patch dropped data values: %v", doc.Data)
	}
	if _, err := dbCredsPatchFromSecretJSON(`{"data":{}}`); err == nil {
		t.Fatal("empty data must error (nothing to recover)")
	}
}

func TestCaptureAndRepatchDBCredsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DBMOVE_STATE_DIR", dir)
	cfg, _ := LoadConfig("configs/n8n.yaml")

	frCap := newFakeRunner()
	frCap.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get secret n8n-db-credentials -o json"] =
		`{"data":{"password":"cGFzcw==","endpoint":"aG9zdA==","port":"NTQzMg==","username":"dQ=="}}`
	if err := (&captureDBCredsStep{cfg: cfg}).Run(context.Background(), frCap, false); err != nil {
		t.Fatalf("capture: %v", err)
	}
	saved, err := os.ReadFile(dbCredsStatePath(cfg))
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	if !strings.Contains(string(saved), "cGFzcw==") {
		t.Fatalf("captured state missing password:\n%s", saved)
	}

	frRep := newFakeRunner()
	frRep.out["kubectl --context 83.222.27.62:26443 -n platform-prod get secret n8n-db-credentials -o jsonpath={.metadata.name}"] = "n8n-db-credentials"
	if err := (&repatchDBCredsStep{cfg: cfg}).Run(context.Background(), frRep, false); err != nil {
		t.Fatalf("repatch: %v", err)
	}
	var patched, restarts int
	for _, c := range frRep.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "patch secret n8n-db-credentials") && strings.Contains(j, "--type merge") {
			patched++
		}
		if strings.Contains(j, "rollout restart deploy") {
			restarts++
		}
	}
	if patched != 1 {
		t.Fatalf("want exactly 1 merge patch of the target secret, got %d (%v)", patched, frRep.calls)
	}
	if restarts != 3 {
		t.Fatalf("want 3 workload restarts (n8n, n8n-runners, n8n-worker), got %d", restarts)
	}
}

func TestVerifyRowCountSQLScopesToSchema(t *testing.T) {
	if got := verifyRowCountSQL(""); strings.Contains(got, "where schemaname") {
		t.Fatalf("empty schema must not scope: %s", got)
	}
	got := verifyRowCountSQL("n8n")
	if !strings.Contains(got, "where schemaname = 'n8n'") {
		t.Fatalf("gap #6: schema not applied: %s", got)
	}
}

func TestReclaimInertUntilConfirmed(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	t.Setenv("DBMOVE_STATE_DIR", t.TempDir())
	for _, tc := range []struct {
		name    string
		dryRun  bool
		confirm bool
	}{
		{"dry-run+confirm", true, true},
		{"execute-without-confirm", false, false},
	} {
		fr := newFakeRunner()
		s := &reclaimStep{cfg: cfg, confirmReclaim: tc.confirm}
		if err := s.Run(context.Background(), fr, tc.dryRun); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, c := range fr.calls {
			if strings.Contains(strings.Join(c, " "), "delete") {
				t.Fatalf("%s: reclaim must not delete anything, got %v", tc.name, c)
			}
		}
	}
}

func TestReclaimDeletesSourceWhenConfirmed(t *testing.T) {
	cfg, _ := LoadConfig("configs/n8n.yaml")
	t.Setenv("DBMOVE_STATE_DIR", t.TempDir())
	fr := newFakeRunner()
	fr.out["kubectl --context 83.222.27.62:26443 -n example-project-prod get pvc n8n-worker-data -o jsonpath={.spec.volumeName}"] = "pvc-w"
	s := &reclaimStep{cfg: cfg, confirmReclaim: true}
	if err := s.Run(context.Background(), fr, false); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	var delPVC, delPV, delVol, touchedData bool
	for _, c := range fr.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "delete pvc n8n-worker-data") {
			delPVC = true
		}
		if strings.Contains(j, "delete pv pvc-w") {
			delPV = true
		}
		if strings.Contains(j, "-n longhorn-system delete volume pvc-w") {
			delVol = true
		}
		if strings.Contains(j, "n8n-data") {
			touchedData = true
		}
	}
	if !delPVC || !delPV || !delVol {
		t.Fatalf("reclaim must delete source PVC+PV+Longhorn volume, got pvc=%v pv=%v vol=%v (%v)", delPVC, delPV, delVol, fr.calls)
	}
	if touchedData {
		t.Fatalf("hasData=false n8n-data must be left alone by reclaim: %v", fr.calls)
	}
}
