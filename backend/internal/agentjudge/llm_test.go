package agentjudge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompleteRetriesRateLimit(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"Rate limit reached for requests"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := &OpenAIChat{BaseURL: srv.URL, APIKey: "k", Model: "m", HTTP: srv.Client(), RetryPause: time.Millisecond}
	out, err := c.Complete(context.Background(), "hi")
	if err != nil || out != "{}" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
	}
}
