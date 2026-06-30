package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/logsearch"
	"github.com/dada-tuda/console/backend/internal/prometheus"
	"github.com/dada-tuda/console/backend/internal/telemetry"
	"github.com/rs/zerolog/log"
)

const (
	// maxBodyBytes bounds an ingest request body (defense against memory abuse on
	// the public write plane). 8 MiB comfortably fits batched OTLP exports.
	maxBodyBytes = 8 << 20
	maxSourceLen = 200
)

// Config carries the gateway's tunables, wired from internal/config in main.
type Config struct {
	MaxLabels       int // client attribute-labels merged per series (cardinality guard)
	MaxSeriesPerReq int // total series accepted in one request (413 above this)
	RateLimitPerMin int // per-app token bucket
	MaxMessageBytes int // per-log message cap
}

// Server is the stateless ingest HTTP handler set.
type Server struct {
	store     keyStore
	promwrite *prometheus.WriteClient // nil when remote-write unconfigured -> 503
	eswrite   *logsearch.WriteClient  // nil when ES unconfigured -> 503
	limiter   *telemetry.IngestLimiter
	cfg       Config
	ping      func(context.Context) error // optional DB liveness probe for /readyz
}

// SetDBPinger wires a database liveness check used by /readyz. Postgres is the
// gateway's only hard dependency (key verify + tenant resolve); without it every
// ingest request 503s, so readiness must reflect it. main passes pool.Ping.
func (s *Server) SetDBPinger(fn func(context.Context) error) { s.ping = fn }

// NewServer builds the gateway handler set. promwrite/eswrite may be nil; the
// corresponding ingest path then returns 503 (mirrors the console).
func NewServer(store keyStore, promwrite *prometheus.WriteClient, eswrite *logsearch.WriteClient, cfg Config) *Server {
	if cfg.MaxLabels <= 0 {
		cfg.MaxLabels = 30
	}
	if cfg.MaxSeriesPerReq <= 0 {
		cfg.MaxSeriesPerReq = 2000
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = 32 * 1024
	}
	return &Server{
		store:     store,
		promwrite: promwrite,
		eswrite:   eswrite,
		limiter:   telemetry.NewIngestLimiter(cfg.RateLimitPerMin),
		cfg:       cfg,
	}
}

// Handler returns the gateway's HTTP router. OTLP paths follow the OTLP/HTTP
// spec (/v1/metrics, /v1/logs) so a stock OTel exporter works with only an
// endpoint + key header; the appId is resolved from the key, not the path.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	// OTLP/HTTP (protobuf + json).
	mux.HandleFunc("/v1/metrics", s.handleOTLPMetrics)
	mux.HandleFunc("/v1/logs", s.handleOTLPLogs)
	// Bespoke DADA JSON (back-compat), appId from key.
	mux.HandleFunc("/api/v1/metrics", s.handleJSONMetrics)
	mux.HandleFunc("/api/v1/logs", s.handleJSONLogs)
	return recoverAndLog(mux)
}

// statusRecorder captures the response status for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// recoverAndLog wraps the mux: it recovers panics into a 500 (a panic on the
// public write plane must never take the process down) and emits one structured
// access line per request. Health probes are not logged (noise).
func recoverAndLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				log.Error().Interface("panic", v).Str("path", r.URL.Path).Msg("gateway handler panic")
				if rec.status == http.StatusOK {
					writeJSON(rec, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				}
			}
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				log.Info().Str("method", r.Method).Str("path", r.URL.Path).
					Int("status", rec.status).Dur("dur", time.Since(start)).Msg("ingest")
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz reports ready only when a forward target is wired AND Postgres is
// reachable (the auth dependency). Used as the k8s readiness gate.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.promwrite == nil && s.eswrite == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no forward targets configured"})
		return
	}
	if s.ping != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unreachable"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// authn resolves and authorizes the request: extracts the dmon_ key, looks up
// the tenant, checks the required scope, and applies the per-app rate limit.
// On failure it writes the status and returns ok=false.
func (s *Server) authn(w http.ResponseWriter, r *http.Request, scope string) (resolved, bool) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return resolved{}, false
	}
	key := telemetry.KeyFromHeaders(r.Header.Get("X-API-Key"), r.Header.Get("Authorization"))
	res, err := resolveKey(r.Context(), s.store, key, s.cfg.MaxLabels)
	if err != nil {
		if errors.Is(err, errKeyUnknown) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid api key"})
			return resolved{}, false
		}
		// DB outage or query error — not the client's fault. 503, and log it so
		// an auth-path Postgres failure is visible (it would otherwise look like a
		// flood of 401s).
		log.Error().Err(err).Msg("gateway key resolution failed")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth backend unavailable"})
		return resolved{}, false
	}
	if !res.hasScope(scope) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing required scope: " + scope})
		return resolved{}, false
	}
	if !s.limiter.Allow(res.appID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return resolved{}, false
	}
	return res, true
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return nil, false
	}
	if len(body) > maxBodyBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
		return nil, false
	}
	return body, true
}

// handleOTLPMetrics decodes an OTLP/HTTP metrics export and forwards it to
// Prometheus remote-write with authoritative tenant labels.
func (s *Server) handleOTLPMetrics(w http.ResponseWriter, r *http.Request) {
	res, ok := s.authn(w, r, "metrics:write")
	if !ok {
		return
	}
	if s.promwrite == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metrics ingestion not configured"})
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	data, err := telemetry.DecodeMetrics(r.Header.Get("Content-Type"), body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	series, dropped := telemetry.MetricsToSeries(data, res.tenant)
	if dropped > 0 {
		log.Warn().Int("dropped", dropped).Str("app", res.appID.String()).
			Msg("dropped unsupported OTLP metric points (exponential histogram / summary)")
	}
	if len(series) > s.cfg.MaxSeriesPerReq {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too many series in one request"})
		return
	}
	// Mimir tenant = project_id (X-Scope-OrgID). Per-project is the real isolation
	// boundary: the single-org collapse made owner_id identical across nearly all
	// projects, so an org-level tenant would fold every project into one Mimir
	// tenant (no per-project retention/limits/query isolation). The console read
	// path stamps the same project_id tenant (see monitoringReadTenant). org_id
	// stays a LABEL on each series for back-compat.
	if err := s.promwrite.Write(r.Context(), res.tenant.ProjectID, series); err != nil {
		log.Error().Err(err).Str("app", res.appID.String()).Msg("prometheus remote-write failed")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "remote-write failed: " + err.Error()})
		return
	}
	otlpSuccess(w, r.Header.Get("Content-Type"))
}

// handleOTLPLogs decodes an OTLP/HTTP logs export and forwards it to ES.
func (s *Server) handleOTLPLogs(w http.ResponseWriter, r *http.Request) {
	res, ok := s.authn(w, r, "logs:write")
	if !ok {
		return
	}
	if s.eswrite == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "log ingestion not configured"})
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	data, err := telemetry.DecodeLogs(r.Header.Get("Content-Type"), body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	docs := telemetry.LogsToAppLogs(data, res.tenant)
	if len(docs) > s.cfg.MaxSeriesPerReq {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too many log records in one request"})
		return
	}
	for i := range docs {
		if len(docs[i].Message) > s.cfg.MaxMessageBytes {
			docs[i].Message = docs[i].Message[:s.cfg.MaxMessageBytes]
		}
		if err := s.eswrite.Index(r.Context(), docs[i]); err != nil {
			log.Error().Err(err).Str("app", res.appID.String()).Msg("elasticsearch log index failed")
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "log write failed: " + err.Error()})
			return
		}
	}
	otlpSuccess(w, r.Header.Get("Content-Type"))
}

// ---- bespoke DADA JSON (back-compat), appId resolved from key ----

type jsonMetricsRequest struct {
	Timestamp string             `json:"timestamp"`
	Source    string             `json:"source"`
	Metrics   map[string]float64 `json:"metrics"`
}

type jsonLogsRequest struct {
	Source  string `json:"source"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func (s *Server) handleJSONMetrics(w http.ResponseWriter, r *http.Request) {
	res, ok := s.authn(w, r, "metrics:write")
	if !ok {
		return
	}
	if s.promwrite == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metrics ingestion not configured"})
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req jsonMetricsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if len(req.Metrics) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metrics is required and must be non-empty"})
		return
	}
	if len(req.Metrics) > s.cfg.MaxSeriesPerReq {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too many metrics in one request"})
		return
	}
	if len(req.Source) > maxSourceLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source too long"})
		return
	}
	tsMS := time.Now().UnixMilli()
	if req.Timestamp != "" {
		if parsed, err := time.Parse(time.RFC3339, req.Timestamp); err == nil {
			tsMS = parsed.UnixMilli()
		}
	}
	t := res.tenant
	series := make([]prometheus.WriteSeries, 0, len(req.Metrics))
	for name, value := range req.Metrics {
		series = append(series, prometheus.WriteSeries{
			Labels: map[string]string{
				"__name__":       telemetry.SanitizeMetricName(name),
				"org_id":         t.OrgID,
				"project_id":     t.ProjectID,
				"environment":    t.Environment,
				"source":         req.Source,
				"monitoring_app": t.MonitoringApp,
			},
			Value:       value,
			TimestampMS: tsMS,
		})
	}
	if err := s.promwrite.Write(r.Context(), t.ProjectID, series); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "remote-write failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"ingested": len(series)})
}

func (s *Server) handleJSONLogs(w http.ResponseWriter, r *http.Request) {
	res, ok := s.authn(w, r, "logs:write")
	if !ok {
		return
	}
	if s.eswrite == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "log ingestion not configured"})
		return
	}
	body, ok := readBody(w, r)
	if !ok {
		return
	}
	var req jsonLogsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if len(req.Message) > s.cfg.MaxMessageBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "message too long"})
		return
	}
	if len(req.Source) > maxSourceLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source too long"})
		return
	}
	t := res.tenant
	if err := s.eswrite.Index(r.Context(), logsearch.AppLog{
		Timestamp:     time.Now(),
		Source:        req.Source,
		Level:         strings.ToUpper(strings.TrimSpace(req.Level)),
		Message:       req.Message,
		OrgID:         t.OrgID,
		ProjectID:     t.ProjectID,
		Environment:   t.Environment,
		MonitoringApp: t.MonitoringApp,
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "log write failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"ingested": 1})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// otlpSuccess writes the OTLP/HTTP success response: 200 with an empty
// Export*ServiceResponse, encoded to match the request (the OTLP/HTTP spec wants
// the response Content-Type to mirror the request). An empty response message is
// valid (no partial_success) — zero bytes for protobuf, "{}" for json — and the
// stock OTel exporters accept it.
func otlpSuccess(w http.ResponseWriter, reqContentType string) {
	if strings.Contains(strings.ToLower(reqContentType), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}
