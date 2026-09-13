package agentruntime

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const failedTaskReply = `{"id":"task-9","contextId":"runtime-c1","kind":"task","status":{"state":"failed","message":{"role":"agent","parts":[{"kind":"text","text":"429 Too Many Requests"}]}},"artifacts":[]}`

func TestA2ASendRetriesOnceWhenTaskFails(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{failedTaskReply, completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	text, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if !strings.Contains(text, "Перевод сделали?") {
		t.Fatalf("text = %q, want the reply of the retried turn", text)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want exactly one retry", len(agent.requests))
	}
	first, second := agent.requests[0], agent.requests[1]
	if second.TaskID != "" || second.ContextID != first.ContextID || second.MessageID == first.MessageID {
		t.Fatalf("retry must be a fresh message in the same context, got first=%+v second=%+v", first, second)
	}
	if second.Parts[0].Text != first.Parts[0].Text {
		t.Fatalf("retry must resend the same envelope")
	}
}

func TestA2ASendGivesUpWhenTaskFailsTwice(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{failedTaskReply, failedTaskReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	_, err := newTestA2AClient(srv.URL).Send(context.Background(), testRun())
	if err == nil || !strings.Contains(err.Error(), "failed") || !strings.Contains(err.Error(), "429 Too Many Requests") {
		t.Fatalf("err = %v, want task-failed with the agent's reason", err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %d, want exactly one retry", len(agent.requests))
	}
}

func TestA2ASendRetryStopsWhenBudgetIsGone(t *testing.T) {
	agent := &fakeAgent{t: t, replies: []string{failedTaskReply, completedReply}}
	srv := httptest.NewServer(agent.handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	client := newTestA2AClient(srv.URL)
	client.retryPause = failedTaskRetryPause
	_, err := client.Send(ctx, testRun())
	if err == nil || !strings.Contains(err.Error(), "retry after failed task") {
		t.Fatalf("err = %v, want retry aborted by context", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("requests = %d, want no retry once the turn budget is gone", len(agent.requests))
	}
}

func TestParseTaskKeepsFailureReason(t *testing.T) {
	task := parseTask([]byte(failedTaskReply))
	if task.State != "failed" || task.Error != "429 Too Many Requests" {
		t.Fatalf("task = %+v", task)
	}
}
