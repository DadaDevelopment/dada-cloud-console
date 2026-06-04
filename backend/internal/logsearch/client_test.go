package logsearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew_DisabledWhenUnconfigured(t *testing.T) {
	if New("", "k", "") != nil {
		t.Fatal("expected nil client when baseURL is empty")
	}
	c := New("https://es.example.com", "k", "")
	if c == nil || c.index != "dada-vm-logs-*" {
		t.Fatalf("expected default index dada-vm-logs-*, got %+v", c)
	}
}

func TestEncodeAPIKey(t *testing.T) {
	// "id:key" form (what filebeat config + our Secret store) → base64.
	if got := encodeAPIKey("abc:def"); got != "YWJjOmRlZg==" {
		t.Errorf("encodeAPIKey(id:key) = %q, want base64", got)
	}
	// already-encoded (no colon) → pass through.
	if got := encodeAPIKey("YWJjOmRlZg=="); got != "YWJjOmRlZg==" {
		t.Errorf("encodeAPIKey(encoded) = %q, want unchanged", got)
	}
	if got := encodeAPIKey(""); got != "" {
		t.Errorf("encodeAPIKey(empty) = %q", got)
	}
}

func TestSearch_BuildsQueryAndParsesHits(t *testing.T) {
	const body = `{"hits":{"total":{"value":2},"hits":[
		{"_source":{"@timestamp":"2026-06-04T00:00:00Z","message":"hello","vm_name":"vm-1","stream":"stdout"}},
		{"_source":{"@timestamp":"2026-06-04T00:00:01Z","message":"world","host":{"name":"vm-2"}}}
	]}}`
	var gotAuth, gotPath string
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &reqBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret-key", "filebeat-*")
	res, err := c.Search(context.Background(), SearchOpts{
		VMName: "vm-1",
		Query:  "error",
		Since:  time.Unix(1733300000, 0),
		Size:   50,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "ApiKey secret-key" {
		t.Errorf("auth header = %q, want ApiKey secret-key", gotAuth)
	}
	if gotPath != "/filebeat-*/_search" {
		t.Errorf("path = %q", gotPath)
	}
	if reqBody["size"] != float64(50) {
		t.Errorf("size in body = %v, want 50", reqBody["size"])
	}
	if res.Total != 2 || len(res.Entries) != 2 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Entries[0].VMName != "vm-1" || res.Entries[0].Message != "hello" {
		t.Errorf("entry0 = %+v", res.Entries[0])
	}
	// host.name fallback when vm_name absent.
	if res.Entries[1].VMName != "vm-2" {
		t.Errorf("entry1 vm fallback = %q, want vm-2", res.Entries[1].VMName)
	}
}

func TestSearch_CapsSize(t *testing.T) {
	c := New("https://es.example.com", "", "")
	q := c.buildQuery(SearchOpts{App: "x", Size: 99999})
	if q["size"] != 1000 {
		t.Errorf("size cap = %v, want 1000", q["size"])
	}
}

func TestSearch_PropagatesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", "")
	if _, err := c.Search(context.Background(), SearchOpts{VMName: "vm"}); err == nil {
		t.Fatal("expected error on 400")
	}
}
