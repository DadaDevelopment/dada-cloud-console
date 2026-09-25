package tggatewayclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsBearer(t *testing.T) {
	var got []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.Write([]byte(`{"bot_username":"b"}`))
	}))
	defer gw.Close()
	c := New(gw.URL, "s3cret")
	ctx := context.Background()
	if _, err := c.Bind(ctx, "a", "p", "tok"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := c.Get(ctx, "a"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := c.Unbind(ctx, "a"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("calls: %d", len(got))
	}
	for i, h := range got {
		if h != "Bearer s3cret" {
			t.Errorf("call %d Authorization = %q", i, h)
		}
	}
}
