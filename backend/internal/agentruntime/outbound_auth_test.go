package agentruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPChannelOutboundSendsBearer(t *testing.T) {
	var got string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer gw.Close()
	if err := NewHTTPChannelOutbound(gw.URL, "s3cret").SendOutbound(context.Background(), "a", "1", "hi", ""); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got != "Bearer s3cret" {
		t.Fatalf("Authorization = %q", got)
	}
}
