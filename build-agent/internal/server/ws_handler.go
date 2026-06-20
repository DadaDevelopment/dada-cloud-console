package server

import (
	"net/http"

	"github.com/dada-tuda/console/build-agent/internal/db"
	"github.com/dada-tuda/console/build-agent/internal/wstoken"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout: 10e9, // 10s
	CheckOrigin:      func(r *http.Request) bool { return true },
}

// handleBuildWS handles GET /ws/build — live build-log streaming.
//
// Auth: the only trusted input is `token`, a wstoken signed by the console
// backend after canWrite(). The token's BuildID claim is authoritative.
//
// On connect it replays the recent builds_logs backlog (so late joiners see
// history), then registers with the hub for live frames. Because backlog is read
// before registration there can be at most a small duplicate window at the
// boundary — acceptable for a log viewer.
func (s *Server) handleBuildWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	claims, err := wstoken.Verify(s.tokenSecret, token)
	if err != nil || claims.BuildID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Warn().Err(err).Str("build", claims.BuildID).Msg("ws: upgrade failed")
		return
	}
	defer conn.Close()

	// Replay recent backlog before going live.
	if s.pool != nil {
		if bid, perr := uuid.Parse(claims.BuildID); perr == nil {
			if backlog, lerr := db.RecentLogs(r.Context(), s.pool, bid, 500); lerr == nil {
				for _, line := range backlog {
					if werr := conn.WriteJSON(wsFrame{Type: "log", Line: line}); werr != nil {
						return
					}
				}
			}
		}
	}

	sess := &Session{
		key:  "build/" + claims.BuildID,
		send: make(chan wsFrame, 64),
	}
	s.hub.Register(sess)
	defer s.hub.Unregister(sess)

	// Write pump: forwards hub frames to the WS connection.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for frame := range sess.send {
			if err := conn.WriteJSON(frame); err != nil {
				return
			}
		}
	}()

	// Read loop: drain until client disconnects (no inbound commands yet).
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	close(sess.send)
	<-writeDone
}
