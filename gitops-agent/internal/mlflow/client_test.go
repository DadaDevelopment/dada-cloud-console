package mlflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// New() returns nil when no base URL is configured. Callers branch on this to
// emit a clear error rather than dereference a nil client.
func TestNewReturnsNilWhenBaseURLEmpty(t *testing.T) {
	if c := New("", ""); c != nil {
		t.Errorf("New(\"\", \"\") = %+v, want nil", c)
	}
}

func TestGetModelVersionSource(t *testing.T) {
	const wantAuth = "Bearer test-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/2.0/mlflow/model-versions/get" {
			t.Errorf("path = %q, want /api/2.0/mlflow/model-versions/get", r.URL.Path)
		}
		if got := r.URL.Query().Get("name"); got != "iris" {
			t.Errorf("name = %q, want iris", got)
		}
		if got := r.URL.Query().Get("version"); got != "3" {
			t.Errorf("version = %q, want 3", got)
		}
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization = %q, want %q", got, wantAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_version":{"name":"iris","version":"3","source":"s3://bucket/iris/3"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, wantAuth)
	src, err := c.GetModelVersionSource(context.Background(), "iris", "3")
	if err != nil {
		t.Fatalf("GetModelVersionSource: %v", err)
	}
	if src != "s3://bucket/iris/3" {
		t.Errorf("source = %q, want s3://bucket/iris/3", src)
	}
}

func TestGetModelVersionSourceUnreachable(t *testing.T) {
	// Nil client (no base URL) → ErrUnreachable so callers can match-and-fail.
	var c *Client
	_, err := c.GetModelVersionSource(context.Background(), "x", "1")
	if err != ErrUnreachable {
		t.Errorf("nil client: err = %v, want ErrUnreachable", err)
	}
}

func TestGetModelVersionSourceErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error_code":"RESOURCE_DOES_NOT_EXIST"}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").GetModelVersionSource(context.Background(), "missing", "9")
	if err == nil {
		t.Fatal("expected error from 404, got nil")
	}
	if !strings.Contains(err.Error(), "missing@9") {
		t.Errorf("error %q should mention name@version", err.Error())
	}
}

func TestGetModelVersionSourceEmptySource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model_version":{"name":"iris","version":"3","source":""}}`))
	}))
	defer srv.Close()
	_, err := New(srv.URL, "").GetModelVersionSource(context.Background(), "iris", "3")
	if err == nil || !strings.Contains(err.Error(), "empty source") {
		t.Errorf("expected empty-source error, got %v", err)
	}
}
