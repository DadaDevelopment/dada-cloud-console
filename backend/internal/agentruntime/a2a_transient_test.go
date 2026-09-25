package agentruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func transientAgent(t *testing.T, failures []func(http.ResponseWriter)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	agent := &fakeAgent{t: t, replies: []string{completedReply}}
	ok := agent.handler()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		if n <= len(failures) {
			failures[n-1](w)
			return
		}
		ok(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func status(code int) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code) }
}

func rpcError(msg string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"agentruntime","error":{"code":-32603,"message":"` + msg + `"}}`))
	}
}

func TestA2ASendRetriesTransientCallFailures(t *testing.T) {
	srv, calls := transientAgent(t, []func(http.ResponseWriter){
		status(http.StatusTooManyRequests),
		status(http.StatusBadGateway),
		rpcError("litellm.RateLimitError: 429 Too Many Requests"),
	})
	client := newTestA2AClient(srv.URL)
	var pauses []time.Duration
	client.pause = func(_ context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return nil
	}
	text, err := client.Send(context.Background(), testRun())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(text, "Перевод сделали?") {
		t.Fatalf("text = %q, want the reply after the transient failures", text)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want three retries", calls.Load())
	}
	want := []time.Duration{3 * time.Second, 6 * time.Second, 12 * time.Second}
	if len(pauses) != len(want) {
		t.Fatalf("pauses = %v, want %v", pauses, want)
	}
	for i := range want {
		if pauses[i] != want[i] {
			t.Fatalf("pauses = %v, want %v", pauses, want)
		}
	}
}

func TestA2ASendGivesUpAfterTheRetryBudget(t *testing.T) {
	fail := status(http.StatusServiceUnavailable)
	srv, calls := transientAgent(t, []func(http.ResponseWriter){fail, fail, fail, fail, fail})
	_, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if !errors.Is(err, errA2ATransient) {
		t.Fatalf("err = %v, want the transient failure after the budget", err)
	}
	if calls.Load() != int32(failedTaskRetries+1) {
		t.Fatalf("calls = %d, want the first send plus %d retries", calls.Load(), failedTaskRetries)
	}
}

func TestA2ASendDoesNotRetryAgentErrors(t *testing.T) {
	for name, fail := range map[string]func(http.ResponseWriter){
		"bad request":   status(http.StatusBadRequest),
		"rpc tool down": rpcError("could not reach MCP server"),
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := transientAgent(t, []func(http.ResponseWriter){fail})
			_, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
			if err == nil || errors.Is(err, errA2ATransient) {
				t.Fatalf("err = %v, want a final non-transient error", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("calls = %d, want no retry", calls.Load())
			}
		})
	}
}

func TestA2ASendRetriesARefusedConnection(t *testing.T) {
	srv, calls := transientAgent(t, nil)
	url := srv.URL
	srv.Close()
	client := newTestA2AClient(url)
	tries := 0
	client.pause = func(context.Context, time.Duration) error {
		tries++
		return nil
	}
	_, err := client.Send(context.Background(), testRun())
	if !errors.Is(err, errA2ATransient) || tries != failedTaskRetries {
		t.Fatalf("err = %v after %d pauses, want %d retries of a refused connection", err, tries, failedTaskRetries)
	}
	if calls.Load() != 0 {
		t.Fatalf("a closed server answered %d calls", calls.Load())
	}
}

func TestTransientTransportSkipsTimeouts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if transientTransport(ctx, errors.New("connection reset by peer")) {
		t.Fatal("a cancelled turn must not retry")
	}
	if transientTransport(context.Background(), context.DeadlineExceeded) {
		t.Fatal("a spent call deadline must not retry")
	}
	if !transientTransport(context.Background(), errors.New("read: connection reset by peer")) {
		t.Fatal("a dropped connection must retry")
	}
}
