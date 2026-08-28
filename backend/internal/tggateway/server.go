package tggateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Server is tg-gateway's internal HTTP API: no auth, ClusterIP-only, trusted
// the same way kagent.Reader trusts the kagent runtime. The console backend
// is its only caller.
type Server struct {
	mgr  *Manager
	ping func(context.Context) error
}

// NewServer builds the internal API over mgr.
func NewServer(mgr *Manager) *Server { return &Server{mgr: mgr} }

// SetDBPinger wires a Postgres liveness check used by /readyz.
func (s *Server) SetDBPinger(fn func(context.Context) error) { s.ping = fn }

// Handler returns tg-gateway's internal HTTP router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("POST /bindings", s.handleBind)
	mux.HandleFunc("DELETE /bindings/{agentName}", s.handleUnbind)
	mux.HandleFunc("GET /bindings/{agentName}", s.handleGet)
	return recoverAndLog(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
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

type bindRequest struct {
	AgentName string `json:"agent_name"`
	ProjectID string `json:"project_id"`
	BotToken  string `json:"bot_token"`
}

func (s *Server) handleBind(w http.ResponseWriter, r *http.Request) {
	var req bindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	req.AgentName = strings.TrimSpace(req.AgentName)
	req.BotToken = strings.TrimSpace(req.BotToken)
	if req.AgentName == "" || req.BotToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_name and bot_token are required"})
		return
	}

	b, err := s.mgr.Bind(r.Context(), req.AgentName, req.ProjectID, req.BotToken)
	if err != nil {
		var invalid ErrInvalidToken
		if errors.As(err, &invalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid bot token"})
			return
		}
		log.Error().Err(err).Str("agent", req.AgentName).Msg("tggateway: bind failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "bind failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"bot_username": b.BotUsername})
}

func (s *Server) handleUnbind(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agentName")
	if err := s.mgr.Unbind(r.Context(), agentName); err != nil {
		log.Error().Err(err).Str("agent", agentName).Msg("tggateway: unbind failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unbind failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("agentName")
	b, err := s.mgr.Get(r.Context(), agentName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no binding for that agent"})
			return
		}
		log.Error().Err(err).Str("agent", agentName).Msg("tggateway: get binding failed")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bound": true, "bot_username": b.BotUsername})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// recoverAndLog wraps the mux: it recovers a handler panic into a 500 and
// emits one structured access line per request (health probes excluded).
func recoverAndLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				log.Error().Interface("panic", v).Str("path", r.URL.Path).Msg("tggateway handler panic")
				if rec.status == http.StatusOK {
					writeJSON(rec, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				}
			}
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				log.Info().Str("method", r.Method).Str("path", r.URL.Path).
					Int("status", rec.status).Dur("dur", time.Since(start)).Msg("tggateway")
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
