package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/gitops-agent/internal/config"
	"github.com/dada-tuda/console/gitops-agent/internal/git"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// GitHookHandler is called when a verified push event arrives.
type GitHookHandler interface {
	TriggerNow(ctx context.Context)
}

// Server handles HTTP for /healthz, /webhook/github, and /ws/values.
type Server struct {
	addr    string
	secret  string
	handler GitHookHandler
	// Values editor dependencies (optional — nil disables /ws/values).
	pool        *pgxpool.Pool
	mgr         *git.Manager
	hub         *Hub
	tokenSecret string
	cfg         *config.Config
}

// ServerOptions carries optional dependencies for the values WS editor.
type ServerOptions struct {
	Pool        *pgxpool.Pool
	Manager     *git.Manager
	Hub         *Hub
	TokenSecret string
	Config      *config.Config
}

func New(addr, webhookSecret string, handler GitHookHandler, opts *ServerOptions) *Server {
	s := &Server{addr: addr, secret: webhookSecret, handler: handler}
	if opts != nil {
		s.pool = opts.Pool
		s.mgr = opts.Manager
		s.hub = opts.Hub
		s.tokenSecret = opts.TokenSecret
		s.cfg = opts.Config
	}
	return s
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/webhook/github", s.githubWebhook)

	// File editor WebSocket — only active when all deps are wired.
	// /ws/file is the generic endpoint (values.yaml, compose.yaml, .env);
	// /ws/values is kept as a backward-compatible alias. Both resolve the target
	// file from the (authoritative) token claims.
	if s.mgr != nil && s.hub != nil && s.tokenSecret != "" {
		mux.HandleFunc("/ws/file", s.handleFileWS)
		mux.HandleFunc("/ws/values", s.handleFileWS)
		log.Info().Msg("ws/file editor endpoint enabled")
	}

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info().Str("addr", s.addr).Msg("webhook server listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook server: %w", err)
	}
	return nil
}

func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if s.secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyGitHubSignature(s.secret, body, sig) {
			log.Warn().Str("sig", sig).Msg("webhook: invalid signature")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")
	if event != "push" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(body, &payload)
	log.Info().Str("ref", payload.Ref).Msg("webhook: push received, triggering sync")

	go s.handler.TriggerNow(context.Background())

	w.WriteHeader(http.StatusOK)
}

func verifyGitHubSignature(secret string, body []byte, sig string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expected)
}
