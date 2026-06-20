// Package server hosts the build-agent HTTP surface:
// /healthz, /metrics, /webhook/github (nudge), and /ws/build (log stream).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dada-tuda/console/build-agent/internal/config"
	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/github"
	"github.com/dada-tuda/console/build-agent/internal/metrics"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// PushNudger is notified when a verified webhook arrives, so the poller can
// drain the queue immediately instead of waiting for the next tick.
type PushNudger interface {
	OnPush(ctx context.Context)
}

// Server is the build-agent HTTP server.
type Server struct {
	addr        string
	pool        *pgxpool.Pool
	hub         *Hub
	nudger      PushNudger
	tokenSecret string
	cfg         *config.Config
}

// Options carries server dependencies.
type Options struct {
	Pool        *pgxpool.Pool
	Hub         *Hub
	Nudger      PushNudger
	TokenSecret string
	Config      *config.Config
}

// New constructs a Server.
func New(addr string, opts *Options) *Server {
	s := &Server{addr: addr}
	if opts != nil {
		s.pool = opts.Pool
		s.hub = opts.Hub
		s.nudger = opts.Nudger
		s.tokenSecret = opts.TokenSecret
		s.cfg = opts.Config
	}
	return s
}

// Start runs the HTTP server until ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/webhook/github", s.githubWebhook)

	if s.hub != nil && s.tokenSecret != "" {
		mux.HandleFunc("/ws/build", s.handleBuildWS)
		log.Info().Msg("ws/build log endpoint enabled")
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

	log.Info().Str("addr", s.addr).Msg("build-agent server listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// pushEvent is the subset of a GitHub push webhook payload we consume.
type pushEvent struct {
	Ref        string `json:"ref"` // refs/heads/<branch>
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	HeadCommit struct {
		Message string `json:"message"`
	} `json:"head_commit"`
}

// githubWebhook is the "nudge" trigger. It verifies the HMAC (per-repo secret
// when configured, else the global app secret), idempotently enqueues a build
// for every git_repos linked to the pushed repo+branch, then nudges the queue
// and returns 200 fast.
//
// TODO(wave-3): pull_request events (open/sync/close) for preview envs +
// fork-PR safety flag (head.repo != base → fork_unsafe, inject no secrets).
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

	event := r.Header.Get("X-GitHub-Event")
	if event != "push" {
		// pull_request and other events are accepted but not yet acted on.
		w.WriteHeader(http.StatusOK)
		return
	}

	var ev pushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	branch := strings.TrimPrefix(ev.Ref, "refs/heads/")
	log.Info().Str("event", event).Str("repo", ev.Repository.FullName).Str("branch", branch).Msg("webhook received")

	enqueued := 0
	if s.pool != nil && ev.Repository.FullName != "" {
		repos, rerr := db.ResolveReposByFullName(r.Context(), s.pool, ev.Repository.FullName)
		if rerr != nil {
			log.Error().Err(rerr).Msg("webhook: resolve repos")
			http.Error(w, "resolve error", http.StatusInternalServerError)
			return
		}
		sig := r.Header.Get("X-Hub-Signature-256")
		for _, repo := range repos {
			if !s.verifyWebhook(repo.WebhookSecret, body, sig) {
				log.Warn().Str("repo", repo.RepoFullName).Msg("webhook: invalid signature")
				continue
			}
			if repo.ProductionBranch != branch || !repo.AutoDeploy {
				continue
			}
			if _, err := db.InsertBuildFromWebhook(r.Context(), s.pool,
				repo.ID, repo.EnvironmentID, repo.AppName, ev.After, ev.HeadCommit.Message, branch, "push"); err != nil {
				log.Error().Err(err).Str("repo", repo.RepoFullName).Msg("webhook: enqueue build")
				continue
			}
			enqueued++
		}
	}

	if enqueued > 0 && s.nudger != nil {
		go s.nudger.OnPush(context.Background())
	}

	w.WriteHeader(http.StatusOK)
}

// verifyWebhook checks the HMAC against the per-repo secret when set, falling
// back to the global app webhook secret. Absent any configured secret, the
// signature is not enforced (dev convenience).
func (s *Server) verifyWebhook(repoSecret string, body []byte, sig string) bool {
	secret := repoSecret
	if secret == "" && s.cfg != nil {
		secret = s.cfg.GitHubWebhookSecret
	}
	if secret == "" {
		return true
	}
	return github.VerifySignature(secret, body, sig)
}

// Hub exposes the log hub so the runner can publish frames.
func (s *Server) Hub() *Hub { return s.hub }
