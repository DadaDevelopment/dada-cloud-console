// The volume report (volume_maintenance_jobs.go) can only ever describe
// fonbet-value's outage, never fix it: ext4 hands out inodes at mkfs time,
// so the only way to get one back without recreating the filesystem is to
// make the files that hold it disappear. Deleting them one at a time through
// the file browser needs a running pod (which a full-inode app does not
// have) and would take a human clicking through roughly a million rows even
// if it did. Collapsing a directory into a single tar.gz is the one
// operation that returns inodes at the scale this outage needs: about a
// million files in the target directory become the one inode the archive
// occupies.
//
// This Job mounts the PVC read-write -- the report Job never does, and that
// difference is the whole point of a second Job type rather than a flag on
// the first one: a script that can delete a user's files has to be trusted
// with strictly more than a script that only reads df and find output.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// volumeCompactJobNamePrefix mirrors volumeReportJobNamePrefix. It carries
// its own prefix (not volumeReportJobNamePrefix) so a report Job and a
// compact Job for the same app never collide on the same object name --
// they are different operations with different blast radii and must be
// individually inspectable in `kubectl get jobs`.
const volumeCompactJobNamePrefix = "vol-compact-"

// volumeCompactActiveDeadlineSeconds is far more generous than the report's
// 300s: packing on the order of a million small files through tar+gzip is a
// slow, mostly single-threaded walk, and killing the Job early must never
// happen mid-tar -- the ordering guarantee in volumeCompactScript (archive,
// verify, only then delete) depends on the Job being allowed to finish the
// verify step it is currently on rather than being cut off at an arbitrary
// point. 1800s is a budget, not an estimate: fonbet-value's ~989k files
// have not been timed, so this errs wide instead of guessing narrow and
// leaving the fix half-finished.
const volumeCompactActiveDeadlineSeconds = int64(1800)

// volumeCompactBackoffLimit is 0, deliberately unlike the report's 1. The
// report is read-only, so retrying it after a transient failure is free.
// Compact deletes the source directory as its last step; if a first attempt
// somehow reached partway into that delete before dying (a node failure
// mid-rm, not a script failure -- the script itself only reaches rm after
// verification passes), an automatic retry would run tar over a directory
// that is now partially gone and archive a corrupted subset as if it were
// complete. A human has to look at a failed compact before anything runs
// against that directory again.
const volumeCompactBackoffLimit = int32(0)

// volumeCompactTTLSecondsAfterFinished outlives the report's 3600s because a
// compact result -- how many inodes came back, where the archive landed --
// is the evidence a human needs before trusting the app to restart, and a
// slow-to-notice operator should not find the Job already swept.
const volumeCompactTTLSecondsAfterFinished = int32(7200)

// volumeCompactTargetEnvVar carries the validated, relative-to-volume-root
// directory path into the Job's container as a Kubernetes environment
// variable, never by formatting it into the shell script text. This is the
// load-bearing choice against shell injection: an env var's value reaches
// the container as inert data through the Kubernetes API, not as characters
// a shell parses for meaning, so nothing the caller supplies can ever be
// interpreted as a command, regardless of what validateVolumeCompactPath
// does or does not catch. validateVolumeCompactPath still runs first and
// rejects almost all of that input outright; the env var boundary is the
// second, independent lock on the same door.
const volumeCompactTargetEnvVar = "TARGET_REL_PATH"

// volumeCompactPathHashLen is how many hex characters of the path's sha256
// go into the Job name. 10 hex characters (40 bits) makes two distinct
// directories of the same app collide on a Job name only by extraordinary
// coincidence, while leaving enough of the 63-character Kubernetes object
// name budget for a legible appName prefix.
const volumeCompactPathHashLen = 10

// volumeCompactResources asks for more than volumeReportResources: gzip is
// CPU-bound in a way df/find/wc never are, and the working set is larger
// over roughly a million files. Still explicit for the same reason the
// report's are: a user-namespace LimitRange rejects a pod that requests
// nothing, silently from the caller's point of view.
func volumeCompactResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

// volumeCompactScript is the whole packing operation. Order is the entire
// safety contract, so it is spelled out here rather than left to be
// inferred from the code: (1) resolve and re-validate the target inside the
// container, never trusting that Go-side validation was the only gate; (2)
// refuse if there is not enough free byte budget to even hold the archive
// next to the directory it is made from, so a byte-side ENOSPC cannot ever
// interrupt the run partway; (3) count what is there; (4) tar it; (5) prove
// the archive is real, non-empty, and lists exactly as many entries as were
// counted before tar ran; (6) only then remove the original. Any failure at
// any step -- including a mismatched entry count, which is the strongest
// signal something about the archive is not trustworthy -- deletes nothing
// but the (unverified) archive itself and exits non-zero, leaving the
// user's files exactly as they were. set -eu turns every unset variable and
// every non-zero exit from a plain command into an immediate abort, so
// nothing after a failed step is reached implicitly.
//
// It prints exactly one line of JSON on success, parsed by
// parseVolumeCompactOutput, matching volumeReportScript's contract so both
// Job types are read the same way.
var volumeCompactScript = fmt.Sprintf(`set -eu
MNT=%s
TARGET_REL="${%s:?%s not set}"

case "$TARGET_REL" in
  "/"*|*..*)
    echo "target path must be relative and free of .. segments" >&2
    exit 10
    ;;
esac

TARGET="$MNT/$TARGET_REL"
case "$TARGET" in
  "$MNT/"*) : ;;
  *)
    echo "target path escapes the volume mount" >&2
    exit 10
    ;;
esac

if [ ! -d "$TARGET" ]; then
  echo "target directory not found: $TARGET" >&2
  exit 11
fi

PARENT=$(dirname "$TARGET")
BASE=$(basename "$TARGET")
ARCHIVE="$PARENT/$BASE.tar.gz"

if [ -e "$ARCHIVE" ]; then
  echo "archive already exists at $ARCHIVE, remove it before retrying" >&2
  exit 12
fi

IFREE_BEFORE=$(df -iP "$MNT" 2>/dev/null | tail -n 1 | awk '{print $4}')
BFREE=$(df -kP "$MNT" 2>/dev/null | tail -n 1 | awk '{print $4 * 1024}')
DIRBYTES=$(du -sk "$TARGET" 2>/dev/null | awk '{print $1 * 1024}')

if [ -z "${DIRBYTES:-}" ]; then
  echo "could not measure directory size" >&2
  exit 13
fi
if [ "$DIRBYTES" -ge "${BFREE:-0}" ]; then
  echo "not enough free bytes to safely archive: needs about $DIRBYTES, have ${BFREE:-0}" >&2
  exit 14
fi

ENTRIES_PRE=$(cd "$PARENT" && find "$BASE" | wc -l)
FILES_PRE=$(cd "$PARENT" && find "$BASE" -type f | wc -l)

if ! (cd "$PARENT" && tar czf "$ARCHIVE" "$BASE"); then
  rm -f "$ARCHIVE"
  echo "tar failed, original left untouched" >&2
  exit 15
fi

if [ ! -s "$ARCHIVE" ]; then
  rm -f "$ARCHIVE"
  echo "archive is missing or empty after tar, original left untouched" >&2
  exit 16
fi

ENTRIES_POST=$(tar tzf "$ARCHIVE" 2>/dev/null | wc -l)
if [ "$ENTRIES_POST" -ne "$ENTRIES_PRE" ]; then
  rm -f "$ARCHIVE"
  echo "archive entry count $ENTRIES_POST does not match source count $ENTRIES_PRE, original left untouched" >&2
  exit 17
fi

ARCHIVE_BYTES=$(wc -c < "$ARCHIVE")

rm -rf "$TARGET"

IFREE_AFTER=$(df -iP "$MNT" 2>/dev/null | tail -n 1 | awk '{print $4}')

printf '{"files_packed":%%s,"archive_path":"%%s","archive_bytes":%%s,"inodes_free_before":%%s,"inodes_free_after":%%s}\n' \
  "${FILES_PRE:-0}" "$ARCHIVE" "${ARCHIVE_BYTES:-0}" "${IFREE_BEFORE:-0}" "${IFREE_AFTER:-0}"
`, volumeReportMountPath, volumeCompactTargetEnvVar, volumeCompactTargetEnvVar)

// validateVolumeCompactPath is the first of the two independent locks
// described on volumeCompactTargetEnvVar. It accepts either a path already
// relative to the volume root, or the same absolute form the report Job
// hands back in top_dirs (e.g. "/mnt/vol/data/raw_data/bodies") so the
// caller can pass a top_dirs entry straight through without knowing the
// mount path is an implementation detail. Everything else about the
// contract is an allowlist, not a denylist: only letters, digits, dot,
// underscore, dash and slash survive, "." and ".." are refused as whole
// path segments (not just as a leading prefix, since a middle segment needs
// catching too), and the empty path (the volume root itself) is refused
// because compacting the whole volume is not what this endpoint is for --
// top_dirs entries are always at least one real subdirectory. Pure.
func validateVolumeCompactPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("path contains a nul byte")
	}

	trimmed := raw
	switch {
	case trimmed == volumeReportMountPath || strings.HasPrefix(trimmed, volumeReportMountPath+"/"):
		trimmed = strings.TrimPrefix(trimmed, volumeReportMountPath)
		trimmed = strings.TrimPrefix(trimmed, "/")
	case strings.HasPrefix(trimmed, "/"):
		return "", fmt.Errorf("absolute path %q is outside the volume mount %q", raw, volumeReportMountPath)
	}

	if trimmed == "" {
		return "", fmt.Errorf("path must not be the volume root")
	}

	segments := strings.Split(trimmed, "/")
	for _, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("path must not contain empty segments")
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("path must not contain . or .. segments")
		}
		for _, r := range seg {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
			if !ok {
				return "", fmt.Errorf("path contains a character that is not a letter, digit, dot, underscore or dash: %q", r)
			}
		}
	}

	return strings.Join(segments, "/"), nil
}

// volumeCompactPathHash is the deterministic fragment of the Job name that
// makes compacting the same directory twice idempotent (like the report's
// Ensure) without granting the console's ServiceAccount delete on jobs. A
// second POST for a directory that already has a running or finished
// compact Job finds that Job by name and reuses it; a POST for a different
// directory gets a different name and a different Job, never colliding with
// or needing to displace the first. Pure.
func volumeCompactPathHash(relPath string) string {
	sum := sha256.Sum256([]byte(relPath))
	return hex.EncodeToString(sum[:])[:volumeCompactPathHashLen]
}

// volumeCompactJobName derives the Job name from both the app and the
// validated relative path, truncating only the app portion to stay under
// the 63-character Kubernetes object name limit so the path hash -- the
// part that actually distinguishes one compact from another on the same
// app -- always survives intact. Pure.
func volumeCompactJobName(appName, relPath string) string {
	hash := volumeCompactPathHash(relPath)
	suffix := "-" + hash
	safeApp := volumeReportJobNameSanitizer.ReplaceAllString(strings.ToLower(appName), "-")
	safeApp = strings.Trim(safeApp, "-")

	maxAppLen := 63 - len(volumeCompactJobNamePrefix) - len(suffix)
	if maxAppLen < 1 {
		maxAppLen = 1
	}
	if len(safeApp) > maxAppLen {
		safeApp = safeApp[:maxAppLen]
	}
	safeApp = strings.Trim(safeApp, "-")

	return volumeCompactJobNamePrefix + safeApp + suffix
}

// volumeCompactJobSpec builds the Job that packs one directory of an app's
// volume into a tar.gz and removes the originals once the archive is
// proven good. Unlike volumeReportJobSpec it mounts the claim read-write --
// this Job's entire purpose is to delete the files that are eating the
// inode table, and byte-only deletion (bytes are not the shortage here)
// would not free a single inode. Pure.
func volumeCompactJobSpec(namespace, appName, pvcName, relPath string) *batchv1.Job {
	name := volumeCompactJobName(appName, relPath)
	backoff := volumeCompactBackoffLimit
	deadline := volumeCompactActiveDeadlineSeconds
	ttl := volumeCompactTTLSecondsAfterFinished
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"dada.io/app":         appName,
				"dada.io/maintenance": "volume-compact",
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
						"dada.io/maintenance": "volume-compact",
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:    "compact",
						Image:   volumeReportImage,
						Command: []string{"/bin/sh", "-c", volumeCompactScript},
						Env: []corev1.EnvVar{{
							Name:  volumeCompactTargetEnvVar,
							Value: relPath,
						}},
						Resources: volumeCompactResources(),
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "vol",
							MountPath: volumeReportMountPath,
							ReadOnly:  false,
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "vol",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
								ReadOnly:  false,
							},
						},
					}},
				},
			},
		},
	}
}

// volumeCompactOutput is the parsed shape of the compact script's one JSON
// line, printed only on success -- a failed run's diagnosis comes from the
// Job's condition, exactly as it does for the report, since the script
// exits before printing anything on every failure path.
type volumeCompactOutput struct {
	FilesPacked      int64  `json:"files_packed"`
	ArchivePath      string `json:"archive_path"`
	ArchiveBytes     int64  `json:"archive_bytes"`
	InodesFreeBefore int64  `json:"inodes_free_before"`
	InodesFreeAfter  int64  `json:"inodes_free_after"`
}

// parseVolumeCompactOutput mirrors parseVolumeReportOutput: it scans from
// the last line backwards for the one JSON line the script is contracted to
// print, and returns an error rather than a zeroed struct when it finds
// none -- a crash, an OOMKilled pod, or an image missing a tool the script
// needs must read back as "failed to parse", never as "packed 0 files
// successfully". Pure.
func parseVolumeCompactOutput(logs string) (volumeCompactOutput, error) {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var out volumeCompactOutput
		if err := json.Unmarshal([]byte(line), &out); err != nil {
			continue
		}
		if out.ArchivePath == "" {
			continue
		}
		return out, nil
	}
	return volumeCompactOutput{}, fmt.Errorf("no JSON compact result line found in job output")
}

// volumeCompactRequest is the POST body for CreateAppVolumeMaintenanceCompact.
type volumeCompactRequest struct {
	Path string `json:"path" binding:"required"`
}

// CreateAppVolumeMaintenanceCompact starts (or reuses) a Job that packs one
// directory of an app's volume into a tar.gz and deletes the originals once
// the archive is verified, so a project member can recover inodes on a
// volume whose inode table is full -- the one thing the read-only report
// Job can describe but never fix. POST
// /projects/:projectId/environments/:envId/apps/:appName/volume/maintenance/compact
//
// @ID          createAppVolumeMaintenanceCompact
// @Summary     Pack a directory of an app's volume to recover inodes
// @Description Creates (or reuses, if one is already running or finished for the same directory) a Kubernetes Job that mounts the app's PersistentVolumeClaim read-write, tars the given directory (relative to the volume root, or the absolute form returned in the report's top_dirs), verifies the archive before touching anything, and only then deletes the original files. Does not wait for the Job: poll GET on the same path with the same path query parameter for the result. 400 when the path fails validation. 404 when the app has no volume or is not a Kubernetes app.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     volumeCompactRequest  true "Directory to pack, relative to the volume root"
// @Success     202       {object} map[string]interface{} "object with job_name and status=running"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/maintenance/compact [post]
func (h *Handler) CreateAppVolumeMaintenanceCompact(c *gin.Context) {
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

	var req volumeCompactRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	namespace, ok := h.volumeMaintenanceEnvNamespace(c, projectID, envID)
	if !ok {
		return
	}

	audit := func(outcome string, metadata map[string]any) {
		metadata["path"] = req.Path
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "CreateAppVolumeMaintenanceCompact",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       outcome,
			Metadata:      metadata,
		})
	}

	relPath, err := validateVolumeCompactPath(req.Path)
	if err != nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "invalid_path", "error": err.Error()})
		respondError(c, http.StatusBadRequest, "invalid path: "+err.Error())
		return
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

	name := volumeCompactJobName(appName, relPath)
	if _, err := jobs.Ensure(ctx, namespace, name, func() *batchv1.Job {
		return volumeCompactJobSpec(namespace, appName, pvcName, relPath)
	}); err != nil {
		audit(auditOutcomeFailure, map[string]any{"reason": "job_ensure_failed", "error": err.Error()})
		respondError(c, http.StatusInternalServerError, "failed to start volume compact")
		return
	}

	audit(auditOutcomeSuccess, map[string]any{"job_name": name, "pvc": pvcName})
	c.JSON(http.StatusAccepted, gin.H{"job_name": name, "status": "running"})
}

// GetAppVolumeMaintenanceCompact reads the outcome of a compact Job started
// by CreateAppVolumeMaintenanceCompact. The path query parameter must match
// what was POSTed (after validation it maps to the same Job name), since the
// same app can have a compact result for more than one directory at once.
// GET /projects/:projectId/environments/:envId/apps/:appName/volume/maintenance/compact
//
// @ID          getAppVolumeMaintenanceCompact
// @Summary     Read the result of an app's volume compact
// @Description Reads the status of the Job POST started for the given path: running, succeeded, failed, or absent (no compact was ever started for this path, or its Job has already been swept). On succeeded, also returns files_packed, archive_path, archive_bytes, inodes_free_before/after and inodes_freed.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       path      query    string true "Directory that was (or will be) packed, relative to the volume root"
// @Success     200       {object} map[string]interface{} "object with status, and on status=succeeded also files_packed/archive_path/archive_bytes/inodes_free_before/inodes_free_after/inodes_freed/finished_at; on status=failed also reason and hint"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/maintenance/compact [get]
func (h *Handler) GetAppVolumeMaintenanceCompact(c *gin.Context) {
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

	relPath, err := validateVolumeCompactPath(c.Query("path"))
	if err != nil {
		respondError(c, http.StatusBadRequest, "invalid path: "+err.Error())
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
	name := volumeCompactJobName(appName, relPath)
	job, err := jobs.Status(ctx, namespace, name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to read volume compact")
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
	out, err := parseVolumeCompactOutput(logs)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "failed",
			"reason": err.Error(),
			"hint":   "see the job's pod logs",
		})
		return
	}

	resp := gin.H{
		"status":             "succeeded",
		"files_packed":       out.FilesPacked,
		"archive_path":       out.ArchivePath,
		"archive_bytes":      out.ArchiveBytes,
		"inodes_free_before": out.InodesFreeBefore,
		"inodes_free_after":  out.InodesFreeAfter,
		"inodes_freed":       out.InodesFreeAfter - out.InodesFreeBefore,
	}
	if finished := volumeReportFinishedAt(job); finished != nil {
		resp["finished_at"] = finished
	}
	c.JSON(http.StatusOK, resp)
}
