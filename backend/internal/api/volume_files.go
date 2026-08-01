package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	k8sexec "k8s.io/client-go/util/exec"
)

// Limits for the app file browser. Text reads and writes are deliberately
// small: the editor is for config and data files, while raw download and the
// tarball export carry anything bigger.
const (
	maxTextFileBytes  = 1 << 20
	maxUploadBytes    = 100 << 20
	fsMetadataTimeout = 60 * time.Second
	fsStreamTimeout   = 10 * time.Minute
	binarySniffBytes  = 8000
)

// Shell exit codes used by the PodFS scripts to distinguish their own failures
// from those of the tools they call.
const (
	fsExitPathNotFound = 66
	fsExitDestExists   = 67
)

// appFSSession is everything the file handlers need after authorisation: which
// container to exec in, and the resolved physical root that every requested
// path must stay under.
type appFSSession struct {
	projectID uuid.UUID
	envID     uuid.UUID
	appName   string
	target    cloudtask.PodTarget
	root      string
}

// openAppFS authorises the caller, locates a running pod for the app and
// resolves the app's volume mount to a symlink-free root path. It writes the
// error response itself and returns ok=false when the app has no volume, no
// running pod, or file access is not configured for this environment.
func (h *Handler) openAppFS(c *gin.Context, needWrite bool) (*appFSSession, bool) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return nil, false
	}
	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return nil, false
	}
	appName := c.Param("appName")

	if needWrite {
		if _, err := h.requireWriter(c, claims.UserID, projectID); err != nil {
			return nil, false
		}
	} else if _, err := h.requireMember(c, claims.UserID, projectID); err != nil {
		return nil, false
	}

	if !h.podFS.Enabled() {
		respondError(c, http.StatusServiceUnavailable, "file access is not configured for this environment")
		return nil, false
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return nil, false
	}

	var namespace string
	err := h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&namespace)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return nil, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment")
		return nil, false
	}
	if namespace == "" {
		respondError(c, http.StatusConflict, "environment has no namespace")
		return nil, false
	}

	var summaryRaw []byte
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT summary_json FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&summaryRaw)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return nil, false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load app")
		return nil, false
	}

	var cur struct {
		Volume *models.AppVolume `json:"volume"`
	}
	_ = json.Unmarshal(summaryRaw, &cur)
	if cur.Volume == nil || cur.Volume.Path == "" {
		respondError(c, http.StatusConflict, "app has no volume")
		return nil, false
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), fsMetadataTimeout)
	defer cancel()

	podName, containerName, err := h.podFS.FindRunningPod(ctx, namespace, appName)
	if err != nil {
		respondError(c, http.StatusConflict, "no running pod for this app: files are readable only while the app is running")
		return nil, false
	}
	target := cloudtask.PodTarget{Namespace: namespace, Pod: podName, Container: containerName}

	root, err := h.podFS.ResolvePath(ctx, target, cur.Volume.Path)
	if err != nil {
		status, msg := classifyFSError(err, "failed to open the app's volume")
		respondError(c, status, msg)
		return nil, false
	}

	return &appFSSession{
		projectID: projectID,
		envID:     envID,
		appName:   appName,
		target:    target,
		root:      root,
	}, true
}

// resolve turns a client-supplied path into the physical path to operate on.
// Every path in this API is relative to the volume root — "/" is the mount
// point, never the container's root directory — so the path is normalised
// against the root first, which makes a lexical "../.." escape impossible, and
// then resolved inside the container so a symlinked directory cannot walk out
// either.
func (s *appFSSession) resolve(ctx context.Context, fs cloudtask.PodFS, raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errFSOutsideVolume
	}
	rooted := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	return s.resolvePhysical(ctx, fs, path.Join(s.root, rooted))
}

// resolvePhysical is resolve for a path that is already absolute inside the
// container (one built from an earlier resolve result). It re-checks the
// lexical prefix and then the physically resolved one.
func (s *appFSSession) resolvePhysical(ctx context.Context, fs cloudtask.PodFS, candidate string) (string, error) {
	candidate = path.Clean(candidate)
	if !withinRoot(s.root, candidate) {
		return "", errFSOutsideVolume
	}
	resolved, err := fs.ResolvePath(ctx, s.target, candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.root, resolved) {
		return "", errFSOutsideVolume
	}
	return resolved, nil
}

// resolveLeaf is resolve plus a symlink check: when the entry is a link, its
// dereferenced target must also stay inside the volume, so reading or
// downloading a link cannot leak a file from the container's root filesystem.
func (s *appFSSession) resolveLeaf(ctx context.Context, fs cloudtask.PodFS, raw string) (string, cloudtask.FileEntry, error) {
	resolved, err := s.resolve(ctx, fs, raw)
	if err != nil {
		return "", cloudtask.FileEntry{}, err
	}
	entry, err := fs.Stat(ctx, s.target, resolved)
	if err != nil {
		return "", cloudtask.FileEntry{}, err
	}
	if entry.Kind == cloudtask.FileKindSymlink {
		linkTarget, err := fs.ResolveLink(ctx, s.target, resolved)
		if err != nil {
			return "", cloudtask.FileEntry{}, errFSOutsideVolume
		}
		if !withinRoot(s.root, linkTarget) {
			return "", cloudtask.FileEntry{}, errFSOutsideVolume
		}
	}
	return resolved, entry, nil
}

// relative renders a physical path back as a volume-relative path for the API
// response, so clients never have to know the container's mount point.
func (s *appFSSession) relative(p string) string {
	if p == s.root {
		return "/"
	}
	return strings.TrimPrefix(p, s.root)
}

var errFSOutsideVolume = errors.New("path is outside the app's volume")

// withinRoot reports whether p is root itself or lives under it. The separator
// check prevents "/data-backup" from passing as a child of "/data".
func withinRoot(root, p string) bool {
	if p == root {
		return true
	}
	if root == "/" {
		return strings.HasPrefix(p, "/")
	}
	return strings.HasPrefix(p, root+"/")
}

// classifyFSError turns a pod-exec failure into an HTTP status plus a message
// a user can act on. The shell scripts' own exit codes are checked first, then
// the stderr tail, so "tar: /data/x: No such file" becomes a 404 rather than a
// generic 502.
func classifyFSError(err error, fallback string) (int, string) {
	if errors.Is(err, errFSOutsideVolume) {
		return http.StatusBadRequest, "path is outside the app's volume"
	}
	var pe *cloudtask.PodExecError
	if !errors.As(err, &pe) {
		return http.StatusBadGateway, fallback + ": " + err.Error()
	}
	var exitErr k8sexec.ExitError
	if errors.As(pe.Err, &exitErr) {
		switch exitErr.ExitStatus() {
		case fsExitPathNotFound:
			return http.StatusNotFound, "path not found"
		case fsExitDestExists:
			return http.StatusConflict, "destination already exists"
		}
	}
	stderr := strings.ToLower(pe.Stderr)
	switch {
	case strings.Contains(stderr, "no such file"):
		return http.StatusNotFound, "path not found"
	case strings.Contains(stderr, "permission denied"):
		return http.StatusConflict, "permission denied inside the container"
	case strings.Contains(stderr, "not empty"):
		return http.StatusConflict, "directory is not empty"
	case strings.Contains(stderr, "no space left"):
		return http.StatusInsufficientStorage, "no space left on the volume"
	case strings.Contains(stderr, "read-only file system"):
		return http.StatusConflict, "the volume is mounted read-only"
	}
	lowErr := strings.ToLower(pe.Err.Error())
	if strings.Contains(lowErr, "executable file not found") || strings.Contains(stderr, "executable file not found") {
		return http.StatusConflict, "the app's image has no shell, so its files cannot be browsed"
	}
	return http.StatusBadGateway, fallback + ": " + pe.Error()
}

// auditFS records one file-manager action. Reads of directory listings and
// text previews are not audited (too noisy to be useful); everything that
// mutates the volume or exports data out of it is.
func (h *Handler) auditFS(c *gin.Context, s *appFSSession, action string, outcome string, meta map[string]any) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		return
	}
	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     s.projectID,
		EnvironmentID: s.envID,
		Action:        action,
		ResourceKind:  "App",
		ResourceName:  s.appName,
		Outcome:       outcome,
		Metadata:      meta,
	})
}

// ListAppFiles lists one directory of an app's persistent volume. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/files
//
// @ID          listAppFiles
// @Summary     List a directory inside an app's persistent volume
// @Description Lists one directory of an app's persistent volume, read live from a Running pod of the app. Paths are relative to the volume mount ("/" is the volume root); absolute paths must stay inside it. 409 when the app has no volume, has no running pod, or its image has no shell. 503 when file access is not configured for this environment.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       path      query    string false "Directory path relative to the volume root (default \"/\")"
// @Success     200       {object} map[string]interface{} "object with path, entries and truncated"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files [get]
func (h *Handler) ListAppFiles(c *gin.Context) {
	s, ok := h.openAppFS(c, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsMetadataTimeout)
	defer cancel()

	dir, err := s.resolve(ctx, h.podFS, c.Query("path"))
	if err != nil {
		status, msg := classifyFSError(err, "failed to list directory")
		respondError(c, status, msg)
		return
	}
	entries, truncated, err := h.podFS.List(ctx, s.target, dir)
	if err != nil {
		status, msg := classifyFSError(err, "failed to list directory")
		respondError(c, status, msg)
		return
	}
	if entries == nil {
		entries = []cloudtask.FileEntry{}
	}
	c.JSON(http.StatusOK, gin.H{
		"path":      s.relative(dir),
		"entries":   entries,
		"truncated": truncated,
	})
}

// ReadAppFile returns a text file's content for the in-console editor. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/files/content
//
// @ID          readAppFile
// @Summary     Read a text file from an app's persistent volume
// @Description Returns up to 1 MiB of a file's content as UTF-8 text, for editing in the console. 415 when the file is binary (download it instead). 413 when it is larger than 1 MiB. The returned modified timestamp must be echoed back on write to detect a concurrent change.
// @Tags        app
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       path      query    string true "File path relative to the volume root"
// @Success     200       {object} map[string]interface{} "object with path, content, size and modified"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     415       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/content [get]
func (h *Handler) ReadAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsMetadataTimeout)
	defer cancel()

	target, entry, err := s.resolveLeaf(ctx, h.podFS, c.Query("path"))
	if err != nil {
		status, msg := classifyFSError(err, "failed to read file")
		respondError(c, status, msg)
		return
	}
	if entry.Kind == cloudtask.FileKindDir {
		respondError(c, http.StatusConflict, "path is a directory")
		return
	}
	if entry.Size > maxTextFileBytes {
		respondError(c, http.StatusRequestEntityTooLarge, "file is larger than 1 MiB: download it instead")
		return
	}

	var buf strings.Builder
	if err := h.podFS.ReadFile(ctx, s.target, target, maxTextFileBytes, &buf); err != nil {
		status, msg := classifyFSError(err, "failed to read file")
		respondError(c, status, msg)
		return
	}
	content := buf.String()
	if isBinaryContent(content) {
		respondError(c, http.StatusUnsupportedMediaType, "file is binary: download it instead")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"path":     s.relative(target),
		"content":  content,
		"size":     entry.Size,
		"modified": entry.ModTime,
	})
}

// isBinaryContent reports whether a preview looks like a binary file: a NUL
// byte in the sniff window, or invalid UTF-8 anywhere.
func isBinaryContent(content string) bool {
	head := content
	if len(head) > binarySniffBytes {
		head = head[:binarySniffBytes]
	}
	if strings.ContainsRune(head, 0) {
		return true
	}
	return !utf8.ValidString(content)
}

// writeAppFileRequest is the editor's save payload. Modified carries the
// timestamp from the last read so a concurrent change can be detected.
type writeAppFileRequest struct {
	Path     string `json:"path" binding:"required"`
	Content  string `json:"content"`
	Modified int64  `json:"modified"`
}

// WriteAppFile saves a text file back into an app's persistent volume. PUT
// /projects/:projectId/environments/:envId/apps/:appName/volume/files/content
//
// @ID          writeAppFile
// @Summary     Write a text file into an app's persistent volume
// @Description Writes up to 1 MiB of text to a file, creating it when absent. The write is atomic (temp file plus rename), so a failed transfer never truncates the previous content. When "modified" is sent and the file changed since that timestamp, the write is refused with 409 instead of overwriting someone else's change.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                true "Project UUID"
// @Param       envId     path     string                true "Environment UUID"
// @Param       appName   path     string                true "App name"
// @Param       body      body     writeAppFileRequest   true "File path, content and the modified timestamp from the last read"
// @Success     200       {object} map[string]interface{} "object with path and modified"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/content [put]
func (h *Handler) WriteAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, true)
	if !ok {
		return
	}
	var req writeAppFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Content) > maxTextFileBytes {
		respondError(c, http.StatusRequestEntityTooLarge, "content is larger than 1 MiB")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), fsStreamTimeout)
	defer cancel()

	target, err := s.resolve(ctx, h.podFS, req.Path)
	if err != nil {
		status, msg := classifyFSError(err, "failed to write file")
		s.failFS(h, c, "WriteAppFile", req.Path, msg)
		respondError(c, status, msg)
		return
	}
	if req.Modified > 0 {
		if entry, err := h.podFS.Stat(ctx, s.target, target); err == nil {
			if entry.Kind == cloudtask.FileKindDir {
				respondError(c, http.StatusConflict, "path is a directory")
				return
			}
			if entry.ModTime > req.Modified {
				s.failFS(h, c, "WriteAppFile", req.Path, "changed_on_disk")
				respondError(c, http.StatusConflict, "the file changed on disk since it was opened")
				return
			}
		}
	}

	if err := h.podFS.WriteFile(ctx, s.target, target, strings.NewReader(req.Content)); err != nil {
		status, msg := classifyFSError(err, "failed to write file")
		s.failFS(h, c, "WriteAppFile", req.Path, msg)
		respondError(c, status, msg)
		return
	}

	modified := time.Now().Unix()
	if entry, err := h.podFS.Stat(ctx, s.target, target); err == nil {
		modified = entry.ModTime
	}
	h.auditFS(c, s, "WriteAppFile", auditOutcomeSuccess, map[string]any{
		"path":  s.relative(target),
		"bytes": len(req.Content),
	})
	c.JSON(http.StatusOK, gin.H{"path": s.relative(target), "modified": modified})
}

// failFS records a rejected mutation so a denied or failed write is as
// traceable as a successful one.
func (s *appFSSession) failFS(h *Handler, c *gin.Context, action, requestedPath, reason string) {
	h.auditFS(c, s, action, auditOutcomeFailure, map[string]any{
		"path":   requestedPath,
		"reason": reason,
	})
}

// DownloadAppFile streams a single file out of an app's persistent volume. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/files/raw
//
// @ID          downloadAppFile
// @Summary     Download a single file from an app's persistent volume
// @Description Streams one file straight out of a Running pod with no size limit and no intermediate storage. Directories are rejected with 409: use the archive endpoint instead.
// @Tags        app
// @Produce     octet-stream
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       envId     path     string true "Environment UUID"
// @Param       appName   path     string true "App name"
// @Param       path      query    string true "File path relative to the volume root"
// @Success     200       {file}   binary
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/raw [get]
func (h *Handler) DownloadAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsStreamTimeout)
	defer cancel()

	target, entry, err := s.resolveLeaf(ctx, h.podFS, c.Query("path"))
	if err != nil {
		status, msg := classifyFSError(err, "failed to download file")
		respondError(c, status, msg)
		return
	}
	if entry.Kind == cloudtask.FileKindDir {
		respondError(c, http.StatusConflict, "path is a directory: download it as an archive instead")
		return
	}

	h.auditFS(c, s, "DownloadAppFile", auditOutcomeSuccess, map[string]any{
		"path":  s.relative(target),
		"bytes": entry.Size,
	})
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", contentDispositionAttachment(path.Base(target)))
	if err := h.podFS.ReadFile(ctx, s.target, target, 0, c.Writer); err != nil {
		c.Abort()
	}
}

// DownloadAppDirectory streams a directory as a tar.gz. GET
// /projects/:projectId/environments/:envId/apps/:appName/volume/files/archive
//
// @ID          downloadAppDirectory
// @Summary     Download a directory from an app's persistent volume as tar.gz
// @Description Streams "tar czf" of one directory of the volume straight from a Running pod to the client, without staging it in object storage. Use the volume export endpoint for a whole-volume archive with a shareable link.
// @Tags        app
// @Produce     octet-stream
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       path      query    string false "Directory path relative to the volume root (default \"/\")"
// @Success     200       {file}   binary
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/archive [get]
func (h *Handler) DownloadAppDirectory(c *gin.Context) {
	s, ok := h.openAppFS(c, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsStreamTimeout)
	defer cancel()

	dir, entry, err := s.resolveLeaf(ctx, h.podFS, c.Query("path"))
	if err != nil {
		status, msg := classifyFSError(err, "failed to archive directory")
		respondError(c, status, msg)
		return
	}
	if entry.Kind != cloudtask.FileKindDir {
		respondError(c, http.StatusConflict, "path is not a directory")
		return
	}

	name := path.Base(dir)
	if dir == s.root {
		name = s.appName
	}
	filename := fmt.Sprintf("%s-%s.tar.gz", name, time.Now().UTC().Format("20060102-150405"))

	h.auditFS(c, s, "DownloadAppDirectory", auditOutcomeSuccess, map[string]any{"path": s.relative(dir)})
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", contentDispositionAttachment(filename))
	if err := h.podFS.TarDir(ctx, s.target, dir, c.Writer); err != nil {
		c.Abort()
	}
}

// contentDispositionAttachment builds a Content-Disposition header that
// survives non-ASCII names: a sanitised ASCII fallback plus the RFC 5987
// UTF-8 form browsers actually use.
func contentDispositionAttachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "download"
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, escapeURLPathSegment(name))
}

// escapeURLPathSegment percent-encodes everything outside the RFC 5987
// attr-char set, which url.PathEscape does not do (it leaves "&", "=" and
// others intact).
func escapeURLPathSegment(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
			ch == '-' || ch == '.' || ch == '_' || ch == '~' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[ch>>4])
		b.WriteByte(hex[ch&0x0f])
	}
	return b.String()
}

// UploadAppFile stores an uploaded file into an app's persistent volume. POST
// /projects/:projectId/environments/:envId/apps/:appName/volume/files/upload
//
// @ID          uploadAppFile
// @Summary     Upload a file into an app's persistent volume
// @Description Streams a multipart upload (field "file") straight into the target directory (field "path", default the volume root) without buffering it on the console. Writes are atomic: a failed transfer leaves any previous file intact. Maximum 100 MiB per file.
// @Tags        app
// @Accept      multipart/form-data
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true  "Project UUID"
// @Param       envId     path     string true  "Environment UUID"
// @Param       appName   path     string true  "App name"
// @Param       path      formData string false "Target directory relative to the volume root (default \"/\")"
// @Param       file      formData file   true  "File to upload"
// @Success     200       {object} map[string]interface{} "object with path and size"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/upload [post]
func (h *Handler) UploadAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, true)
	if !ok {
		return
	}
	header, err := c.FormFile("file")
	if err != nil {
		respondError(c, http.StatusBadRequest, "missing file")
		return
	}
	if header.Size > maxUploadBytes {
		s.failFS(h, c, "UploadAppFile", header.Filename, "too_large")
		respondError(c, http.StatusRequestEntityTooLarge, "file is larger than 100 MiB")
		return
	}
	name := path.Base(header.Filename)
	if name == "" || name == "." || name == "/" || strings.Contains(header.Filename, "\x00") {
		respondError(c, http.StatusBadRequest, "invalid file name")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), fsStreamTimeout)
	defer cancel()

	dir, err := s.resolve(ctx, h.podFS, c.PostForm("path"))
	if err != nil {
		status, msg := classifyFSError(err, "failed to upload file")
		s.failFS(h, c, "UploadAppFile", name, msg)
		respondError(c, status, msg)
		return
	}
	target, err := s.resolvePhysical(ctx, h.podFS, path.Join(dir, name))
	if err != nil {
		status, msg := classifyFSError(err, "failed to upload file")
		s.failFS(h, c, "UploadAppFile", name, msg)
		respondError(c, status, msg)
		return
	}

	src, err := header.Open()
	if err != nil {
		respondError(c, http.StatusBadRequest, "failed to read upload")
		return
	}
	defer src.Close()

	if err := h.podFS.WriteFile(ctx, s.target, target, io.LimitReader(src, maxUploadBytes)); err != nil {
		status, msg := classifyFSError(err, "failed to upload file")
		s.failFS(h, c, "UploadAppFile", name, msg)
		respondError(c, status, msg)
		return
	}

	h.auditFS(c, s, "UploadAppFile", auditOutcomeSuccess, map[string]any{
		"path":  s.relative(target),
		"bytes": header.Size,
	})
	c.JSON(http.StatusOK, gin.H{"path": s.relative(target), "size": header.Size})
}

// appFilePathRequest is the payload shared by the single-path mutations.
type appFilePathRequest struct {
	Path      string `json:"path" binding:"required"`
	Recursive bool   `json:"recursive"`
}

// CreateAppDirectory creates a directory inside an app's persistent volume.
// POST /projects/:projectId/environments/:envId/apps/:appName/volume/files/mkdir
//
// @ID          createAppDirectory
// @Summary     Create a directory inside an app's persistent volume
// @Description Creates a directory (parents included) inside the app's volume. Succeeds when it already exists.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string              true "Project UUID"
// @Param       envId     path     string              true "Environment UUID"
// @Param       appName   path     string              true "App name"
// @Param       body      body     appFilePathRequest  true "Directory path relative to the volume root"
// @Success     200       {object} map[string]interface{} "object with path"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/mkdir [post]
func (h *Handler) CreateAppDirectory(c *gin.Context) {
	s, ok := h.openAppFS(c, true)
	if !ok {
		return
	}
	var req appFilePathRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsMetadataTimeout)
	defer cancel()

	target, err := s.resolve(ctx, h.podFS, req.Path)
	if err != nil {
		status, msg := classifyFSError(err, "failed to create directory")
		s.failFS(h, c, "CreateAppDirectory", req.Path, msg)
		respondError(c, status, msg)
		return
	}
	if err := h.podFS.Mkdir(ctx, s.target, target); err != nil {
		status, msg := classifyFSError(err, "failed to create directory")
		s.failFS(h, c, "CreateAppDirectory", req.Path, msg)
		respondError(c, status, msg)
		return
	}
	h.auditFS(c, s, "CreateAppDirectory", auditOutcomeSuccess, map[string]any{"path": s.relative(target)})
	c.JSON(http.StatusOK, gin.H{"path": s.relative(target)})
}

// moveAppFileRequest is the rename/move payload; both paths are validated
// against the volume root independently.
type moveAppFileRequest struct {
	From string `json:"from" binding:"required"`
	To   string `json:"to" binding:"required"`
}

// MoveAppFile renames or moves an entry inside an app's persistent volume.
// POST /projects/:projectId/environments/:envId/apps/:appName/volume/files/move
//
// @ID          moveAppFile
// @Summary     Rename or move a file inside an app's persistent volume
// @Description Renames or moves a file or directory. Both paths must stay inside the volume. Refuses to overwrite an existing destination with 409.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string              true "Project UUID"
// @Param       envId     path     string              true "Environment UUID"
// @Param       appName   path     string              true "App name"
// @Param       body      body     moveAppFileRequest  true "Source and destination paths relative to the volume root"
// @Success     200       {object} map[string]interface{} "object with from and to"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/move [post]
func (h *Handler) MoveAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, true)
	if !ok {
		return
	}
	var req moveAppFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsMetadataTimeout)
	defer cancel()

	from, err := s.resolve(ctx, h.podFS, req.From)
	if err != nil {
		status, msg := classifyFSError(err, "failed to move file")
		s.failFS(h, c, "MoveAppFile", req.From, msg)
		respondError(c, status, msg)
		return
	}
	to, err := s.resolve(ctx, h.podFS, req.To)
	if err != nil {
		status, msg := classifyFSError(err, "failed to move file")
		s.failFS(h, c, "MoveAppFile", req.To, msg)
		respondError(c, status, msg)
		return
	}
	if from == s.root {
		respondError(c, http.StatusConflict, "the volume root cannot be moved")
		return
	}
	if err := h.podFS.Move(ctx, s.target, from, to); err != nil {
		status, msg := classifyFSError(err, "failed to move file")
		s.failFS(h, c, "MoveAppFile", req.From, msg)
		respondError(c, status, msg)
		return
	}
	h.auditFS(c, s, "MoveAppFile", auditOutcomeSuccess, map[string]any{
		"from": s.relative(from),
		"to":   s.relative(to),
	})
	c.JSON(http.StatusOK, gin.H{"from": s.relative(from), "to": s.relative(to)})
}

// DeleteAppFile removes a file or directory from an app's persistent volume.
// POST /projects/:projectId/environments/:envId/apps/:appName/volume/files/delete
//
// @ID          deleteAppFile
// @Summary     Delete a file or directory from an app's persistent volume
// @Description Deletes a file, an empty directory, or — with "recursive": true — a directory and everything under it. The volume root itself cannot be deleted. Irreversible: the volume snapshot schedule is the only recovery path.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string              true "Project UUID"
// @Param       envId     path     string              true "Environment UUID"
// @Param       appName   path     string              true "App name"
// @Param       body      body     appFilePathRequest  true "Path relative to the volume root, plus recursive for directories"
// @Success     200       {object} map[string]interface{} "object with path"
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     409       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/volume/files/delete [post]
func (h *Handler) DeleteAppFile(c *gin.Context) {
	s, ok := h.openAppFS(c, true)
	if !ok {
		return
	}
	var req appFilePathRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), fsStreamTimeout)
	defer cancel()

	target, err := s.resolve(ctx, h.podFS, req.Path)
	if err != nil {
		status, msg := classifyFSError(err, "failed to delete")
		s.failFS(h, c, "DeleteAppFile", req.Path, msg)
		respondError(c, status, msg)
		return
	}
	if target == s.root {
		s.failFS(h, c, "DeleteAppFile", req.Path, "volume_root")
		respondError(c, http.StatusConflict, "the volume root cannot be deleted")
		return
	}
	if err := h.podFS.Remove(ctx, s.target, target, req.Recursive); err != nil {
		status, msg := classifyFSError(err, "failed to delete")
		s.failFS(h, c, "DeleteAppFile", req.Path, msg)
		respondError(c, status, msg)
		return
	}
	h.auditFS(c, s, "DeleteAppFile", auditOutcomeSuccess, map[string]any{
		"path":      s.relative(target),
		"recursive": req.Recursive,
	})
	c.JSON(http.StatusOK, gin.H{"path": s.relative(target)})
}
