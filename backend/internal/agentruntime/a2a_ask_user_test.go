package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dada-tuda/console/backend/internal/turnbudget"
)

type fakeAgent struct {
	t        *testing.T
	requests []a2aMessage
	headers  []http.Header
	replies  []string
}

func (f *fakeAgent) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req a2aRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Fatalf("decode: %v", err)
		}
		f.requests = append(f.requests, req.Params.Message)
		f.headers = append(f.headers, r.Header.Clone())
		reply := f.replies[len(f.requests)-1]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"agentruntime","result":` + reply + `}`))
	}
}

const pausedOnAskUser = `{"id":"task-1","contextId":"runtime-c1","kind":"task","status":{"state":"input-required","message":{"role":"agent","parts":[
  {"kind":"data","data":{"id":"fc1","name":"ask_user","args":{"questions":[{"question":"placeholder"},{"question":"Когда добавите остальное?"}]}},"metadata":{"kagent_type":"function_call","kagent_is_long_running":true}},
  {"kind":"data","data":{"id":"fc2","name":"adk_request_confirmation","args":{"originalFunctionCall":{"name":"ask_user"}}},"metadata":{"kagent_type":"function_call","kagent_is_long_running":true}}]}},"artifacts":[]}`

const pausedOnPlaceholder = `{"id":"task-1","contextId":"runtime-c1","kind":"task","status":{"state":"input-required","message":{"role":"agent","parts":[
  {"kind":"data","data":{"id":"adk-cf72ff99","name":"adk_request_confirmation","args":{"originalFunctionCall":{"args":{"questions":[{"question":"placeholder"}]},"id":"call_-7265911073608296503","name":"ask_user"},"toolConfirmation":{"confirmed":false,"hint":"","payload":null}}},"metadata":{"kagent_type":"function_call","kagent_is_long_running":true}}]}},"artifacts":[]}`

const completedReply = `{"id":"task-1","contextId":"runtime-c1","kind":"task","status":{"state":"completed"},"artifacts":[{"parts":[{"kind":"text","text":"Деньги в кошельке FxPro, переведите их на торговый счёт. Перевод сделали?"}]}]}`

func newTestA2AClient(url string) *httpA2AClient {
	return &httpA2AClient{http: &http.Client{Timeout: 5 * time.Second}, endpoint: func(string) string { return url },
		retryPause: failedTaskRetryPause, retries: failedTaskRetries, pause: func(context.Context, time.Duration) error { return nil }}
}

func testRun() AgentRunRequest {
	return AgentRunRequest{AgentName: "tg-exchange-support", ContextID: "runtime-c1", EndUserKey: "telegram:-900100000423",
		Messages: []Message{{Role: "user", Content: "пополнила 526, но на торговом 0, где деньги?"}}}
}

func TestA2ASendRetriesTheTurnWhenAgentPausesOnAskUser(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{pausedOnPlaceholder, completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	client := newTestA2AClient(srv.URL)
	var pauses []time.Duration
	client.pause = func(_ context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return nil
	}
	reply, err := client.Send(context.Background(), testRun())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(reply, "переведите их на торговый счёт") {
		t.Fatalf("reply = %q", reply)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want initial + one fresh retry", len(agent.requests))
	}
	first, retry := agent.requests[0], agent.requests[1]
	if retry.TaskID != "" || retry.ContextID != first.ContextID || retry.MessageID == first.MessageID {
		t.Fatalf("a paused turn must be retried as a fresh message, not resumed: first=%+v retry=%+v", first, retry)
	}
	if len(retry.Parts) != 1 || retry.Parts[0].Kind != "text" || retry.Parts[0].Text != first.Parts[0].Text {
		t.Fatalf("retry must resend the same envelope, got %+v", retry.Parts)
	}
	if len(pauses) != 1 || pauses[0] != 3*time.Second {
		t.Fatalf("pauses = %v, want one 3s pause before the retry", pauses)
	}
}

func TestA2ASendResumesTaskThatKeepsPausingOnAskUser(t *testing.T) {
	replies := []string{pausedOnAskUser, pausedOnAskUser, pausedOnAskUser, pausedOnAskUser, completedReply}
	agent := &fakeAgent{t: t, replies: replies}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	reply, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(reply, "переведите их на торговый счёт") {
		t.Fatalf("reply = %q", reply)
	}
	if len(agent.requests) != 2+failedTaskRetries {
		t.Fatalf("requests = %d, want initial + %d retries + resume", len(agent.requests), failedTaskRetries)
	}
	for i := 1; i <= failedTaskRetries; i++ {
		if agent.requests[i].TaskID != "" {
			t.Fatalf("request %d must be a fresh retry, got %+v", i, agent.requests[i])
		}
	}
	resume := agent.requests[len(agent.requests)-1]
	if resume.ContextID != "runtime-c1" || resume.TaskID != "task-1" || resume.Role != "user" {
		t.Fatalf("resume addressing = %+v", resume)
	}
	if len(resume.Parts) != 1 || resume.Parts[0].Kind != "data" {
		t.Fatalf("resume parts = %+v", resume.Parts)
	}
	data := resume.Parts[0].Data
	if data["decision_type"] != "approve" {
		t.Fatalf("decision_type = %v", data["decision_type"])
	}
	answers, _ := data["ask_user_answers"].([]any)
	if len(answers) != 2 {
		t.Fatalf("answers = %v, want one per question", data["ask_user_answers"])
	}
	for _, a := range answers {
		texts, _ := a.(map[string]any)["answer"].([]any)
		if len(texts) != 1 || texts[0] != askUserUnavailableAnswer {
			t.Fatalf("answer = %v", a)
		}
	}
	for i, h := range agent.headers {
		if h.Get(endUserHeader) != "telegram:-900100000423" || h.Get(agentHeader) != "tg-exchange-support" {
			t.Fatalf("request %d lost identity headers: %v", i, h)
		}
	}
}

func TestA2ASendGivesUpWhenAgentPausesPastTheResume(t *testing.T) {
	replies := make([]string, 0, 2+failedTaskRetries)
	for len(replies) < 2+failedTaskRetries {
		replies = append(replies, pausedOnPlaceholder)
	}
	agent := &fakeAgent{t: t, replies: replies}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	_, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if err == nil || !strings.Contains(err.Error(), "input-required") {
		t.Fatalf("err = %v, want task-did-not-complete", err)
	}
	if len(agent.requests) != 2+failedTaskRetries {
		t.Fatalf("requests = %d, want retries and exactly one resume", len(agent.requests))
	}
}

func TestA2ASendCompletedTaskNeedsNoResume(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	if _, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d", len(agent.requests))
	}
}

func TestA2ASendNamesEmptyRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"agentruntime","error":{"code":-32603,"message":""}}`))
	}))
	defer srv.Close()

	_, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if err == nil || !strings.Contains(err.Error(), "-32603") || !strings.Contains(err.Error(), "MCP tool server") {
		t.Fatalf("err = %v, want code and a diagnosable message", err)
	}
}

func TestParseTaskCountsAskUserQuestions(t *testing.T) {
	task := parseTask(json.RawMessage(pausedOnAskUser))
	if task.ID != "task-1" || task.State != "input-required" {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Questions) != 2 || task.Questions[0] != "placeholder" {
		t.Fatalf("questions = %v", task.Questions)
	}
	bare := parseTask(json.RawMessage(`{"id":"t","status":{"state":"input-required"}}`))
	if len(bare.Questions) != 1 {
		t.Fatalf("paused task without visible questions must still get one answer: %+v", bare)
	}
}

func TestParseTaskUnwrapsConfirmationWrapper(t *testing.T) {
	task := parseTask(json.RawMessage(pausedOnPlaceholder))
	if task.State != "input-required" || !task.needsRetry() {
		t.Fatalf("task = %+v", task)
	}
	if len(task.Questions) != 1 || task.Questions[0] != "placeholder" {
		t.Fatalf("questions = %v, want the placeholder the model sent inside adk_request_confirmation", task.Questions)
	}
}

func TestA2ASendGivesEachAgentCallItsOwnBudget(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{pausedOnAskUser, completedReply}}
	slow := func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		agent.handler()(w, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(slow))
	defer srv.Close()

	client := newTestA2AClient(srv.URL)
	client.timeout = 250 * time.Millisecond
	if _, err := client.Send(context.Background(), testRun()); err != nil {
		t.Fatalf("two 150ms calls under a 250ms per-call budget must succeed: %v", err)
	}
}

func TestA2ASendTimesOutSlowAgentCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()

	client := newTestA2AClient(srv.URL)
	client.timeout = 100 * time.Millisecond
	started := time.Now()
	_, err := client.Send(context.Background(), testRun())
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("timeout not applied per call: took %s", time.Since(started))
	}
}

func TestNewA2AClientUsesTurnBudget(t *testing.T) {
	t.Setenv("AGENT_CALL_TIMEOUT_SECONDS", "")
	client := NewA2AClient().(*httpA2AClient)
	if client.timeout != turnbudget.AgentCall() || client.http.Timeout != 0 {
		t.Fatalf("client timeout = %s, http timeout = %s", client.timeout, client.http.Timeout)
	}
}
