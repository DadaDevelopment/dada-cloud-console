package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew_DisabledWhenUnconfigured(t *testing.T) {
	if New("", "", "") != nil {
		t.Fatal("expected nil client when baseURL is empty")
	}
	if New("https://prom.example.com", "u", "p") == nil {
		t.Fatal("expected non-nil client when baseURL is set")
	}
}

func TestPointUnmarshal(t *testing.T) {
	var p Point
	if err := json.Unmarshal([]byte(`[1733300000, "12.5"]`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.T != 1733300000 || p.V != 12.5 {
		t.Fatalf("got t=%v v=%v, want 1733300000/12.5", p.T, p.V)
	}
	// Malformed tuples must error rather than silently zero.
	if err := json.Unmarshal([]byte(`[1]`), &p); err == nil {
		t.Fatal("expected error for single-element tuple")
	}
	if err := json.Unmarshal([]byte(`[1, "notafloat"]`), &p); err == nil {
		t.Fatal("expected error for non-numeric value string")
	}
}

func TestQueryRange_ParsesMatrixAndSetsBasicAuth(t *testing.T) {
	const body = `{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"vm_name":"vm-1"},"values":[[1733300000,"10"],[1733300060,"20.5"]]}
	]}}`
	var gotAuth bool
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotAuth = ok && u == "user" && p == "pass"
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, "user", "pass")
	series, err := c.QueryRange(context.Background(), `up{vm_name="vm-1"}`,
		time.Unix(1733300000, 0), time.Unix(1733300060, 0), time.Minute, "")
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if !gotAuth {
		t.Error("basic auth header not set / incorrect")
	}
	if gotPath != "/api/v1/query_range" {
		t.Errorf("path = %q, want /api/v1/query_range", gotPath)
	}
	if gotQuery != `up{vm_name="vm-1"}` {
		t.Errorf("query param = %q", gotQuery)
	}
	if len(series) != 1 || series[0].Metric["vm_name"] != "vm-1" {
		t.Fatalf("unexpected series: %+v", series)
	}
	if len(series[0].Points) != 2 || series[0].Points[1].V != 20.5 {
		t.Fatalf("unexpected points: %+v", series[0].Points)
	}
}

func TestQueryRange_PropagatesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","error":"bad query"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", "")
	if _, err := c.QueryRange(context.Background(), "boom", time.Now().Add(-time.Hour), time.Now(), time.Minute, ""); err == nil {
		t.Fatal("expected error when status != success")
	}
}

func TestEscapeLabelValue(t *testing.T) {
	if got := EscapeLabelValue(`a"b\c`); got != `a\"b\\c` {
		t.Errorf("escape = %q", got)
	}
}
