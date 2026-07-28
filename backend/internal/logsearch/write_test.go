package logsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewWriteClient_NilWhenUnconfigured(t *testing.T) {
	if NewWriteClient("", "", "") != nil {
		t.Error("expected nil for empty baseURL")
	}
}

func TestNewWriteClient_Defaults(t *testing.T) {
	c := NewWriteClient("http://es:9200/", "", "")
	if c.baseURL != "http://es:9200" {
		t.Errorf("baseURL trailing slash not trimmed: %q", c.baseURL)
	}
	if c.baseIndex != "dada-app-logs" {
		t.Errorf("default baseIndex = %q", c.baseIndex)
	}
	// "dada-app-logs-*" should be normalized back to base
	c2 := NewWriteClient("http://es:9200", "", "dada-app-logs-*")
	if c2.baseIndex != "dada-app-logs" {
		t.Errorf("baseIndex not normalized: %q", c2.baseIndex)
	}
}

func TestIndex_WritesDatedDocWithTenancyLabels(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotDoc map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotDoc)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewWriteClient(srv.URL, "id:secret", "dada-app-logs")
	ts := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	err := c.Index(context.Background(), AppLog{
		Timestamp:     ts,
		Source:        "worker-1",
		Level:         "ERROR",
		Message:       "boom",
		OrgID:         "org1",
		ProjectID:     "proj1",
		Environment:   "prod",
		MonitoringApp: "my-app",
	})
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	wantPath := "/dada-app-logs-2026.06.21/_doc"
	if gotPath != wantPath {
		t.Errorf("path: got %q want %q", gotPath, wantPath)
	}
	if !strings.HasPrefix(gotAuth, "ApiKey ") {
		t.Errorf("auth header: got %q", gotAuth)
	}
	// tenancy labels + reuse-compat fields
	checks := map[string]string{
		"message":        "boom",
		"level":          "ERROR",
		"source":         "worker-1",
		"org_id":         "org1",
		"project_id":     "proj1",
		"environment":    "prod",
		"monitoring_app": "my-app",
		"app":            "my-app",   // reuse-compat
		"vm_name":        "worker-1", // reuse-compat
	}
	for k, want := range checks {
		if got, _ := gotDoc[k].(string); got != want {
			t.Errorf("doc[%q] = %q, want %q", k, got, want)
		}
	}
	if _, ok := gotDoc["@timestamp"]; !ok {
		t.Error("missing @timestamp")
	}
}

func TestIndex_PropagatesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("mapping error"))
	}))
	defer srv.Close()

	c := NewWriteClient(srv.URL, "", "")
	err := c.Index(context.Background(), AppLog{Message: "x"})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Errorf("expected status 400 error, got %v", err)
	}
}
