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

// --- Job spec ---

func TestVolumeReportJobSpecShape(t *testing.T) {
	job := volumeReportJobSpec("artemmendeleev-gmail-com-prod", "fonbet-value", "fonbet-value-pvc")

	if job.Namespace != "artemmendeleev-gmail-com-prod" {
		t.Fatalf("job namespace = %q, want the app's own namespace, not a shared platform one", job.Namespace)
	}
	if job.Name != "vol-report-fonbet-value" {
		t.Fatalf("job name = %q, want vol-report-fonbet-value", job.Name)
	}

	pod := job.Spec.Template.Spec
	if len(pod.Volumes) != 1 || pod.Volumes[0].PersistentVolumeClaim == nil {
		t.Fatalf("pod does not mount a PVC volume: %+v", pod.Volumes)
	}
	if pod.Volumes[0].PersistentVolumeClaim.ClaimName != "fonbet-value-pvc" {
		t.Fatalf("claimName = %q, want fonbet-value-pvc", pod.Volumes[0].PersistentVolumeClaim.ClaimName)
	}
	if !pod.Volumes[0].PersistentVolumeClaim.ReadOnly {
		t.Fatal("the PVC volume must be read-only: a report never writes to a user's volume")
	}

	if len(pod.Containers) != 1 {
		t.Fatalf("want exactly one container, got %d", len(pod.Containers))
	}
	container := pod.Containers[0]
	if len(container.VolumeMounts) != 1 || !container.VolumeMounts[0].ReadOnly {
		t.Fatalf("container mount must be read-only: %+v", container.VolumeMounts)
	}
	if container.VolumeMounts[0].MountPath != volumeReportMountPath {
		t.Fatalf("mount path = %q, want %q", container.VolumeMounts[0].MountPath, volumeReportMountPath)
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
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 1 {
		t.Fatalf("backoffLimit = %v, want 1", job.Spec.BackoffLimit)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != 300 {
		t.Fatalf("activeDeadlineSeconds = %v, want 300", job.Spec.ActiveDeadlineSeconds)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 3600 {
		t.Fatalf("ttlSecondsAfterFinished = %v, want 3600", job.Spec.TTLSecondsAfterFinished)
	}

	if job.Labels["dada.io/app"] != "fonbet-value" || job.Labels["dada.io/maintenance"] != "volume-report" {
		t.Fatalf("job labels = %v, missing dada.io/app or dada.io/maintenance", job.Labels)
	}
}

func TestVolumeReportJobNameIsSanitizedAndBounded(t *testing.T) {
	longName := "Fonbet-VALUE-" + strings.Repeat("x", 80)
	name := volumeReportJobName(longName)
	if len(name) > 63 {
		t.Fatalf("job name %q is %d chars, over the Kubernetes 63-char limit", name, len(name))
	}
	if name[len(name)-1] == '-' {
		t.Fatalf("job name %q ends in a dash, not a valid Kubernetes object name", name)
	}
}

// --- Ensure idempotency ---

func TestVolumeReportJobEnsureCreatesOnce(t *testing.T) {
	cs := fake.NewSimpleClientset()
	jobs := &clusterVolumeMaintenanceJobs{cs: cs}
	build := func() *batchv1.Job {
		return volumeReportJobSpec("ns-1", "fonbet-value", "fonbet-value-pvc")
	}

	for i := 0; i < 3; i++ {
		if _, err := jobs.Ensure(context.Background(), "ns-1", "vol-report-fonbet-value", build); err != nil {
			t.Fatalf("tick %d: Ensure = %v", i, err)
		}
	}

	list, err := cs.BatchV1().Jobs("ns-1").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("every POST started a new job against the same volume: %d jobs", len(list.Items))
	}
}

func TestVolumeReportJobEnsureReportsFirstCreate(t *testing.T) {
	cs := fake.NewSimpleClientset()
	jobs := &clusterVolumeMaintenanceJobs{cs: cs}
	build := func() *batchv1.Job {
		return volumeReportJobSpec("ns-1", "fonbet-value", "fonbet-value-pvc")
	}

	created, err := jobs.Ensure(context.Background(), "ns-1", "vol-report-fonbet-value", build)
	if err != nil || !created {
		t.Fatalf("first Ensure = created=%v err=%v, want created=true", created, err)
	}
	created, err = jobs.Ensure(context.Background(), "ns-1", "vol-report-fonbet-value", build)
	if err != nil || created {
		t.Fatalf("second Ensure = created=%v err=%v, want created=false", created, err)
	}
}

// --- outcome classification ---

func TestVolumeReportJobOutcomeSucceeded(t *testing.T) {
	job := volumeReportJobSpec("ns-1", "app", "app-pvc")
	job.Status.Succeeded = 1
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobFailed,
		Status: corev1.ConditionTrue,
		Reason: "StaleFailureFromAnEarlierRetry",
	}}

	succeeded, failed, _ := volumeReportJobOutcome(job)
	if !succeeded || failed {
		t.Fatalf("succeeded=%v failed=%v, want succeeded to win over a stale failed condition", succeeded, failed)
	}
}

func TestVolumeReportJobOutcomeFailed(t *testing.T) {
	job := volumeReportJobSpec("ns-1", "app", "app-pvc")
	job.Status.Conditions = []batchv1.JobCondition{{
		Type:   batchv1.JobFailed,
		Status: corev1.ConditionTrue,
		Reason: "BackoffLimitExceeded",
	}}

	succeeded, failed, reason := volumeReportJobOutcome(job)
	if succeeded || !failed || reason != "BackoffLimitExceeded" {
		t.Fatalf("succeeded=%v failed=%v reason=%q, want failed=true reason=BackoffLimitExceeded", succeeded, failed, reason)
	}
}

func TestVolumeReportJobOutcomeRunning(t *testing.T) {
	job := volumeReportJobSpec("ns-1", "app", "app-pvc")
	succeeded, failed, _ := volumeReportJobOutcome(job)
	if succeeded || failed {
		t.Fatalf("succeeded=%v failed=%v, want both false while the job has no terminal status yet", succeeded, failed)
	}
}

// --- report parser ---

func TestParseVolumeReportOutputValidJSON(t *testing.T) {
	logs := `{"inodes_total":1310720,"inodes_used":1310720,"inodes_free":0,"bytes_total":21474836480,"bytes_used":15876296294,"bytes_free":5598540186,"top_dirs":[{"path":"/mnt/vol/data","files":989000},{"path":"/mnt/vol/tmp","files":12}],"truncated":false}
`
	out, err := parseVolumeReportOutput(logs)
	if err != nil {
		t.Fatalf("parseVolumeReportOutput: %v", err)
	}
	if out.InodesTotal != 1310720 || out.InodesUsed != 1310720 || out.InodesFree != 0 {
		t.Fatalf("inode fields = %+v", out)
	}
	if out.InodesRatio != 1.0 {
		t.Fatalf("inodes_ratio = %v, want 1.0 (this is the fonbet-value case: exhausted inode table)", out.InodesRatio)
	}
	if len(out.TopDirs) != 2 || out.TopDirs[0].Path != "/mnt/vol/data" || out.TopDirs[0].Files != 989000 {
		t.Fatalf("top_dirs = %+v", out.TopDirs)
	}
	if out.Truncated {
		t.Fatal("truncated should be false")
	}
}

func TestParseVolumeReportOutputTruncatedTrue(t *testing.T) {
	logs := `{"inodes_total":100,"inodes_used":50,"inodes_free":50,"bytes_total":100,"bytes_used":50,"bytes_free":50,"top_dirs":[],"truncated":true}`
	out, err := parseVolumeReportOutput(logs)
	if err != nil {
		t.Fatalf("parseVolumeReportOutput: %v", err)
	}
	if !out.Truncated {
		t.Fatal("truncated=true from the script must survive parsing -- a partial top-dirs list must never read as complete")
	}
}

func TestParseVolumeReportOutputGarbageFailsCleanly(t *testing.T) {
	logs := "sh: find: applet not found\nBusyBox v1.37.0\ncommand not found\n"
	if _, err := parseVolumeReportOutput(logs); err == nil {
		t.Fatal("garbage output must return an error, not a zeroed report that reads as a healthy volume")
	}
}

func TestParseVolumeReportOutputEmptyFailsCleanly(t *testing.T) {
	if _, err := parseVolumeReportOutput(""); err == nil {
		t.Fatal("empty logs must return an error")
	}
}

func TestParseVolumeReportOutputFindsJSONLineAmongNoise(t *testing.T) {
	logs := "warning: some shell noise on stdout\n" +
		`{"inodes_total":10,"inodes_used":5,"inodes_free":5,"bytes_total":10,"bytes_used":5,"bytes_free":5,"top_dirs":[],"truncated":false}` + "\n"
	out, err := parseVolumeReportOutput(logs)
	if err != nil {
		t.Fatalf("parseVolumeReportOutput: %v", err)
	}
	if out.InodesTotal != 10 {
		t.Fatalf("inodes_total = %v, want 10 (parser must find the JSON line, not the noise before it)", out.InodesTotal)
	}
}

// --- PVC resolve fallback ---

func TestResolveAppVolumePVCNameFromPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fonbet-value-abc123",
			Namespace: "ns-1",
			Labels:    map[string]string{"dada.io/app": "fonbet-value"},
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "vol",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "fonbet-value-pvc"},
				},
			}},
		},
	}
	cs := fake.NewSimpleClientset(pod)

	name := resolveAppPVCName(context.Background(), cs, "ns-1", "fonbet-value")
	if name != "fonbet-value-pvc" {
		t.Fatalf("resolveAppPVCName = %q, want fonbet-value-pvc from the live pod", name)
	}
}

func TestResolveAppVolumePVCNameFallsBackWhenPodIsGone(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fonbet-value-pvc",
			Namespace: "ns-1",
			Labels:    map[string]string{"argocd.argoproj.io/instance": "fonbet-value-prod-3e0c7967"},
		},
	}
	cs := fake.NewSimpleClientset(pvc)

	name := resolveAppPVCName(context.Background(), cs, "ns-1", "fonbet-value")
	if name != "fonbet-value-pvc" {
		t.Fatalf("resolveAppPVCName = %q, want the fallback fonbet-value-pvc (no pod, PVC exists by naming convention)", name)
	}
}

func TestResolveAppVolumePVCNameEmptyWhenNeitherExists(t *testing.T) {
	cs := fake.NewSimpleClientset()
	name := resolveAppPVCName(context.Background(), cs, "ns-1", "ghost-app")
	if name != "" {
		t.Fatalf("resolveAppPVCName = %q, want empty when no pod and no conventionally-named PVC exist (caller 404s on this)", name)
	}
}

func TestResolveAppVolumePVCNameFallbackNeverGuessesAnotherAppsClaim(t *testing.T) {
	other := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "some-other-claim", Namespace: "ns-1"},
	}
	cs := fake.NewSimpleClientset(other)

	name := resolveAppPVCName(context.Background(), cs, "ns-1", "fonbet-value")
	if name != "" {
		t.Fatalf("resolveAppPVCName = %q, want empty: a differently-named PVC in the namespace must never be picked by guessing", name)
	}
}
