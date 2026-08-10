package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dada-tuda/console/backend/internal/buildagent"
	"github.com/dada-tuda/console/backend/internal/config"
)

// TestLinkGitRepo_PortDetection covers the connect-by-URL port pipeline end to
// end: an unset port asks the build-agent what the repo actually listens on
// and uses that; an explicit port always wins over detection; any detection
// failure (agent down, bad payload, no usable port) falls back to the old
// hardcoded 8080 so the connect can never be blocked or slowed by it; and a
// worker repo never gets a Service port no matter what detection returns.
func TestLinkGitRepo_PortDetection(t *testing.T) {
	pool := testInstallPool(t)
	projectID, envID, userID := seedInstallProject(t, pool, "acme", "k8s")
	t.Cleanup(func() {
		for _, name := range []string{"port-detect-used", "port-explicit-wins", "port-detect-fails", "port-detect-worker"} {
			dropSeededAudit(pool, "GitRepo", name)
		}
	})

	ctx := context.Background()

	t.Run("detected port is used when present", func(t *testing.T) {
		agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			port := 3000
			json.NewEncoder(w).Encode(buildagent.FrameworkDetection{Port: &port})
		}))
		defer agent.Close()

		h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, buildagent: buildagent.New(agent.URL)}
		req := &connectGitRepoRequest{
			RepoFullName: "acme/detect-used",
			AppName:      "port-detect-used",
			Provider:     "github",
		}
		repo, fault := h.linkGitRepo(ctx, userID, projectID, envID, req)
		if fault != nil {
			t.Fatalf("link failed: %+v", fault)
		}
		if repo.Port != 3000 {
			t.Fatalf("port = %d, want 3000 (detected)", repo.Port)
		}
	})

	t.Run("explicit user port wins over detection", func(t *testing.T) {
		agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			port := 3000
			json.NewEncoder(w).Encode(buildagent.FrameworkDetection{Port: &port})
		}))
		defer agent.Close()

		h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, buildagent: buildagent.New(agent.URL)}
		explicit := 9090
		req := &connectGitRepoRequest{
			RepoFullName: "acme/explicit-wins",
			AppName:      "port-explicit-wins",
			Provider:     "github",
			Port:         &explicit,
		}
		repo, fault := h.linkGitRepo(ctx, userID, projectID, envID, req)
		if fault != nil {
			t.Fatalf("link failed: %+v", fault)
		}
		if repo.Port != 9090 {
			t.Fatalf("port = %d, want 9090 (explicit)", repo.Port)
		}
	})

	t.Run("detection failure falls back to 8080", func(t *testing.T) {
		agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer agent.Close()

		h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, buildagent: buildagent.New(agent.URL)}
		req := &connectGitRepoRequest{
			RepoFullName: "acme/detect-fails",
			AppName:      "port-detect-fails",
			Provider:     "github",
		}
		repo, fault := h.linkGitRepo(ctx, userID, projectID, envID, req)
		if fault != nil {
			t.Fatalf("link failed: %+v", fault)
		}
		if repo.Port != 8080 {
			t.Fatalf("port = %d, want 8080 (fallback)", repo.Port)
		}
	})

	t.Run("worker stays portless regardless of detection", func(t *testing.T) {
		agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			port := 3000
			json.NewEncoder(w).Encode(buildagent.FrameworkDetection{Port: &port})
		}))
		defer agent.Close()

		h := &Handler{pool: pool, cfg: &config.Config{GitopsEncryptionKey: installTestKey}, buildagent: buildagent.New(agent.URL)}
		req := &connectGitRepoRequest{
			RepoFullName: "acme/detect-worker",
			AppName:      "port-detect-worker",
			Provider:     "github",
			Worker:       true,
		}
		repo, fault := h.linkGitRepo(ctx, userID, projectID, envID, req)
		if fault != nil {
			t.Fatalf("link failed: %+v", fault)
		}
		if repo.Port != 0 {
			t.Fatalf("port = %d, want 0 (worker)", repo.Port)
		}
	})
}
