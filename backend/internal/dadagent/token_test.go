package dadagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenSource_CachesToken(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "abc", "expires_in": 300})
	}))
	defer srv.Close()

	ts := NewTokenSource(srv.URL, "cid", "secret")
	for i := 0; i < 3; i++ {
		tok, err := ts.Token(context.Background())
		if err != nil || tok != "abc" {
			t.Fatalf("token=%q err=%v", tok, err)
		}
	}
	if hits != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (cached)", hits)
	}
}
