package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dada-tuda/console/gitops-agent/internal/db"
	"github.com/dada-tuda/console/gitops-agent/internal/renderer"
	"github.com/dada-tuda/console/gitops-agent/internal/wstoken"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// wsEvent is the wire format for all WebSocket messages (both directions).
type wsEvent struct {
	Type    string `json:"type"`              // "content"|"update"|"save"|"committed"|"error"
	YAML    string `json:"yaml,omitempty"`    // payload for content/update/save
	SHA     string `json:"sha,omitempty"`     // set on "committed"
	Message string `json:"message,omitempty"` // set on "error"
}

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10e9, // 10s
	CheckOrigin:      func(r *http.Request) bool { return true },
}

// defaultValuesTemplate is sent when values.yaml does not yet exist in git.
const defaultValuesTemplate = `image: ""
port: 8080
replicas: 1
profile: small
`

// editFile describes one editable file kind: its git path, its default content
// when absent, and whether its content must parse as YAML on save.
type editFile struct {
	path     string
	fallback string
	isYAML   bool
}

// resolveEditFile maps a token's File claim to its git path + validation rules.
// Empty file defaults to values.yaml for backward compatibility.
func resolveEditFile(file, project, env, app string) (editFile, bool) {
	switch file {
	case "", "values.yaml":
		return editFile{
			path:     renderer.AppHelmValuesGitPath(project, env, app),
			fallback: defaultValuesTemplate,
			isYAML:   true,
		}, true
	case "compose.yaml":
		return editFile{
			path:     renderer.AppComposeGitPath(project, env, app),
			fallback: renderer.RenderComposeSkeleton(app),
			isYAML:   true,
		}, true
	case ".env":
		return editFile{
			path:     renderer.AppEnvGitPath(project, env, app),
			fallback: renderer.RenderEnvSkeleton(),
			isYAML:   false,
		}, true
	default:
		return editFile{}, false
	}
}

// handleFileWS handles GET /ws/file (and the legacy /ws/values alias).
//
// Auth: the only trusted input is `token`, a wstoken signed by the console
// backend. The token's claims (project/env/app/file) are authoritative — query
// params are ignored — so a token cannot be repurposed across apps or files.
func (s *Server) handleFileWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, err := wstoken.Verify(s.tokenSecret, token)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	project, env, app := claims.Project, claims.Env, claims.App
	ef, ok := resolveEditFile(claims.File, project, env, app)
	if !ok {
		http.Error(w, "unsupported file", http.StatusBadRequest)
		return
	}
	fileLabel := claims.File
	if fileLabel == "" {
		fileLabel = "values.yaml"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Str("app", app).Msg("ws: upgrade failed")
		return
	}
	defer conn.Close()

	// Load current file content from the local git clone.
	content, err := s.mgr.ReadFile(ef.path)
	if err != nil {
		content = ef.fallback
	}
	if err := conn.WriteJSON(wsEvent{Type: "content", YAML: content}); err != nil {
		return
	}

	// Register in Hub (keyed per file) so GitWatcher can push live updates.
	sess := &Session{
		key:  project + "/" + env + "/" + app + "/" + fileLabel,
		send: make(chan wsEvent, 8),
	}
	s.hub.Register(sess)
	defer s.hub.Unregister(sess)

	// Write pump: forwards hub notifications to the WS connection.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for evt := range sess.send {
			if err := conn.WriteJSON(evt); err != nil {
				log.Debug().Err(err).Str("app", app).Msg("ws: write pump stopped")
				return
			}
		}
	}()

	// Read loop: receives "save" messages from the editor.
	for {
		var msg wsEvent
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		if msg.Type != "save" {
			continue
		}

		// Syntax check for YAML files only (.env is plain KEY=VALUE).
		if ef.isYAML {
			var tmp any
			if err := yaml.Unmarshal([]byte(msg.YAML), &tmp); err != nil {
				_ = conn.WriteJSON(wsEvent{Type: "error", Message: "invalid YAML: " + err.Error()})
				continue
			}
		}

		commitMsg := fmt.Sprintf(
			"[DADA Console] Edit %s for app %s\n\nApp: %s\nEnvironment: %s\nProject: %s\n",
			fileLabel, app, app, env, project,
		)
		sha, err := s.mgr.CommitAndPush(ef.path, msg.YAML, commitMsg, s.cfg.BotName, s.cfg.BotEmail)
		if err != nil {
			_ = conn.WriteJSON(wsEvent{Type: "error", Message: err.Error()})
			continue
		}

		// Audit record — best-effort.
		if s.pool != nil {
			if err := db.InsertCommit(
				context.Background(), s.pool,
				sha, s.mgr.RepoURL(), s.mgr.Branch(),
				ef.path, commitMsg,
				s.cfg.BotName, s.cfg.BotEmail,
				nil, "agent",
			); err != nil {
				log.Warn().Err(err).Str("sha", sha).Msg("ws: record commit")
			}
		}

		// Editing a compose app's compose.yaml/.env must redeploy the stack.
		if s.pool != nil && (fileLabel == "compose.yaml" || fileLabel == ".env") {
			if opID, err := db.EnqueueDeployStackBySlug(context.Background(), s.pool, project, env, app); err != nil {
				log.Warn().Err(err).Str("app", app).Msg("ws: enqueue redeploy")
			} else {
				log.Info().Str("app", app).Str("deploy_op", opID.String()).Msg("ws: redeploy enqueued after edit")
			}
		}

		_ = conn.WriteJSON(wsEvent{Type: "committed", SHA: sha})
		log.Info().Str("app", app).Str("file", fileLabel).Str("sha", sha).Msg("ws: committed")
	}

	close(sess.send)
	<-writeDone
}
