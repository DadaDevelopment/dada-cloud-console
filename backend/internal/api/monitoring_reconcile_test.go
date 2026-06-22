package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dada-tuda/console/backend/internal/grafana"
)

// fakeGrafana is a minimal stand-in for the Grafana provisioning API: it tracks
// which alert-rule UIDs and contact-point names "exist" and records created rules.
type fakeGrafana struct {
	mu          sync.Mutex
	rules       map[string]bool // uid -> exists
	contacts    map[string]bool // name -> exists
	created     []map[string]any
	foldersMade []string
}

func newFakeGrafana() *fakeGrafana {
	return &fakeGrafana{rules: map[string]bool{}, contacts: map[string]bool{}}
}

func (f *fakeGrafana) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		// Alert-rule existence check.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/provisioning/alert-rules/"):
			uid := strings.TrimPrefix(r.URL.Path, "/api/v1/provisioning/alert-rules/")
			if f.rules[uid] {
				_, _ = w.Write([]byte(`{"uid":"` + uid + `"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)

		// Alert-rule creation.
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/provisioning/alert-rules":
			body, _ := io.ReadAll(r.Body)
			var rule map[string]any
			_ = json.Unmarshal(body, &rule)
			f.created = append(f.created, rule)
			uid, _ := rule["uid"].(string)
			f.rules[uid] = true
			_, _ = w.Write([]byte(`{"uid":"` + uid + `"}`))

		// Contact-point existence query (?name=...).
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/provisioning/contact-points":
			name := r.URL.Query().Get("name")
			if f.contacts[name] {
				_, _ = w.Write([]byte(`[{"name":"` + name + `","type":"webhook"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))

		// Folder ensure: report exists so EnsureFolder is a no-op GET.
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/folders/"):
			f.foldersMade = append(f.foldersMade, strings.TrimPrefix(r.URL.Path, "/api/folders/"))
			_, _ = w.Write([]byte(`{"uid":"x"}`))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
}

func desiredRuleFixture() desiredAlertRule {
	return desiredAlertRule{
		uid:          "dar-test-uid",
		title:        "cpu high",
		folderUID:    "fold-1",
		folderTitle:  "Project p",
		ruleGroup:    "app-1",
		expr:         `avg(cpu{project_id="p"})`,
		condition:    ">",
		threshold:    90,
		forDur:       "5m",
		contactPoint: "dch-chan-1",
		labels:       map[string]string{"project_id": "p"},
	}
}

func TestReconcileDesiredRule_ExistingRuleSkipped(t *testing.T) {
	f := newFakeGrafana()
	f.rules["dar-test-uid"] = true
	srv := f.server(t)
	defer srv.Close()
	gc := grafana.New(srv.URL, "tok", "prom", "")

	recreated, err := reconcileDesiredRule(context.Background(), gc, "prom", desiredRuleFixture())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recreated {
		t.Fatal("existing rule must not be re-created")
	}
	if len(f.created) != 0 {
		t.Fatalf("no rule should be created, got %d", len(f.created))
	}
}

func TestReconcileDesiredRule_MissingRuleRecreated(t *testing.T) {
	f := newFakeGrafana()
	f.contacts["dch-chan-1"] = true // channel still present → keep routing
	srv := f.server(t)
	defer srv.Close()
	gc := grafana.New(srv.URL, "tok", "prom", "")

	recreated, err := reconcileDesiredRule(context.Background(), gc, "prom", desiredRuleFixture())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recreated {
		t.Fatal("missing rule must be re-created")
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 created rule, got %d", len(f.created))
	}
	got := f.created[0]
	if got["uid"] != "dar-test-uid" {
		t.Fatalf("rule re-created with wrong uid: %v", got["uid"])
	}
	if _, ok := got["notification_settings"]; !ok {
		t.Fatal("routing must be preserved when contact point exists")
	}
}

func TestReconcileDesiredRule_MissingContactPointRecreatesUnrouted(t *testing.T) {
	f := newFakeGrafana()
	// contact point absent → rule re-created without routing
	srv := f.server(t)
	defer srv.Close()
	gc := grafana.New(srv.URL, "tok", "prom", "")

	recreated, err := reconcileDesiredRule(context.Background(), gc, "prom", desiredRuleFixture())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !recreated {
		t.Fatal("missing rule must be re-created even without its channel")
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 created rule, got %d", len(f.created))
	}
	if _, ok := f.created[0]["notification_settings"]; ok {
		t.Fatal("rule must be re-created unrouted when contact point is gone")
	}
}
