package api

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// --- path validation ---

func TestValidateVolumeCompactPathAcceptsRelative(t *testing.T) {
	got, err := validateVolumeCompactPath("data/raw_data/bodies")
	if err != nil {
		t.Fatalf("validateVolumeCompactPath: %v", err)
	}
	if got != "data/raw_data/bodies" {
		t.Fatalf("got %q, want data/raw_data/bodies", got)
	}
}

func TestValidateVolumeCompactPathStripsReportMountPrefix(t *testing.T) {
	got, err := validateVolumeCompactPath(volumeReportMountPath + "/data/raw_data/bodies")
	if err != nil {
		t.Fatalf("validateVolumeCompactPath: %v", err)
	}
	if got != "data/raw_data/bodies" {
		t.Fatalf("got %q, want the top_dirs absolute form stripped down to data/raw_data/bodies", got)
	}
}

func TestValidateVolumeCompactPathAcceptsSingleSegment(t *testing.T) {
	got, err := validateVolumeCompactPath("logs")
	if err != nil || got != "logs" {
		t.Fatalf("got %q err=%v, want logs", got, err)
	}
}

func TestValidateVolumeCompactPathRejectsEvilInputs(t *testing.T) {
	cases := []string{
		"",
		"/",
		".",
		"..",
		"../etc",
		"data/../../etc",
		"/etc/passwd",
		"data/../secrets",
		"data/..",
		"./data",
		"data/./sub",
		"da$ta",
		"data;rm -rf /",
		"data`whoami`",
		"data\x00",
		"data\nrm -rf /",
		"data/../../../../etc/passwd",
		"data//sub",
		"data sub",
		"data'sub",
		"data\"sub",
		"data|sub",
		"data&sub",
		"data(sub)",
	}
	for _, in := range cases {
		if got, err := validateVolumeCompactPath(in); err == nil {
			t.Errorf("validateVolumeCompactPath(%q) = %q, <nil>, want an error", in, got)
		}
	}
}

func TestValidateVolumeCompactPathRejectsMountRootItself(t *testing.T) {
	if _, err := validateVolumeCompactPath(volumeReportMountPath); err == nil {
		t.Fatal("compacting the bare volume root must be rejected")
	}
	if _, err := validateVolumeCompactPath(volumeReportMountPath + "/"); err == nil {
		t.Fatal("compacting the bare volume root (trailing slash) must be rejected")
	}
}

// --- Job name / idempotency ---

func TestVolumeCompactJobNameIsDeterministic(t *testing.T) {
	a := volumeCompactJobName("fonbet-value", "data/raw_data/bodies")
	b := volumeCompactJobName("fonbet-value", "data/raw_data/bodies")
	if a != b {
		t.Fatalf("volumeCompactJobName is not deterministic: %q != %q", a, b)
	}
}

func TestVolumeCompactJobNameDiffersByPath(t *testing.T) {
	a := volumeCompactJobName("fonbet-value", "data/raw_data/bodies")
	b := volumeCompactJobName("fonbet-value", "data/raw_data/tmp")
	if a == b {
		t.Fatalf("two different paths for the same app produced the same job name %q: a second compact would collide with the first instead of running independently", a)
	}
}

func TestVolumeCompactJobNameDiffersFromReportJobName(t *testing.T) {
	report := volumeReportJobName("fonbet-value")
	compact := volumeCompactJobName("fonbet-value", "data/raw_data/bodies")
	if report == compact {
		t.Fatalf("report and compact job names collided: %q", report)
	}
}

func TestVolumeCompactJobNameIsBounded(t *testing.T) {
	longName := "Fonbet-VALUE-" + strings.Repeat("x", 80)
	name := volumeCompactJobName(longName, "data/raw_data/bodies")
	if len(name) > 63 {
		t.Fatalf("job name %q is %d chars, over the Kubernetes 63-char limit", name, len(name))
	}
	if !strings.HasPrefix(name, volumeCompactJobNamePrefix) {
		t.Fatalf("job name %q lost its prefix under truncation", name)
	}
	if !strings.HasSuffix(name, volumeCompactPathHash("data/raw_data/bodies")) {
		t.Fatalf("job name %q lost its path hash under truncation: the hash must survive, not the app name", name)
	}
}

// --- Job spec ---

func TestVolumeCompactJobSpecShape(t *testing.T) {
	job := volumeCompactJobSpec("artemmendeleev-gmail-com-prod", "fonbet-value", "fonbet-value-pvc", "data/raw_data/bodies")

	if job.Namespace != "artemmendeleev-gmail-com-prod" {
		t.Fatalf("job namespace = %q, want the app's own namespace", job.Namespace)
	}

	pod := job.Spec.Template.Spec
	if len(pod.Volumes) != 1 || pod.Volumes[0].PersistentVolumeClaim == nil {
		t.Fatalf("pod does not mount a PVC volume: %+v", pod.Volumes)
	}
	if pod.Volumes[0].PersistentVolumeClaim.ClaimName != "fonbet-value-pvc" {
		t.Fatalf("claimName = %q, want fonbet-value-pvc", pod.Volumes[0].PersistentVolumeClaim.ClaimName)
	}
	if pod.Volumes[0].PersistentVolumeClaim.ReadOnly {
		t.Fatal("the PVC volume must be read-write: compact deletes the packed originals, unlike the report")
	}

	if len(pod.Containers) != 1 {
		t.Fatalf("want exactly one container, got %d", len(pod.Containers))
	}
	container := pod.Containers[0]
	if len(container.VolumeMounts) != 1 || container.VolumeMounts[0].ReadOnly {
		t.Fatalf("container mount must be read-write: %+v", container.VolumeMounts)
	}
	if container.VolumeMounts[0].MountPath != volumeReportMountPath {
		t.Fatalf("mount path = %q, want %q", container.VolumeMounts[0].MountPath, volumeReportMountPath)
	}

	if len(container.Env) != 1 || container.Env[0].Name != volumeCompactTargetEnvVar {
		t.Fatalf("target path must be carried as the %s env var, not formatted into the script: %+v", volumeCompactTargetEnvVar, container.Env)
	}
	if container.Env[0].Value != "data/raw_data/bodies" {
		t.Fatalf("env value = %q, want data/raw_data/bodies", container.Env[0].Value)
	}

	if container.Resources.Requests.Cpu().IsZero() || container.Resources.Requests.Memory().IsZero() {
		t.Fatal("container has no resource requests: a namespace LimitRange can silently reject this pod at admission")
	}
	if container.Resources.Limits.Cpu().IsZero() || container.Resources.Limits.Memory().IsZero() {
		t.Fatal("container has no resource limits")
	}

	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("restartPolicy = %q, want Never", pod.RestartPolicy)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Fatalf("backoffLimit = %v, want 0: compact must never auto-retry over a directory its own rm may have half-touched", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != volumeCompactActiveDeadlineSeconds {
		t.Fatalf("activeDeadlineSeconds = %v, want %d", job.Spec.ActiveDeadlineSeconds, volumeCompactActiveDeadlineSeconds)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != volumeCompactTTLSecondsAfterFinished {
		t.Fatalf("ttlSecondsAfterFinished = %v, want %d", job.Spec.TTLSecondsAfterFinished, volumeCompactTTLSecondsAfterFinished)
	}

	if job.Labels["dada.io/app"] != "fonbet-value" || job.Labels["dada.io/maintenance"] != "volume-compact" {
		t.Fatalf("job labels = %v, missing dada.io/app or dada.io/maintenance=volume-compact", job.Labels)
	}
}

// --- Ensure idempotency, reusing the generic runner ---

func TestVolumeCompactJobEnsureCreatesOnceForSamePath(t *testing.T) {
	cs := fake.NewSimpleClientset()
	jobs := &clusterVolumeMaintenanceJobs{cs: cs}
	name := volumeCompactJobName("fonbet-value", "data/raw_data/bodies")
	build := func() *batchv1.Job {
		return volumeCompactJobSpec("ns-1", "fonbet-value", "fonbet-value-pvc", "data/raw_data/bodies")
	}

	for i := 0; i < 3; i++ {
		if _, err := jobs.Ensure(context.Background(), "ns-1", name, build); err != nil {
			t.Fatalf("tick %d: Ensure = %v", i, err)
		}
	}

	list, err := cs.BatchV1().Jobs("ns-1").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("every POST for the same directory started a new job: %d jobs", len(list.Items))
	}
}

func TestVolumeCompactJobEnsureCreatesSeparateJobsForSeparatePaths(t *testing.T) {
	cs := fake.NewSimpleClientset()
	jobs := &clusterVolumeMaintenanceJobs{cs: cs}

	for _, p := range []string{"data/raw_data/bodies", "data/raw_data/tmp"} {
		name := volumeCompactJobName("fonbet-value", p)
		relPath := p
		if _, err := jobs.Ensure(context.Background(), "ns-1", name, func() *batchv1.Job {
			return volumeCompactJobSpec("ns-1", "fonbet-value", "fonbet-value-pvc", relPath)
		}); err != nil {
			t.Fatalf("Ensure(%s): %v", p, err)
		}
	}

	list, err := cs.BatchV1().Jobs("ns-1").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("compacting two different directories produced %d jobs, want 2 independent jobs", len(list.Items))
	}
}

// --- script content: defense-in-depth checks the container itself performs ---

func TestVolumeCompactScriptRevalidatesInsideContainer(t *testing.T) {
	if !strings.Contains(volumeCompactScript, `"$MNT/"*`) {
		t.Fatal("script must re-check that the resolved target still starts with $MNT/ before touching anything")
	}
	if !strings.Contains(volumeCompactScript, "rm -rf \"$TARGET\"") {
		t.Fatal("script must remove only $TARGET (the validated directory), never a wider path")
	}
}

func TestVolumeCompactScriptOrdersVerifyBeforeDelete(t *testing.T) {
	tarIdx := strings.Index(volumeCompactScript, "tar czf")
	verifyIdx := strings.Index(volumeCompactScript, "ENTRIES_POST")
	rmIdx := strings.Index(volumeCompactScript, `rm -rf "$TARGET"`)
	if tarIdx < 0 || verifyIdx < 0 || rmIdx < 0 {
		t.Fatalf("expected tar, verify and rm steps all present in script (tar=%d verify=%d rm=%d)", tarIdx, verifyIdx, rmIdx)
	}
	if !(tarIdx < verifyIdx && verifyIdx < rmIdx) {
		t.Fatalf("script does not archive, then verify, then delete in that order: tar=%d verify=%d rm=%d", tarIdx, verifyIdx, rmIdx)
	}
}

// --- output parser ---

func TestParseVolumeCompactOutputValidJSON(t *testing.T) {
	logs := `{"files_packed":989000,"archive_path":"/mnt/vol/data/raw_data/bodies.tar.gz","archive_bytes":4200000000,"inodes_free_before":0,"inodes_free_after":988998}
`
	out, err := parseVolumeCompactOutput(logs)
	if err != nil {
		t.Fatalf("parseVolumeCompactOutput: %v", err)
	}
	if out.FilesPacked != 989000 {
		t.Fatalf("files_packed = %v, want 989000", out.FilesPacked)
	}
	if out.ArchivePath != "/mnt/vol/data/raw_data/bodies.tar.gz" {
		t.Fatalf("archive_path = %v", out.ArchivePath)
	}
	if out.InodesFreeBefore != 0 || out.InodesFreeAfter != 988998 {
		t.Fatalf("inode fields = %+v", out)
	}
}

func TestParseVolumeCompactOutputFindsJSONLineAmongNoise(t *testing.T) {
	logs := "warning: shell noise\n" +
		`{"files_packed":5,"archive_path":"/mnt/vol/x.tar.gz","archive_bytes":100,"inodes_free_before":10,"inodes_free_after":15}` + "\n"
	out, err := parseVolumeCompactOutput(logs)
	if err != nil {
		t.Fatalf("parseVolumeCompactOutput: %v", err)
	}
	if out.FilesPacked != 5 {
		t.Fatalf("files_packed = %v, want 5", out.FilesPacked)
	}
}

func TestParseVolumeCompactOutputGarbageFailsCleanly(t *testing.T) {
	logs := "target directory not found: /mnt/vol/data/ghost\n"
	if _, err := parseVolumeCompactOutput(logs); err == nil {
		t.Fatal("a failure message with no JSON line must return an error, not a zeroed result")
	}
}

func TestParseVolumeCompactOutputEmptyFailsCleanly(t *testing.T) {
	if _, err := parseVolumeCompactOutput(""); err == nil {
		t.Fatal("empty logs must return an error")
	}
}

func TestParseVolumeCompactOutputRejectsLineWithoutArchivePath(t *testing.T) {
	logs := `{"files_packed":0,"archive_path":"","archive_bytes":0,"inodes_free_before":0,"inodes_free_after":0}`
	if _, err := parseVolumeCompactOutput(logs); err == nil {
		t.Fatal("a JSON line with an empty archive_path must not be accepted as a valid success result")
	}
}
