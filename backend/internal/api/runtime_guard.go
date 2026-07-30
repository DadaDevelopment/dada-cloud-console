package api

import (
	"context"
	"net/http"

	"github.com/dada-tuda/console/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// envRuntime returns the deployment substrate (k8s, vm or box) of an environment
// scoped to its project. It is the single source of truth for runtime guards so
// compose-only and Kubernetes-only endpoints reject a mismatched app with a
// clear 400 instead of enqueuing an operation the worker cannot honour.
//
// 'box' is a third value (migration 061, D1): a Dada Box owns one environment
// row, and crystallization promotes that row to 'vm' in place. Because both
// require* guards below compare for EQUALITY with the runtime they want, a box
// environment is already rejected by every app endpoint with a 400 — which is
// correct: a box has no Helm release and no compose stack to act on. The guards
// that had to be changed for 'box' were the ones with a *default arm*, i.e. the
// ones that treated "not vm" as "therefore k8s". See
// valuesFileAllowedForRuntime below.
func (h *Handler) envRuntime(ctx context.Context, projectID, envID uuid.UUID) (models.EnvironmentRuntime, error) {
	var rt models.EnvironmentRuntime
	err := h.pool.QueryRow(ctx,
		`SELECT runtime FROM environments WHERE id = $1 AND project_id = $2`,
		envID, projectID,
	).Scan(&rt)
	return rt, err
}

// requireVMRuntime responds and returns false unless the environment runs the
// compose (VM) substrate. Compose-only endpoints (rollback/restart/adopt) call
// it after the write-role check and abort on false.
func (h *Handler) requireVMRuntime(c *gin.Context, projectID, envID uuid.UUID) bool {
	return h.requireRuntime(c, projectID, envID, models.EnvironmentRuntimeVM,
		"this action is only supported for VM (compose) apps")
}

// requireK8sRuntime is the inverse guard for Kubernetes-only endpoints such as
// the resource profile and the Helm values editor.
func (h *Handler) requireK8sRuntime(c *gin.Context, projectID, envID uuid.UUID) bool {
	return h.requireRuntime(c, projectID, envID, models.EnvironmentRuntimeK8s,
		"this action is only supported for Kubernetes apps")
}

// valuesFileAllowedForRuntime reports whether a config file is editable for the
// given runtime: Kubernetes apps own values.yaml, compose (VM) apps own
// compose.yaml and .env, and a box owns NEITHER. Cross-runtime files are rejected
// so the editor never commits a file the deploy path ignores.
//
// The box arm is explicit rather than falling through to the k8s arm. Before
// 'box' existed the final return read "anything that is not vm edits
// values.yaml", which would have handed a box environment a values-token for a
// Helm chart that does not exist — a signed token to write a git file nothing
// reads. A box's configuration is its image plus its injected env, and both are
// changed through the box API, not through the file editor.
func valuesFileAllowedForRuntime(rt models.EnvironmentRuntime, file string) bool {
	switch rt {
	case models.EnvironmentRuntimeVM:
		return file == "compose.yaml" || file == ".env"
	case models.EnvironmentRuntimeBox:
		return false
	default:
		return file == "values.yaml"
	}
}

// valuesFileRuntimeMsg is the 400 message explaining which files a runtime owns.
func valuesFileRuntimeMsg(rt models.EnvironmentRuntime) string {
	switch rt {
	case models.EnvironmentRuntimeVM:
		return "VM (compose) apps edit compose.yaml or .env, not values.yaml"
	case models.EnvironmentRuntimeBox:
		return "a box has no editable config file; change its image or its env through the box API"
	default:
		return "Kubernetes apps edit values.yaml, not compose.yaml or .env"
	}
}

func (h *Handler) requireRuntime(c *gin.Context, projectID, envID uuid.UUID, want models.EnvironmentRuntime, msg string) bool {
	rt, err := h.envRuntime(c.Request.Context(), projectID, envID)
	if err == pgx.ErrNoRows {
		respondNotFound(c)
		return false
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to load environment runtime")
		return false
	}
	if rt != want {
		respondError(c, http.StatusBadRequest, msg)
		return false
	}
	return true
}
