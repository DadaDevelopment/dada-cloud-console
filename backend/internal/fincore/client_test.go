package fincore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type capturedBatch struct {
	SourceSystem string           `json:"source_system"`
	Items        []map[string]any `json:"items"`
}

func TestIngestSendsTheEnvelopeTheEndpointDeclares(t *testing.T) {
	var got capturedBatch
	var authHeader, tenant string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ingestPath {
			t.Errorf("path = %q, want %q", r.URL.Path, ingestPath)
		}
		authHeader = r.Header.Get("Authorization")
		tenant = r.Header.Get(tenantHeader)
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("request body is not the declared envelope: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"received":1,"created":1,"updated":0,"unchanged":0,"results":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "fcs_test_secret", "dada_development")
	if c == nil {
		t.Fatal("New returned nil for a fully configured client")
	}

	res, err := c.IngestTransactions(context.Background(), []Transaction{{
		SourceIdentity: "payment:1",
		OperationDate:  WallTime(time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)),
		Direction:      DirectionCredit,
		Amount:         "990.00",
		Currency:       "RUB",
		PayerName:      "Артём",
	}})
	if err != nil {
		t.Fatalf("IngestTransactions: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	if authHeader != "Bearer fcs_test_secret" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	if tenant != "dada_development" {
		t.Fatalf("%s = %q", tenantHeader, tenant)
	}
	if got.SourceSystem != SourceSystem {
		t.Fatalf("source_system = %q, want %q", got.SourceSystem, SourceSystem)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(got.Items))
	}
	if _, ok := got.Items[0]["project_id"]; ok {
		t.Error("project_id was sent while unset; the endpoint forbids nulls it did not ask for")
	}
	if got.Items[0]["operation_date"] != "2026-07-25T12:00:00" {
		t.Fatalf("operation_date = %v", got.Items[0]["operation_date"])
	}
}

func TestIngestSplitsBatchesAtTheServerLimit(t *testing.T) {
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch capturedBatch
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &batch)
		sizes = append(sizes, len(batch.Items))
		_, _ = w.Write([]byte(`{"received":0,"created":0,"updated":0,"unchanged":0,"results":[]}`))
	}))
	defer srv.Close()

	txs := make([]Transaction, maxBatch+1)
	c := New(srv.URL, "fcs_x", "dada_development")
	if _, err := c.IngestTransactions(context.Background(), txs); err != nil {
		t.Fatalf("IngestTransactions: %v", err)
	}
	if len(sizes) != 2 || sizes[0] != maxBatch || sizes[1] != 1 {
		t.Fatalf("batch sizes = %v, want [%d 1]", sizes, maxBatch)
	}
}

func TestNonSuccessCarriesTheServersOwnReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Токену не хватает scope: ingest:write"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "fcs_x", "dada_development")
	_, err := c.IngestTransactions(context.Background(), []Transaction{{SourceIdentity: "a"}})
	if err == nil {
		t.Fatal("a 403 was reported as success")
	}
	if want := "ingest:write"; !contains(err.Error(), want) {
		t.Fatalf("error %q does not carry the server's reason %q", err, want)
	}
}

func TestNewIsOffWhenUnconfigured(t *testing.T) {
	for _, tc := range [][3]string{
		{"", "fcs_x", "dada_development"},
		{"https://x", "", "dada_development"},
		{"https://x", "fcs_x", ""},
	} {
		if c := New(tc[0], tc[1], tc[2]); c != nil {
			t.Fatalf("New(%q,%q,%q) returned a client; a missing tenant or token must switch the push off, not guess", tc[0], tc[1], tc[2])
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
