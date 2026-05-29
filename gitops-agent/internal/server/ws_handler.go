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

// handleValuesWS handles GET /ws/values.
//
// Query params: token, project, env, app.
// The token is a wstoken signed by the console backend; it must match the
// project/env/app query params to prevent token reuse across apps.
func (s *Server) handleValuesWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("token")
	project := q.Get("project")
	env := q.Get("env")
	app := q.Get("app")

	claims, err := wstoken.Verify(s.tokenSecret, token)
	if err != nil || claims.Project != project || claims.Env != env || claims.App != app {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Str("app", app).Msg("ws/values: upgrade failed")
		return
	}
	defer conn.Close()

	// Load current values.yaml from the local git clone.
	valuesPath := renderer.AppHelmValuesGitPath(project, env, app)
	content, err := s.mgr.ReadFile(valuesPath)
	if err != nil {
		content = defaultValuesTemplate
	}

	if err := conn.WriteJSON(wsEvent{Type: "content", YAML: content}); err != nil {
		return
	}

	// Register in Hub so GitWatcher can push live updates.
	sess := &Session{
		key:  project + "/" + env + "/" + app,
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
				log.Debug().Err(err).Str("app", app).Msg("ws/values: write pump stopped")
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

		// Basic YAML syntax check.
		var tmp any
		if err := yaml.Unmarshal([]byte(msg.YAML), &tmp); err != nil {
			_ = conn.WriteJSON(wsEvent{Type: "error", Message: "invalid YAML: " + err.Error()})
			continue
		}

		// Commit directly through the default git manager.
		commitMsg := fmt.Sprintf(
			"[DADA Console] Edit values for app %s\n\nApp: %s\nEnvironment: %s\nProject: %s\n",
			app, app, env, project,
		)
		sha, err := s.mgr.CommitAndPush(valuesPath, msg.YAML, commitMsg, s.cfg.BotName, s.cfg.BotEmail)
		if err != nil {
			_ = conn.WriteJSON(wsEvent{Type: "error", Message: err.Error()})
			continue
		}

		// Audit record — best-effort.
		if s.pool != nil {
			if err := db.InsertCommit(
				context.Background(), s.pool,
				sha, s.mgr.RepoURL(), s.mgr.Branch(),
				valuesPath, commitMsg,
				s.cfg.BotName, s.cfg.BotEmail,
				nil, "agent",
			); err != nil {
				log.Warn().Err(err).Str("sha", sha).Msg("ws/values: record commit")
			}
		}

		_ = conn.WriteJSON(wsEvent{Type: "committed", SHA: sha})
		log.Info().Str("app", app).Str("sha", sha).Msg("ws/values: committed")
	}

	close(sess.send)
	<-writeDone
}
