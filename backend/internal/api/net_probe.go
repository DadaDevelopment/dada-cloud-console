package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/dada-tuda/console/backend/internal/cloudtask"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

const netProbeTimeout = 30 * time.Second

const netProbeMaxTargetLen = 253

// netProbeBlockedSuffixes are hostname suffixes that name something internal
// to the platform rather than an external endpoint the app is trying to
// reach. Rejecting these does not change what the app's own network path can
// already reach (the probe runs inside the app's own pod, with the app's own
// network visibility) -- it exists so a tool named "network probe" cannot be
// pointed at the platform's own internals by name, as a matter of hygiene.
var netProbeBlockedSuffixes = []string{
	".svc.cluster.local",
	".cluster.local",
	".svc",
}

type netProbeRequest struct {
	Target   string `json:"target"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// validateNetProbeRequest checks a probe request before anything touches
// Kubernetes. target must be a bare hostname or IP literal, never a URL: no
// scheme, no path, no userinfo, no whitespace. IP literals in private,
// loopback, link-local (including the 169.254.169.254 cloud metadata address)
// or multicast ranges are rejected, and so are the platform's own internal
// DNS suffixes.
func validateNetProbeRequest(req netProbeRequest) (spec cloudtask.ProbeSpec, err error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return spec, errNetProbe("target is required")
	}
	if len(target) > netProbeMaxTargetLen {
		return spec, errNetProbe("target is too long")
	}
	if strings.ContainsAny(target, " \t\r\n/@\\?#") {
		return spec, errNetProbe("target must be a bare hostname or IP, not a URL")
	}
	if strings.Contains(target, "://") {
		return spec, errNetProbe("target must be a bare hostname or IP, not a URL")
	}
	lower := strings.ToLower(target)
	for _, suffix := range netProbeBlockedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return spec, errNetProbe("target names an internal platform address, not an external endpoint")
		}
	}
	if ip := net.ParseIP(target); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return spec, errNetProbe("target must not be a private, loopback or link-local address")
		}
		if ip.String() == "169.254.169.254" {
			return spec, errNetProbe("target must not be a cloud metadata address")
		}
	}

	if req.Port < 1 || req.Port > 65535 {
		return spec, errNetProbe("port must be between 1 and 65535")
	}

	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		if req.Port == 443 {
			protocol = "tls"
		} else {
			protocol = "tcp"
		}
	}
	switch protocol {
	case "tcp", "tls", "http":
	default:
		return spec, errNetProbe("protocol must be one of tcp, tls, http")
	}

	return cloudtask.ProbeSpec{Target: target, Port: req.Port, Protocol: protocol}, nil
}

type netProbeValidationError struct{ msg string }

func (e *netProbeValidationError) Error() string { return e.msg }

func errNetProbe(msg string) error { return &netProbeValidationError{msg: msg} }

// ProbeAppNetwork execs a fixed DNS/TCP/TLS/HTTP diagnostic sequence inside an
// ephemeral debug container attached to a running app pod, so the assistant
// (or a human) can check network reachability from exactly where the app
// itself sits, independent of what the app's own container image contains.
// POST /projects/:projectId/environments/:envId/apps/:appName/net-probe
//
// @ID          probeAppNetwork
// @Summary     Probe network connectivity from an app's own running pod
// @Description Execs a fixed DNS resolve, TCP connect and (protocol tls/http) TLS handshake or HTTP request against a caller-supplied host and port, from inside an ephemeral debug container attached to the app's own running pod. Runs regardless of what the app's own image contains (no shell or network tools required in the app image). Target must be a bare external hostname or IP; private/loopback/link-local addresses and the platform's own internal DNS suffixes are rejected. 400 on an invalid target/port/protocol. 404 when the environment or app does not exist. 502 when no running pod is found or the exec fails. 503 when network probing is not configured for this environment.
// @Tags        app
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string          true "Project UUID"
// @Param       envId     path     string          true "Environment UUID"
// @Param       appName   path     string          true "App name"
// @Param       body      body     netProbeRequest true "Probe target"
// @Success     200       {object} cloudtask.ProbeResult
// @Failure     400       {object} map[string]string
// @Failure     401       {object} map[string]string
// @Failure     403       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/apps/{appName}/net-probe [post]
func (h *Handler) ProbeAppNetwork(c *gin.Context) {
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

	var req netProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	spec, err := validateNetProbeRequest(req)
	if err != nil {
		respondError(c, http.StatusBadRequest, err.Error())
		return
	}

	reject := func(status int, reason string, respond func()) {
		h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
			ProjectID:     projectID,
			EnvironmentID: envID,
			Action:        "ProbeAppNetwork",
			ResourceKind:  "App",
			ResourceName:  appName,
			Outcome:       auditOutcomeFailure,
			Metadata:      map[string]any{"reason": reason, "status": status, "target": spec.Target, "port": spec.Port},
		})
		respond()
	}
	rejectErr := func(status int, reason, msg string) {
		reject(status, reason, func() { respondError(c, status, msg) })
	}

	if !h.podProber.Enabled() {
		rejectErr(http.StatusServiceUnavailable, "prober_disabled", "network probing is not configured for this environment")
		return
	}
	if !h.requireK8sRuntime(c, projectID, envID) {
		return
	}

	var namespace string
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT namespace FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&namespace)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "environment_not_found", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "environment_load_failed", "failed to load environment")
		return
	}
	if namespace == "" {
		rejectErr(http.StatusConflict, "no_namespace", "environment has no namespace")
		return
	}

	var exists bool
	err = h.pool.QueryRow(c.Request.Context(),
		`SELECT true FROM resource_snapshots
		 WHERE project_id = $1 AND environment_id = $2 AND kind = 'App' AND name = $3`,
		projectID, envID, appName,
	).Scan(&exists)
	if err == pgx.ErrNoRows {
		reject(http.StatusNotFound, "app_not_found", func() { respondNotFound(c) })
		return
	}
	if err != nil {
		rejectErr(http.StatusInternalServerError, "app_load_failed", "failed to load app")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), netProbeTimeout)
	defer cancel()

	podName, _, err := h.podProber.FindRunningPod(ctx, namespace, appName)
	if err != nil {
		rejectErr(http.StatusBadGateway, "no_running_pod", "no running pod found for this app: "+err.Error())
		return
	}

	result, err := h.podProber.RunNetworkProbe(ctx, namespace, podName, spec)
	if err != nil {
		rejectErr(http.StatusBadGateway, "probe_failed", "network probe failed: "+err.Error())
		return
	}

	h.recordAudit(c.Request.Context(), claims.UserID, auditEntry{
		ProjectID:     projectID,
		EnvironmentID: envID,
		Action:        "ProbeAppNetwork",
		ResourceKind:  "App",
		ResourceName:  appName,
		Outcome:       auditOutcomeSuccess,
		Metadata: map[string]any{
			"target": spec.Target, "port": spec.Port, "protocol": spec.Protocol,
			"dns_ok": result.DNS.Ok, "tcp_ok": result.TCP.Ok, "tls_ok": result.TLS.Ok,
		},
	})

	c.JSON(http.StatusOK, result)
}
