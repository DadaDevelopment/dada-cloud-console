package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// inferenceClient is the HTTP client used to talk to in-cluster KServe.
// Timeout matches a reasonable upper bound for synchronous inference; tunable
// via INFERENCE_TIMEOUT env var if we ever need it. Kept small and shared.
var inferenceClient = &http.Client{Timeout: 60 * time.Second}

// kserveProtocolForModelType picks v1 vs v2 inference protocol path based on
// model type. KServe ships predictors with both endpoints exposed for some
// types and only one for others (D14 says transparent passthrough, so we
// resolve at request time rather than baking into the renderer).
//
//	v1: <host>/v1/models/<name>:predict
//	v2: <host>/v2/models/<name>/infer
func kserveProtocolForModelType(modelType string) (path string, isV2 bool) {
	switch modelType {
	case "sklearn", "xgboost", "lightgbm", "pytorch", "tensorflow":
		return "v1", false
	default:
		// huggingface / triton / custom default to v2.
		return "v2", true
	}
}

// kserveURL composes the in-cluster URL the proxy will hit. Mirrors the
// host KServe builds for InferenceService predictors: <name>-predictor.<ns>.
// We always go through the cluster service, never the public domain.
func kserveURL(modelName, namespace, modelType string) string {
	_, isV2 := kserveProtocolForModelType(modelType)
	host := fmt.Sprintf("http://%s-predictor.%s.svc.cluster.local", modelName, namespace)
	if isV2 {
		return fmt.Sprintf("%s/v2/models/%s/infer", host, modelName)
	}
	return fmt.Sprintf("%s/v1/models/%s:predict", host, modelName)
}

// ProxyInference forwards a request to the in-cluster KServe predictor for
// this model and increments the advisory monthly call counter on success.
// Authorisation is the caller's session JWT (project membership). The model's
// own API key is server-side only and not exposed to browsers (NFR-002, D8).
//
// @ID          inferModel
// @Summary     Run inference against a deployed model (playground)
// @Description Proxies an inference request to the in-cluster KServe predictor for a deployed AI model and returns the prediction. The request and response bodies are passed through transparently (KServe v1 or v2 protocol depending on model type). Playground use only — production traffic should go through the model's public endpoint. The model must be Ready (otherwise 503).
// @Tags        model
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       projectId path     string                 true "Project UUID"
// @Param       envId     path     string                 true "Environment UUID"
// @Param       name      path     string                 true "Model name"
// @Param       body      body     map[string]interface{} true "Inference payload (KServe v1/v2 predict request, passed through verbatim)"
// @Success     200       {object} map[string]interface{} "the upstream predictor response, passed through verbatim"
// @Failure     401       {object} map[string]string
// @Failure     404       {object} map[string]string
// @Failure     413       {object} map[string]string
// @Failure     502       {object} map[string]string
// @Failure     503       {object} map[string]string
// @Router      /projects/{projectId}/environments/{envId}/models/{name}/infer [post]
func (h *Handler) ProxyInference(c *gin.Context) {
	startedAt := time.Now()
	claims, ok := auth.GetClaims(c)
	if !ok {
		respondUnauthorized(c)
		return
	}

	projectID, envID, ok := h.parseProjectEnv(c)
	if !ok {
		return
	}
	name := c.Param("name")
	if name == "" {
		respondNotFound(c)
		return
	}

	role, err := h.getUserProjectRole(c.Request.Context(), claims.UserID, projectID, claims.Groups)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to check project membership")
		return
	}
	_ = role // any project member can call the playground

	// Look up the snapshot to get model type + namespace + status. Refusing
	// inference until status is Ready prevents wasted timeouts and gives the
	// UI a clear "Model is starting" state.
	var modelType, status string
	var envNamespace, projectName, envName string
	err = h.pool.QueryRow(c.Request.Context(), `
		SELECT
		  COALESCE(rs.summary_json->>'model_type', ''),
		  COALESCE(rs.summary_json->>'status', ''),
		  e.namespace, p.name, e.name
		FROM resource_snapshots rs
		JOIN projects p     ON p.id = rs.project_id
		JOIN environments e ON e.id = rs.environment_id
		WHERE rs.project_id = $1
		  AND rs.environment_id = $2
		  AND rs.kind = 'AIModel'
		  AND rs.name = $3`,
		projectID, envID, name,
	).Scan(&modelType, &status, &envNamespace, &projectName, &envName)
	if errors.Is(err, pgx.ErrNoRows) {
		respondNotFound(c)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to resolve model")
		return
	}
	if status != "" && !strings.EqualFold(status, "Ready") {
		respondError(c, http.StatusServiceUnavailable, "model is not ready for inference")
		return
	}

	target := kserveURL(name, envNamespace, modelType)
	parsed, err := url.Parse(target)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "invalid kserve url")
		return
	}

	// Read the body once with a hard cap so a hostile client can't OOM the
	// backend by streaming a giant payload through the playground. The cap
	// is configurable (INFERENCE_MAX_BODY_BYTES, default 10MB). The same
	// cap is applied to the upstream response below — if a model returns
	// something larger we'd rather 502 than hold it in memory.
	maxBytes := h.cfg.InferenceMaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(c, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", maxBytes))
			return
		}
		respondError(c, http.StatusBadRequest, "failed to read request body")
		return
	}

	upstream, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "failed to build upstream request")
		return
	}
	// Forward Content-Type so KServe handles JSON / multipart correctly.
	if ct := c.GetHeader("Content-Type"); ct != "" {
		upstream.Header.Set("Content-Type", ct)
	} else {
		upstream.Header.Set("Content-Type", "application/json")
	}
	if accept := c.GetHeader("Accept"); accept != "" {
		upstream.Header.Set("Accept", accept)
	}

	resp, err := inferenceClient.Do(upstream)
	if err != nil {
		log.Warn().Err(err).Str("target", target).Msg("inference proxy: upstream error")
		respondError(c, http.StatusBadGateway, "upstream KServe predictor unreachable")
		return
	}
	defer resp.Body.Close()

	// Cap the upstream response too — same reasoning as the request cap.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		respondError(c, http.StatusBadGateway, "failed to read upstream response")
		return
	}
	if int64(len(respBody)) > maxBytes {
		respondError(c, http.StatusBadGateway,
			fmt.Sprintf("upstream response exceeds %d bytes", maxBytes))
		return
	}

	// Increment the monthly counter on a 2xx (D12: advisory only — failures
	// don't count against the budget). UPSERT on the composite PK so a
	// missing row is created on the first call of the month.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		h.bumpInferenceCounter(c, projectID, envID, name)
	}

	// One structured line per call so operators can see traffic without a
	// metrics pipeline. Body sizes are bytes; latency is milliseconds.
	log.Info().
		Str("model", name).
		Str("project", projectName).
		Str("env", envName).
		Str("model_type", modelType).
		Int("upstream_status", resp.StatusCode).
		Int("req_bytes", len(body)).
		Int("resp_bytes", len(respBody)).
		Int64("latency_ms", time.Since(startedAt).Milliseconds()).
		Msg("inference proxy: request completed")

	// Pass content-type through so the browser renders the response correctly.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Writer.Header().Set("Content-Type", ct)
	}
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(respBody)
}

// bumpInferenceCounter is fire-and-forget — counter inaccuracy is preferable
// to failing a successful inference. Logs at warn level so anomalies are
// visible without paging anyone.
func (h *Handler) bumpInferenceCounter(c *gin.Context, projectID, envID uuid.UUID, name string) {
	yearMonth := time.Now().UTC().Format("2006-01")
	_, err := h.pool.Exec(c.Request.Context(), `
		INSERT INTO aimodel_inference_counters
		    (project_id, year_month, aimodel_name, environment_id, call_count, last_called_at)
		VALUES ($1, $2, $3, $4, 1, NOW())
		ON CONFLICT (project_id, year_month, aimodel_name, environment_id)
		DO UPDATE SET call_count = aimodel_inference_counters.call_count + 1,
		              last_called_at = NOW()`,
		projectID, yearMonth, name, envID,
	)
	if err != nil {
		log.Warn().Err(err).Str("model", name).Msg("inference counter bump failed (advisory)")
	}
}
