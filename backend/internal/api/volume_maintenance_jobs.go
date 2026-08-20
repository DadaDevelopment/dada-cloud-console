package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// volumeReportImage is the container the volume-report Job runs. It has to
// start in an arbitrary user namespace, which carries no imagePullSecret and
// no wiring to our private Nexus registry (verified live,
// artemmendeleev-gmail-com-prod: default ServiceAccount has no
// imagePullSecrets at all, and the two registry secrets that do exist there
// are not attached to it). busybox is a public, unauthenticated Docker Hub
// image every node in the cluster can already reach -- confirmed live by the
// kube-system and network namespaces pulling equally unauthenticated images
// (registry.k8s.io, quay.io) with no pull secret -- and it carries the sh,
// df, find and wc the report script needs.
const volumeReportImage = "docker.io/library/busybox:1.37.0"

// volumeReportJobNamePrefix keeps the Job's name legible next to the app it
// reports on while leaving room, under the 63-character Kubernetes object
// name limit, for a long appName.
const volumeReportJobNamePrefix = "vol-report-"

// volumeReportActiveDeadlineSeconds bounds the whole Job, pod scheduling and
// image pull included -- a stuck admission or a wedged node must not leave
// the report "running" forever.
const volumeReportActiveDeadlineSeconds = int64(300)

// volumeReportScanDeadlineSeconds is the budget the script itself gives the
// directory walk, deliberately short of volumeReportActiveDeadlineSeconds so
// the script has time left to print its JSON line after the walk stops.
const volumeReportScanDeadlineSeconds = 220

// volumeReportPerDirTimeoutSeconds bounds a single directory's file count so
// one huge subdirectory cannot burn the whole scan budget alone.
const volumeReportPerDirTimeoutSeconds = 30

// volumeReportTopDirs is how many directories the report keeps, by file
// count, out of however many depth-2 subdirectories it found.
const volumeReportTopDirs = 20

// volumeReportBackoffLimit is 1, not 0: a Job that never retries turns a
// single transient scheduling hiccup into a permanent "failed" report, but a
// script that fails on real data (not garbage) will fail identically on the
// retry, so more than one retry buys nothing.
const volumeReportBackoffLimit = int32(1)

// volumeReportTTLSecondsAfterFinished keeps a finished Job (and the pod
// whose logs the report was read from) around long enough for a human to
// inspect it after the console already parsed the result.
const volumeReportTTLSecondsAfterFinished = int32(3600)

// volumeReportMountPath is where the script finds the app's volume inside
// the Job's container.
const volumeReportMountPath = "/mnt/vol"

// volumeReportScript is the whole report: inode and byte fill from df, plus
// a best-effort top-N of depth-2 subdirectories by file count. It is a
// constant, not a template, because nothing about the report varies by app
// -- only the PVC the Job mounts does, and that is a volume, not a script
// argument.
//
// It writes exactly one line of JSON to stdout so the caller's parser never
// has to guess which line, among shell tracing or tool warnings, is the
// answer. "truncated" is true whenever either the overall directory walk hit
// its deadline or any single directory's count was cut off by its own
// per-directory timeout -- so a partial top-dirs list is never presented as
// if it were complete.
var volumeReportScript = fmt.Sprintf(`set -eu
MNT=%s
DEADLINE=$(( $(date +%%s) + %d ))
TRUNCATED=false

IROW=$(df -iP "$MNT" 2>/dev/null | tail -n 1)
ITOTAL=$(echo "$IROW" | awk '{print $2}')
IUSED=$(echo "$IROW" | awk '{print $3}')
IFREE=$(echo "$IROW" | awk '{print $4}')

BROW=$(df -kP "$MNT" 2>/dev/null | tail -n 1)
BTOTAL=$(echo "$BROW" | awk '{print $2 * 1024}')
BUSED=$(echo "$BROW" | awk '{print $3 * 1024}')
BFREE=$(echo "$BROW" | awk '{print $4 * 1024}')

TMPCOUNTS=/tmp/vol-report-counts
TMPFIND=/tmp/vol-report-find
: > "$TMPCOUNTS"

DIRS=$(find "$MNT" -mindepth 1 -maxdepth 2 -type d 2>/dev/null || true)
for d in $DIRS; do
  NOW=$(date +%%s)
  if [ "$NOW" -ge "$DEADLINE" ]; then
    TRUNCATED=true
    break
  fi
  if ! timeout %d find "$d" > "$TMPFIND" 2>/dev/null; then
    TRUNCATED=true
  fi
  CNT=$(wc -l < "$TMPFIND")
  echo "$CNT $d" >> "$TMPCOUNTS"
done

TOP=$(sort -rn "$TMPCOUNTS" | head -n %d)

DIRSJSON=""
FIRST=true
while IFS= read -r line; do
  [ -z "$line" ] && continue
  CNT=$(echo "$line" | awk '{print $1}')
  PATHV=$(echo "$line" | cut -d' ' -f2-)
  ESCAPED=$(printf '%%s' "$PATHV" | sed 's/\\/\\\\/g; s/"/\\"/g')
  if [ "$FIRST" = true ]; then FIRST=false; else DIRSJSON="$DIRSJSON,"; fi
  DIRSJSON="$DIRSJSON{\"path\":\"$ESCAPED\",\"files\":$CNT}"
done <<EOF
$TOP
EOF

printf '{"inodes_total":%%s,"inodes_used":%%s,"inodes_free":%%s,"bytes_total":%%s,"bytes_used":%%s,"bytes_free":%%s,"top_dirs":[%%s],"truncated":%%s}\n' \
  "${ITOTAL:-0}" "${IUSED:-0}" "${IFREE:-0}" "${BTOTAL:-0}" "${BUSED:-0}" "${BFREE:-0}" "$DIRSJSON" "$TRUNCATED"
`, volumeReportMountPath, volumeReportScanDeadlineSeconds, volumeReportPerDirTimeoutSeconds, volumeReportTopDirs)

// volumeReportJobNameSanitizer strips everything a Kubernetes object name
// cannot carry, mirroring the DNS-1123 subdomain rule the API server
// enforces on Job names.
var volumeReportJobNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

// volumeReportJobName derives a deterministic, idempotent Job name from the
// app: the same app always maps to the same name, so a repeated request
// finds the Job it already created (Ensure) instead of starting a second
// scan, and truncates to the 63-character Kubernetes object name limit.
// Pure.
func volumeReportJobName(appName string) string {
	safe := volumeReportJobNameSanitizer.ReplaceAllString(strings.ToLower(appName), "-")
	safe = strings.Trim(safe, "-")
	name := volumeReportJobNamePrefix + safe
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

// volumeReportResourceRequests/Limits are deliberately explicit: user
// namespaces carry LimitRanges, and a Job pod that requests nothing can be
// rejected outright at admission -- which, from outside, looks exactly like
// "nothing happened" (no pod, no error the caller sees) rather than a
// deployable failure. df, find and wc over a stat-only walk need very
// little; 64Mi/256Mi covers even the process table for a namespace with a
// million small files.
func volumeReportResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// volumeReportJobSpec builds the Job that reads a volume report straight off
// an app's PVC without needing a running pod of the app itself. It runs in
// the app's own namespace (not a shared platform namespace, unlike the
// db-archive and db-move Jobs) because that is where the PVC lives, mounts
// the claim read-only since a report never writes, and carries explicit
// resources so a namespace LimitRange cannot silently swallow the pod at
// admission. Pure.
func volumeReportJobSpec(namespace, appName, pvcName string) *batchv1.Job {
	name := volumeReportJobName(appName)
	backoff := volumeReportBackoffLimit
	deadline := volumeReportActiveDeadlineSeconds
	ttl := volumeReportTTLSecondsAfterFinished
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"dada.io/app":         appName,
				"dada.io/maintenance": "volume-report",
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			ActiveDeadlineSeconds:   &deadline,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"dada.io/app":         appName,
						"dada.io/maintenance": "volume-report",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:      "report",
						Image:     volumeReportImage,
						Command:   []string{"/bin/sh", "-c", volumeReportScript},
						Resources: volumeReportResources(),
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "vol",
							MountPath: volumeReportMountPath,
							ReadOnly:  true,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "vol",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
								ReadOnly:  true,
							},
						},
					}},
				},
			},
		},
	}
}

// clusterVolumeMaintenanceJobs runs volume-maintenance Jobs through the
// in-cluster API. Unlike db-archive's clusterArchiveJobs it is not bound to
// one fixed namespace: every method takes the namespace the caller wants,
// because the app it reports on lives in an arbitrary user namespace.
type clusterVolumeMaintenanceJobs struct {
	cs kubernetes.Interface
}

// Ensure creates the Job if it does not already exist and reports whether a
// new Job was created. It never re-creates on top of an existing Job (that
// is what makes a repeated POST idempotent), and never reads Job status --
// callers that need the outcome call Status separately, since Ensure's job
// is only "make it exist".
func (c *clusterVolumeMaintenanceJobs) Ensure(ctx context.Context, namespace, name string, build func() *batchv1.Job) (created bool, err error) {
	_, err = c.cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("read job %s: %w", name, err)
	}
	if _, err := c.cs.BatchV1().Jobs(namespace).Create(ctx, build(), metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return false, nil
		}
		return false, fmt.Errorf("create job %s: %w", name, err)
	}
	return true, nil
}

// Status returns the named Job, or nil when it does not exist (or has
// already been swept by TTLSecondsAfterFinished) -- both read as "absent" to
// the caller, which is the correct answer for a report nobody has asked for
// yet, or one old enough that its Job is long gone.
func (c *clusterVolumeMaintenanceJobs) Status(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	job, err := c.cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read job %s: %w", name, err)
	}
	return job, nil
}

// PodLogs returns the report script's stdout from the Job's pod. A Job names
// its pods after itself through the auto-added "job-name" label, so that
// selector is the same lookup Kubernetes itself uses -- no naming convention
// of our own to keep in sync.
func (c *clusterVolumeMaintenanceJobs) PodLogs(ctx context.Context, namespace, jobName string) (string, error) {
	pods, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "job-name=" + jobName,
	})
	if err != nil {
		return "", fmt.Errorf("list pods for job %s: %w", jobName, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("job %s has no pod yet", jobName)
	}
	pod := pods.Items[len(pods.Items)-1]
	stream, err := c.cs.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{}).Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("read logs for pod %s: %w", pod.Name, err)
	}
	defer stream.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(stream); err != nil && buf.Len() == 0 {
		return "", fmt.Errorf("read logs for pod %s: %w", pod.Name, err)
	}
	return buf.String(), nil
}

// newClusterVolumeMaintenanceJobs builds the Job runner, or nil when the
// console is not running inside a cluster.
func newClusterVolumeMaintenanceJobs() *clusterVolumeMaintenanceJobs {
	clientset := newAppHealthClientset()
	if clientset == nil {
		return nil
	}
	return &clusterVolumeMaintenanceJobs{cs: clientset}
}

// volumeReportTopDir is one depth-2 subdirectory and how many files the walk
// counted under it.
type volumeReportTopDir struct {
	Path  string `json:"path"`
	Files int64  `json:"files"`
}

// volumeReportOutput is the parsed shape of the report script's one JSON
// line.
type volumeReportOutput struct {
	InodesTotal float64              `json:"inodes_total"`
	InodesUsed  float64              `json:"inodes_used"`
	InodesFree  float64              `json:"inodes_free"`
	BytesTotal  float64              `json:"bytes_total"`
	BytesUsed   float64              `json:"bytes_used"`
	BytesFree   float64              `json:"bytes_free"`
	TopDirs     []volumeReportTopDir `json:"top_dirs"`
	Truncated   bool                 `json:"truncated"`
	InodesRatio float64              `json:"inodes_ratio"`
}

// parseVolumeReportOutput turns the Job's pod logs into the parsed report,
// or an error when the logs hold no valid JSON line -- a script crash, an
// image without the expected tools, or a pod OOMKilled mid-write must come
// back as a clear failure rather than a panic or a silently zeroed report.
// It reads from the last line backwards because the container prints
// exactly one JSON line but nothing stops a shell from emitting a warning to
// stdout first. Pure.
func parseVolumeReportOutput(logs string) (volumeReportOutput, error) {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var out volumeReportOutput
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			continue
		}
		if out.InodesTotal > 0 {
			out.InodesRatio = out.InodesUsed / out.InodesTotal
		}
		return out, nil
	}
	return volumeReportOutput{}, fmt.Errorf("no JSON report line found in job output")
}

// volumeReportJobOutcome classifies a Job's status into the four states the
// GET endpoint reports. A Job that already succeeded is checked before a Job
// that is also marked failed, mirroring clusterArchiveJobs.Ensure: a retried
// phase can carry both a stale failure count from an earlier attempt and a
// later success, and success is the state that matters once it happens.
func volumeReportJobOutcome(job *batchv1.Job) (succeeded bool, failed bool, failReason string) {
	if job.Status.Succeeded > 0 {
		return true, false, ""
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return false, true, cond.Reason
		}
	}
	return false, false, ""
}

// volumeReportFinishedAt reads the Job's completion time, when it has one.
func volumeReportFinishedAt(job *batchv1.Job) *time.Time {
	if job.Status.CompletionTime != nil {
		t := job.Status.CompletionTime.Time
		return &t
	}
	return nil
}

// volumeMaintenanceEnvNamespace loads the environment's namespace the same
// way GetAppVolumeUsage and openAppFS do, so all three volume endpoints agree
// on what "this environment does not exist" and "this environment has no
// namespace yet" mean.
func (h *Handler) volumeMaintenanceEnvNamespace(c *gin.Context, projectID, envID uuid.UUID) (string, bool) {
	var namespace string
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&namespace)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return "", false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment")
		return "", false
	}
	if namespace == "" {
		respondNotFound(c)
		return "", false
	}
	return namespace, true
}

// CreateAppVolumeMaintenanceReport starts (or reuses) a volume-report Job for
// an app, so a project member gets an inode/byte/top-directories snapshot of
// the app's PVC even when the app has no live pod to exec into. POST
// /projects/:projectId/environments/:envId/apps/:appName/volume/maintenance/report
//
// @ID          createAppVolumeMaintenanceReport
// @Summary     Start a volume maintenance report for an app
// @Description Creates (or reuses, if one is already running or finished) a Kubernetes Job that mounts the app's PersistentVolumeClaim read-only and reports its inode and byte fill plus a top-20 of subdirectories by file count. Unlike the file browser and volume export, this does not need a running pod of the app -- it mounts the PVC directly, so it also works for an app crashlooping on a full volume. Does not wait for the Job: poll GET on the same path for the result. 404 when the app has no volume or is not a Kubernetes app.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     202       {object} map[string]interface{} "object with job_name and status=running"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/maintenance/report [post]
func (h *Handler) CreateAppVolumeMaintenanceReport(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	appName := c.Param("appName")

	if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
		return
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	namespace, ok := h.volumeMaintenanceEnvNamespace(c, projectID, envID)
	if !ok {
		return
	}

	audit := func(outcome string, metadata map[string]any) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateAppVolumeMaintenanceReport",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      metadata,
		})
	}

	ctx := c.Request.Context()
	pvcName := findAppPVCName(ctx, namespace, appName)
	if pvcName == "" {
		audit(auditOutcomeFailure, map[string]any{"reason": "no_volume"})
		respondNotFound(c)
		return
	}

	jobs := newClusterVolumeMaintenanceJobs()
	if jobs == nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "not_configured"})
		respondError(c, http.StatusServiceUnavailable, "volume maintenance is not configured for this environment")
		return
	}

	name := volumeReportJobName(appName)
	if _, err := jobs.Ensure(ctx, namespace, name, func() *batchv1.Job {
		return volumeReportJobSpec(namespace, appName, pvcName)
	}); err != nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "job_ensure_failed", "error": err.Error()})
		respondError(c, http.StatusInternalServerError, "failed to start volume report")
		return
	}

	audit(auditOutcomeSuccess, map[string]any{"job_name": name, "pvc": pvcName})
	c.JSON(http.StatusAccepted, gin.H{"job_name": name, "status": "running"})
}

// GetAppVolumeMaintenanceReport reads the outcome of a volume-report Job
// started by CreateAppVolumeMaintenanceReport. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/maintenance/report
//
// @ID          getAppVolumeMaintenanceReport
// @Summary     Read an app's volume maintenance report
// @Description Reads the status of the Job POST started, and once it has succeeded, the parsed report: inode and byte totals/used/free/ratio, a top-20 of subdirectories by file count, and whether that top-20 is a complete or partial (truncated) view of the volume. status is one of running, succeeded, failed, absent (no report was ever started, or its Job has already been swept).
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Success     200       {object} map[string]interface{} "object with status, and on status=succeeded also inodes_total/inodes_used/inodes_free/inodes_ratio, bytes_total/bytes_used/bytes_free, top_dirs, truncated, finished_at; on status=failed also reason and hint"
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/maintenance/report [get]
func (h *Handler) GetAppVolumeMaintenanceReport(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	appName := c.Param("appName")

	if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	namespace, ok := h.volumeMaintenanceEnvNamespace(c, projectID, envID)
	if !ok {
		return
	}

	jobs := newClusterVolumeMaintenanceJobs()
	if jobs == nil {
		respondError(c, http.StatusServiceUnavailable, "volume maintenance is not configured for this environment")
		return
	}

	ctx := c.Request.Context()
	name := volumeReportJobName(appName)
	job, err := jobs.Status(ctx, namespace, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read volume report")
		return
	}
	if job == nil {
		c.JSON(http.StatusOK, gin.H{"status": "absent"})
		return
	}

	succeeded, failed, failReason := volumeReportJobOutcome(job)
	if failed {
		c.JSON(http.StatusOK, gin.H{
			"status": "failed",
			"reason": failReason,
			"hint":   "see the job's pod logs",
		})
		return
	}
	if !succeeded {
		c.JSON(http.StatusOK, gin.H{"status": "running"})
		return
	}

	logs, err := jobs.PodLogs(ctx, namespace, name)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "failed",
			"reason": err.Error(),
			"hint":   "see the job's pod logs",
		})
		return
	}
	out, err := parseVolumeReportOutput(logs)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "failed",
			"reason": err.Error(),
			"hint":   "see the job's pod logs",
		})
		return
	}

	resp := gin.H{
		"status":       "succeeded",
		"inodes_total": out.InodesTotal,
		"inodes_used":  out.InodesUsed,
		"inodes_free":  out.InodesFree,
		"inodes_ratio": out.InodesRatio,
		"bytes_total":  out.BytesTotal,
		"bytes_used":   out.BytesUsed,
		"bytes_free":   out.BytesFree,
		"top_dirs":     out.TopDirs,
		"truncated":    out.Truncated,
	}
	if finished := volumeReportFinishedAt(job); finished != nil {
		resp["finished_at"] = finished
	}
	c.JSON(http.StatusOK, resp)
}
