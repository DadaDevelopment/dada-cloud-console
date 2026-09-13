package composio

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Server is the broker's HTTP surface. Two halves, on purpose:
//
//   - /mcp/{project} is what an agent talks to, and it is the only half that
//     ever proxies a tool call. It authenticates by the end-user header the
//     agent replays, so it must stay cluster-internal.
//   - /integrations/... and /demo are what a browser talks to: list, connect,
//     callback. The console proxies these through its own authenticated API;
//     when the listener is reachable from outside, Token must be set.
type Server struct {
	svc        *Service
	proxy      *Proxy
	catalog    []string
	consoleURL string
	basePath   string
	token      string
}

// ServerOptions configures a Server.
//
// Catalog is the toolkit allowlist this platform offers; empty means the whole
// Composio catalog, which is a product decision rather than a technical one and
// is why it is explicit. BasePath is the prefix a reverse proxy mounts this
// service under, so the demo page can build its own URLs. Token, when set, is
// required on every browser-facing call.
type ServerOptions struct {
	Catalog    []string
	ConsoleURL string
	BasePath   string
	Token      string
}

// NewServer returns a Server.
func NewServer(svc *Service, opts ServerOptions) *Server {
	return &Server{
		svc:        svc,
		proxy:      NewProxy(svc),
		catalog:    opts.Catalog,
		consoleURL: strings.TrimRight(opts.ConsoleURL, "/"),
		basePath:   strings.TrimRight(opts.BasePath, "/"),
		token:      opts.Token,
	}
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/mcp/", s.mcp)
	mux.HandleFunc("/demo", s.guarded(s.demo))
	mux.HandleFunc("/integrations/callback", s.guarded(s.callback))
	mux.HandleFunc("/integrations", s.guarded(s.integrations))
	mux.HandleFunc("/integrations/", s.guarded(s.integrations))
	return mux
}

// guarded requires the shared token on browser-facing routes when one is set.
//
// The MCP half is deliberately not guarded this way: an agent presents only the
// headers its ManagedAgent claim allows it to replay, and adding a second secret
// there would put a credential into a git-rendered manifest.
func (s *Server) guarded(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) authorized(r *http.Request) bool {
	if r.URL.Query().Get("token") == s.token {
		return true
	}
	header := r.Header.Get("Authorization")
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer")) == s.token
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// mcp serves /mcp/{projectID} for agents.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/mcp/"), "/")
	projectID, err := uuid.Parse(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be /mcp/<project-uuid>"})
		return
	}
	s.proxy.ServeMCP(w, r, projectID)
}

// connectRequest is the body the console posts when a user clicks Connect.
type connectRequest struct {
	ProjectID   string `json:"project_id"`
	EndUserKey  string `json:"end_user_key"`
	Toolkit     string `json:"toolkit"`
	CallbackURL string `json:"callback_url"`
}

// integrations serves the console-facing half.
func (s *Server) integrations(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/catalog"):
		s.listCatalog(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
		s.connect(w, r)
	case r.Method == http.MethodGet:
		s.list(w, r)
	case r.Method == http.MethodDelete:
		s.disconnect(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown route"})
	}
}

// listCatalog returns the apps this platform offers, with names and logos so the
// console renders a real app list rather than slugs.
func (s *Server) listCatalog(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.client.Catalog(r.Context(), s.catalog)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "cannot read the app catalog"})
		return
	}
	out := make([]Toolkit, 0, len(items))
	if len(s.catalog) == 0 {
		for _, item := range items {
			out = append(out, item)
		}
	} else {
		for _, slug := range s.catalog {
			if item, ok := items[slug]; ok {
				out = append(out, item)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"toolkits": out})
}

// list reports one end user's authorizations, refreshed from Composio because
// the user finishes OAuth on a page this platform does not host. A refresh
// failure degrades to the cached view rather than to an error page: a stale
// status is readable, a 500 on the integrations screen is not.
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	projectID, endUser, ok := queryIdentity(w, r)
	if !ok {
		return
	}
	items, err := s.svc.Sync(r.Context(), projectID, endUser)
	if err != nil {
		log.Warn().Err(err).Msg("composio: sync failed, answering from cache")
		items, err = s.svc.Integrations(r.Context(), projectID, endUser)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot read integrations"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": items})
}

// connect returns the authorization URL for one app and one end user.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	projectID, err := uuid.Parse(strings.TrimSpace(req.ProjectID))
	if err != nil || strings.TrimSpace(req.EndUserKey) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id and end_user_key are required"})
		return
	}
	toolkit := strings.ToLower(strings.TrimSpace(req.Toolkit))
	if len(s.catalog) > 0 && !contains(s.catalog, toolkit) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this app is not offered on this platform"})
		return
	}
	callback := req.CallbackURL
	if callback == "" {
		callback = s.callbackURL(req.ProjectID, req.EndUserKey, r.URL.Query().Get("token"))
	}
	redirect, err := s.svc.Connect(r.Context(), projectID, req.EndUserKey, toolkit, callback)
	if err != nil {
		log.Error().Err(err).Str("toolkit", toolkit).Msg("composio: connect failed")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "cannot start authorization for this app"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_url": redirect})
}

// callbackURL builds the return address, carrying the identity through the OAuth
// round trip so the callback can refresh the right user's state without a
// session cookie.
func (s *Server) callbackURL(projectID, endUserKey, token string) string {
	if s.consoleURL == "" {
		return ""
	}
	url := s.consoleURL + "/integrations/callback?project_id=" + projectID + "&end_user_key=" + endUserKey
	if token != "" {
		url += "&token=" + token
	}
	return url
}

// disconnect forgets one authorization locally.
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	projectID, endUser, ok := queryIdentity(w, r)
	if !ok {
		return
	}
	toolkit := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("toolkit")))
	if toolkit == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "toolkit is required"})
		return
	}
	if err := s.svc.store.DeleteIntegration(r.Context(), projectID, endUser, toolkit); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot remove integration"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func queryIdentity(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	projectID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("project_id")))
	endUser := strings.TrimSpace(r.URL.Query().Get("end_user_key"))
	if err != nil || endUser == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id and end_user_key are required"})
		return uuid.Nil, "", false
	}
	return projectID, endUser, true
}

// parseUUID is the callback's own parse, kept separate because a malformed id
// there is not a client error to report but a reason to skip the refresh.
func parseUUID(raw string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(raw))
}

// logSyncFailure records a callback that could not refresh state. The user still
// sees the success page: the authorization really happened upstream, and the next
// list call reconciles.
func logSyncFailure(project, endUser string, err error) {
	log.Warn().Err(err).Str("project", project).Str("end_user", endUser).
		Msg("composio: callback could not refresh integration state")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
