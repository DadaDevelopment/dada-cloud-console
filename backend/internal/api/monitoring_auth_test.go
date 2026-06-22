package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// generateMonitoringKey -> verifyMonitoringKeyHash must round-trip, and any
// other key must be rejected. This guards the device-ingest auth path.
func TestMonitoringKeyHashRoundTrip(t *testing.T) {
	full, prefix, hash, err := generateMonitoringKey()
	if err != nil {
		t.Fatalf("generateMonitoringKey: %v", err)
	}
	if len(hash) != 48 {
		t.Fatalf("hash length = %d, want 48 (salt16+digest32)", len(hash))
	}
	if prefix != full[:13] {
		t.Fatalf("prefix %q is not the first 13 chars of %q", prefix, full)
	}
	if !verifyMonitoringKeyHash(full, hash) {
		t.Fatal("verify rejected a valid key")
	}
	if verifyMonitoringKeyHash(full+"x", hash) {
		t.Fatal("verify accepted a tampered key")
	}
	other, _, _, _ := generateMonitoringKey()
	if verifyMonitoringKeyHash(other, hash) {
		t.Fatal("verify accepted a different key against this hash")
	}
}

func TestVerifyMonitoringKeyHashRejectsBadLength(t *testing.T) {
	if verifyMonitoringKeyHash("dmon_whatever", []byte{1, 2, 3}) {
		t.Fatal("verify accepted a malformed (short) stored hash")
	}
}

func TestIngestKeyFromRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name      string
		header    string
		headerVal string
		want      string
	}{
		{"x-api-key", "X-API-Key", "dmon_abc", "dmon_abc"},
		{"bearer dmon", "Authorization", "Bearer dmon_xyz", "dmon_xyz"},
		{"bearer jwt is ignored", "Authorization", "Bearer eyJhbGciOi", ""},
		{"non-dmon x-api-key ignored", "X-API-Key", "sk_live_123", ""},
		{"empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/", nil)
			if tc.header != "" {
				c.Request.Header.Set(tc.header, tc.headerVal)
			}
			if got := ingestKeyFromRequest(c); got != tc.want {
				t.Fatalf("ingestKeyFromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}
