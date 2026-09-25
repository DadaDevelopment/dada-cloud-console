package tggateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type failingRuntime struct {
	mu    sync.Mutex
	fail  bool
	calls atomic.Int32
}

func (f *failingRuntime) ProcessMessage(context.Context, RuntimeMessageRequest) (RuntimeMessageResponse, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return RuntimeMessageResponse{}, errors.New("runtime: message processing failed")
	}
	return RuntimeMessageResponse{Text: "real reply"}, nil
}

func (f *failingRuntime) setFail(v bool) {
	f.mu.Lock()
	f.fail = v
	f.mu.Unlock()
}

type scriptedTelegram struct {
	fakeTelegram
	mu      sync.Mutex
	batches [][]TelegramUpdate
	sent    []string
}

func (s *scriptedTelegram) push(batch ...TelegramUpdate) {
	s.mu.Lock()
	s.batches = append(s.batches, batch)
	s.mu.Unlock()
}

func (s *scriptedTelegram) GetUpdates(ctx context.Context, _ string, _ int64, _ int) ([]TelegramUpdate, error) {
	for {
		s.mu.Lock()
		if len(s.batches) > 0 {
			b := s.batches[0]
			s.batches = s.batches[1:]
			s.mu.Unlock()
			return b, nil
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (s *scriptedTelegram) SendMessage(_ context.Context, _ string, _ int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, text)
	return nil
}

func (s *scriptedTelegram) SendMessageWithLocationButton(ctx context.Context, token string, chatID int64, text string) error {
	return s.SendMessage(ctx, token, chatID, text)
}

func (s *scriptedTelegram) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func runFailurePoller(t *testing.T, b Binding, rt *failingRuntime) (*scriptedTelegram, func()) {
	t.Helper()
	tg := &scriptedTelegram{}
	ctx, cancel := context.WithCancel(context.Background())
	go runPollerDebounced(ctx, tg, &runtimeWiringA2A{}, rt, b, nil)
	return tg, cancel
}

func TestRuntimeFailureSendsNoticeOncePerChatStreak(t *testing.T) {
	rt := &failingRuntime{fail: true}
	tg, stop := runFailurePoller(t, Binding{AgentName: "agent", BotToken: "token"}, rt)
	defer stop()

	tg.push(TelegramUpdate{UpdateID: 1, ChatID: 42, Text: "hello"})
	waitFor(t, func() bool { return len(tg.messages()) == 1 })
	if got := tg.messages()[0]; got != runtimeFailureNotice {
		t.Fatalf("notice = %q, want %q", got, runtimeFailureNotice)
	}

	tg.push(TelegramUpdate{UpdateID: 2, ChatID: 42, Text: "still there?"})
	waitFor(t, func() bool { return rt.calls.Load() == 2 })
	tg.push(TelegramUpdate{UpdateID: 3, ChatID: 7, Text: "other chat"})
	waitFor(t, func() bool { return len(tg.messages()) == 2 })
	time.Sleep(30 * time.Millisecond)
	if got := tg.messages(); len(got) != 2 {
		t.Fatalf("messages = %q, want one notice per chat", got)
	}

	rt.setFail(false)
	tg.push(TelegramUpdate{UpdateID: 4, ChatID: 42, Text: "again"})
	waitFor(t, func() bool { return len(tg.messages()) == 3 })
	rt.setFail(true)
	tg.push(TelegramUpdate{UpdateID: 5, ChatID: 42, Text: "and again"})
	waitFor(t, func() bool { return len(tg.messages()) == 4 })
	got := tg.messages()
	if got[2] != "real reply" || got[3] != runtimeFailureNotice {
		t.Fatalf("messages = %q, want reply then a fresh notice for the new streak", got)
	}
}

func TestRuntimeFailureHonoursBindingPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding Binding
		want    []string
	}{
		{"custom text", Binding{AgentName: "agent", BotToken: "token", FailureText: "Передаю коллеге, он скоро ответит."}, []string{"Передаю коллеге, он скоро ответит."}},
		{"silent", Binding{AgentName: "agent", BotToken: "token", OnFailure: FailureSilent, FailureText: "ignored"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &failingRuntime{fail: true}
			tg, stop := runFailurePoller(t, tc.binding, rt)
			defer stop()
			tg.push(TelegramUpdate{UpdateID: 1, ChatID: 42, Text: "hello"})
			waitFor(t, func() bool { return rt.calls.Load() == 1 })
			if tc.want != nil {
				waitFor(t, func() bool { return len(tg.messages()) == len(tc.want) })
			}
			time.Sleep(30 * time.Millisecond)
			got := tg.messages()
			if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
				t.Fatalf("messages = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseFailureMode(t *testing.T) {
	for raw, want := range map[string]FailureMode{"": FailureNotice, "notice": FailureNotice, "silent": FailureSilent} {
		if got, ok := ParseFailureMode(raw); !ok || got != want {
			t.Fatalf("ParseFailureMode(%q) = %q,%v", raw, got, ok)
		}
	}
	if _, ok := ParseFailureMode("escalate"); ok {
		t.Fatal("unknown mode accepted")
	}
}

func TestFailurePolicyEndpointSurvivesRebind(t *testing.T) {
	store := newFakeStore()
	mgr := NewManager(store, fakeTelegram{}, &fakeA2A{}, nil)
	if _, err := mgr.Bind(context.Background(), "agent", "project", "token"); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(mgr)
	srv.SetToken("secret")
	h := srv.Handler()
	authed := func(req *http.Request) *http.Request {
		req.Header.Set("Authorization", "Bearer secret")
		return req
	}

	put := func(agent, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodPut, "/bindings/"+agent+"/failure", strings.NewReader(body))))
		return rec
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodPut, "/bindings/agent/failure", strings.NewReader(`{"on_failure":"silent"}`)))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauth.Code)
	}
	if rec := put("agent", `{"on_failure":"loud"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode status = %d", rec.Code)
	}
	if rec := put("ghost", `{"on_failure":"silent"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent status = %d", rec.Code)
	}
	if rec := put("agent", `{"on_failure":"silent","failure_notice":" hi "}`); rec.Code != http.StatusOK {
		t.Fatalf("set status = %d body=%s", rec.Code, rec.Body)
	}
	if _, err := mgr.Bind(context.Background(), "agent", "project", "token2"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/bindings/agent", nil)))
	var got map[string]any
	body, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["on_failure"] != "silent" || got["failure_notice"] != "hi" {
		t.Fatalf("after rebind = %v", got)
	}
}
