package api

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// artifactProxyClient streams artifact bytes from Nexus. No timeout: APK/AAB can
// be large and downloads run as long as the client keeps reading (bounded by the
// request context).
var artifactProxyClient = &http.Client{}

// buildArtifact mirrors a build_artifacts row (mobile delivery, ADR-010).
type buildArtifact struct {
	ID          uuid.UUID `json:"id"`
	BuildID     uuid.UUID `json:"build_id"`
	Type        string    `json:"type"` // apk | aab
	Size        int64     `json:"size"`
	VersionCode *int      `json:"version_code,omitempty"`
	SHA256      *string   `json:"sha256,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

const artifactSelectCols = `id, build_id, type, size, version_code, sha256, created_at`

func scanArtifact(s interface{ Scan(dest ...any) error }, a *buildArtifact) error {
	return s.Scan(&a.ID, &a.BuildID, &a.Type, &a.Size, &a.VersionCode, &a.SHA256, &a.CreatedAt)
}

// ListBuildArtifacts returns the APK/AAB artifacts recorded for a build. The
// nexus_url is deliberately NOT exposed — clients download via the proxy below.
//
// @ID          listBuildArtifacts
// @Summary     List build artifacts
// @Description Returns the Android artifacts (APK/AAB) produced by a build. Read-only.
// @Tags        build
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string true "Project UUID"
// @Param       buildId   path     string true "Build UUID"
// @Success     200       {object} map[string]interface{} "object with an artifacts array"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Router      /projects/{projectId}/builds/{buildId}/artifacts [get]
func (h *Handler) ListBuildArtifacts(c *gin.Context) {
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	buildID, err := uuid.Parse(c.Param("buildId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	// Confirm the build belongs to this project (don't leak existence).
	var b build
	if err := h.loadProjectBuild(c, projectID, buildID, &b); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load build")
		return
	}

	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT `+artifactSelectCols+`
		 FROM build_artifacts
		 WHERE build_id = $1
		 ORDER BY type`,
		buildID,
	)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to query artifacts")
		return
	}
	defer rows.Close()

	artifacts := []buildArtifact{}
	for rows.Next() {
		var a buildArtifact
		if err := scanArtifact(rows, &a); err != nil {
			respondError(c, http.StatusInternalServerError, "failed to scan artifact")
			return
		}
		artifacts = append(artifacts, a)
	}
	if err := rows.Err(); err != nil {
		respondError(c, http.StatusInternalServerError, "error reading artifacts")
		return
	}

	c.JSON(http.StatusOK, gin.H{"artifacts": artifacts})
}

// DownloadBuildArtifact streams an artifact's bytes by proxying its Nexus raw
// URL with server-side Nexus credentials. The Nexus URL and creds never reach
// the client. Org/project isolation is enforced via the build join.
//
// @ID          downloadBuildArtifact
// @Summary     Download a build artifact
// @Description Streams the APK/AAB bytes through the backend (Nexus creds stay server-side). Read-only.
// @Tags        build
// @Produce     application/octet-stream
// @Security    BearerAuth
// @Param       projectId  path string true "Project UUID"
// @Param       buildId    path string true "Build UUID"
// @Param       artifactId path string true "Artifact UUID"
// @Success     200 {file} binary
// @Failure     401 {object} map[string]string
// @Failure     404 {object} map[string]string
// @Failure     503 {object} map[string]string
// @Router      /projects/{projectId}/builds/{buildId}/artifacts/{artifactId}/download [get]
func (h *Handler) DownloadBuildArtifact(c *gin.Context) {
	if h.cfg.NexusRawURL == "" {
		respondError(c, http.StatusServiceUnavailable, "artifact download not configured")
		return
	}

	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, err := uuid.Parse(c.Param("projectId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	buildID, err := uuid.Parse(c.Param("buildId"))
	if err != nil {
		respondNotFound(c)
		return
	}
	artifactID, err := uuid.Parse(c.Param("artifactId"))
	if err != nil {
		respondNotFound(c)
		return
	}

	if _, err := h.effectiveRole(c.Request.Context(), claims, projectID); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}

	// Confirm the build belongs to this project, then resolve the artifact and
	// its private Nexus URL in one isolation-checked query.
	var b build
	if err := h.loadProjectBuild(c, projectID, buildID, &b); err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	} else if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load build")
		return
	}

	var (
		nexusURL  string
		aType     string
		aSize     int64
	)
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT nexus_url, type, size FROM build_artifacts WHERE id = $1 AND build_id = $2`,
		artifactID, buildID,
	).Scan(&nexusURL, &aType, &aSize)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load artifact")
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, nexusURL, nil)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to build upstream request")
		return
	}
	if h.cfg.NexusUser != "" {
		req.SetBasicAuth(h.cfg.NexusUser, h.cfg.NexusToken)
	}

	resp, err := artifactProxyClient.Do(req)
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to reach artifact store")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respondError(c, http.StatusBadGateway, "artifact store returned an error")
		return
	}

	filename := b.AppName + "-" + buildID.String()[:8] + "." + aType
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.Header("Content-Type", "application/octet-stream")
	if resp.ContentLength > 0 {
		c.Header("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	} else if aSize > 0 {
		c.Header("Content-Length", strconv.FormatInt(aSize, 10))
	}

	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, resp.Body)
}
