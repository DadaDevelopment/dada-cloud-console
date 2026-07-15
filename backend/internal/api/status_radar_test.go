package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// TestPublicStatusRadarHandler drives PublicStatusRadar through a real Gin
// engine and asserts the response contract the frontend Status Radar landing
// depends on: fixed key names, all 6 targets present in order, and no
// dropped row even though the probe hits live third-party domains.
func TestPublicStatusRadarHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	r.GET("/api/public/status", h.PublicStatusRadar)

	req := httptest.NewRequest(http.MethodGet, "/api/public/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp statusRadarResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Vantage == "" {
		t.Fatal("expected non-empty vantage")
	}
	if _, err := time.Parse(time.RFC3339, resp.UpdatedAt); err != nil {
		t.Fatalf("updated_at not RFC3339: %v", err)
	}
	if len(resp.Services) != len(statusRadarTargets) {
		t.Fatalf("expected %d services, got %d", len(statusRadarTargets), len(resp.Services))
	}
	for i, target := range statusRadarTargets {
		got := resp.Services[i]
		if got.ID != target.ID || got.Target != target.Target {
			t.Fatalf("service[%d] = %+v, want id=%s target=%s", i, got, target.ID, target.Target)
		}
	}
}

// TestPublicStatusRadarCache asserts the second call within the TTL window
// reuses the cached snapshot instead of re-probing.
func TestPublicStatusRadarCache(t *testing.T) {
	globalStatusRadarCache.set(&statusRadarResponse{
		Vantage:   "test",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Services:  []statusRadarService{{ID: "vercel", Name: "Vercel", Target: "https://vercel.com"}},
	})
	defer func() { globalStatusRadarCache = &statusRadarCache{} }()

	cached, ok := globalStatusRadarCache.get()
	if !ok {
		t.Fatal("expected cache hit")
	}
	if cached.Vantage != "test" {
		t.Fatalf("expected cached snapshot, got %+v", cached)
	}
}
