package server

import "sync"

// wsFrame is the wire format for build-log WebSocket messages.
type wsFrame struct {
	Type string `json:"type"`           // "log"|"status"|"error"|"done"
	Line string `json:"line,omitempty"` // log payload
	Msg  string `json:"msg,omitempty"`  // status/error payload
}

// Session represents one active build-log WebSocket connection.
type Session struct {
	key  string       // "build/<id>"
	send chan wsFrame // buffered; runner writes log frames here
}

// Hub tracks all active Sessions and fans out build-log frames.
// Copied from gitops-agent/internal/server/hub.go (keyed by build/<id>).
type Hub struct {
	mu       sync.RWMutex
	sessions map[string][]*Session // key → sessions
}

// NewHub returns an initialised Hub.
func NewHub() *Hub {
	return &Hub{sessions: make(map[string][]*Session)}
}

// Register adds a Session to the Hub.
func (h *Hub) Register(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s.key] = append(h.sessions[s.key], s)
}

// Unregister removes a Session from the Hub.
func (h *Hub) Unregister(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.sessions[s.key]
	for i, sess := range list {
		if sess == s {
			h.sessions[s.key] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(h.sessions[s.key]) == 0 {
		delete(h.sessions, s.key)
	}
}

// PublishLog is the exported convenience the runner uses to fan out one
// redacted log line for a build.
func (h *Hub) PublishLog(buildID, line string) {
	h.Publish(buildID, wsFrame{Type: "log", Line: line})
}

// Publish pushes a log frame to all Sessions watching build/<id>.
// Non-blocking: slow clients are skipped (buffer full).
func (h *Hub) Publish(buildID string, frame wsFrame) {
	key := "build/" + buildID
	h.mu.RLock()
	sessions := make([]*Session, len(h.sessions[key]))
	copy(sessions, h.sessions[key])
	h.mu.RUnlock()

	for _, s := range sessions {
		select {
		case s.send <- frame:
		default:
			// client too slow — skip this frame
		}
	}
}
