package server

import "sync"

// Session represents one active WebSocket editor connection.
type Session struct {
	key  string      // "project/env/app"
	send chan wsEvent // buffered; agent writes updates here
}

// Hub tracks all active Sessions and broadcasts file-change notifications.
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

// Notify pushes a file-update event to all Sessions watching
// project/env/app/file. Non-blocking: slow clients are skipped (buffer full).
func (h *Hub) Notify(project, env, app, file, yaml string) {
	key := project + "/" + env + "/" + app + "/" + file
	h.mu.RLock()
	sessions := make([]*Session, len(h.sessions[key]))
	copy(sessions, h.sessions[key])
	h.mu.RUnlock()

	evt := wsEvent{Type: "update", YAML: yaml}
	for _, s := range sessions {
		select {
		case s.send <- evt:
		default:
			// client too slow — skip this update
		}
	}
}
