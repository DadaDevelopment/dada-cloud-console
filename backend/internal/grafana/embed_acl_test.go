package grafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGrafana records the last folder-permissions POST body and serves canned
// lookups so we can assert the read-merge-write ACL behavior.
type fakeGrafana struct {
	curPerms    []map[string]any // returned by GET .../permissions
	lastPosted  []map[string]any // captured items from POST .../permissions
	userByLogin map[string]int   // existing users
	createdUser string           // login passed to POST /api/admin/users
}

func (f *fakeGrafana) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users/lookup":
			login := r.URL.Query().Get("loginOrEmail")
			if id, ok := f.userByLogin[login]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/admin/users":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.createdUser, _ = body["login"].(string)
			id := 999
			f.userByLogin[f.createdUser] = id
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodGet && r.URL.Path == "/api/folders/F1/permissions":
			_ = json.NewEncoder(w).Encode(f.curPerms)
		case r.Method == http.MethodPost && r.URL.Path == "/api/folders/F1/permissions":
			var body struct {
				Items []map[string]any `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastPosted = body.Items
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
		}
	}))
}

func TestSetFolderTenantStripsRolesKeepsUsers(t *testing.T) {
	f := &fakeGrafana{
		userByLogin: map[string]int{},
		curPerms: []map[string]any{
			{"role": "Editor", "permission": 2},
			{"role": "Viewer", "permission": 1},
			{"userId": 42, "permission": 1},
		},
	}
	srv := f.server(t)
	defer srv.Close()
	c := New(srv.URL, "tok", "prom", "")

	if err := c.SetFolderTenant(context.Background(), "F1"); err != nil {
		t.Fatalf("SetFolderTenant: %v", err)
	}
	// Role grants gone; the explicit user grant survives.
	if len(f.lastPosted) != 1 {
		t.Fatalf("expected 1 item, got %v", f.lastPosted)
	}
	if got := f.lastPosted[0]["userId"]; toInt(got) != 42 {
		t.Fatalf("kept wrong/no user grant: %v", f.lastPosted)
	}
}

func TestEnsureUserFolderAccessCreatesUserAndGrants(t *testing.T) {
	f := &fakeGrafana{
		userByLogin: map[string]int{"existing": 7}, // alice does not exist yet
		curPerms: []map[string]any{
			{"role": "Viewer", "permission": 1},
			{"userId": 7, "permission": 1}, // existing user must be preserved
		},
	}
	srv := f.server(t)
	defer srv.Close()
	c := New(srv.URL, "tok", "prom", "")

	if err := c.EnsureUserFolderAccess(context.Background(), "F1", "alice", "alice@x.io", "Alice"); err != nil {
		t.Fatalf("EnsureUserFolderAccess: %v", err)
	}
	if f.createdUser != "alice" {
		t.Fatalf("expected alice created, got %q", f.createdUser)
	}
	// Result: role stripped, existing user 7 kept, new user 999 granted View.
	users := map[int]int{}
	for _, it := range f.lastPosted {
		if _, ok := it["role"]; ok {
			t.Fatalf("role grant leaked: %v", f.lastPosted)
		}
		users[toInt(it["userId"])] = toInt(it["permission"])
	}
	if users[7] != 1 || users[999] != 1 || len(users) != 2 {
		t.Fatalf("unexpected ACL: %v", f.lastPosted)
	}
}

func TestEnsureUserFolderAccessIdempotentForGrantedUser(t *testing.T) {
	f := &fakeGrafana{
		userByLogin: map[string]int{"alice": 5},
		curPerms: []map[string]any{
			{"userId": 5, "permission": 1}, // already granted
		},
	}
	srv := f.server(t)
	defer srv.Close()
	c := New(srv.URL, "tok", "prom", "")

	if err := c.EnsureUserFolderAccess(context.Background(), "F1", "alice", "", ""); err != nil {
		t.Fatalf("EnsureUserFolderAccess: %v", err)
	}
	if f.createdUser != "" {
		t.Fatalf("should not create an existing user")
	}
	if len(f.lastPosted) != 1 || toInt(f.lastPosted[0]["userId"]) != 5 {
		t.Fatalf("expected single idempotent grant, got %v", f.lastPosted)
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}
