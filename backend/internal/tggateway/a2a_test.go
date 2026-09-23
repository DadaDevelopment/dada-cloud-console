package tggateway

import (
	"encoding/json"
	"testing"
)

func TestExtractTextSkipsHistoryEcho(t *testing.T) {
	raw := json.RawMessage(`{
		"id": "e79e60ae-3e76-4014-88e4-ffb4b574a422",
		"contextId": "018ccb3d-9cb9-423e-938c-9bb142b880a6",
		"status": {
			"state": "completed",
			"message": {
				"role": "agent",
				"parts": [{"kind": "text", "text": "verdict json"}]
			}
		},
		"history": [
			{"role": "user", "parts": [{"kind": "text", "text": "/start"}]},
			{"role": "agent", "parts": [{"kind": "text", "text": "verdict json"}]}
		]
	}`)

	got := extractText(raw)
	want := "verdict json"
	if got != want {
		t.Fatalf("extractText() = %q, want %q (history echo leaked into reply)", got, want)
	}
}

func TestExtractTextFallsBackToArtifactsWhenNoStatusMessage(t *testing.T) {
	raw := json.RawMessage(`{
		"artifacts": [
			{"parts": [{"kind": "text", "text": "final answer"}]}
		],
		"history": [
			{"role": "user", "parts": [{"kind": "text", "text": "hello"}]}
		]
	}`)

	got := extractText(raw)
	want := "final answer"
	if got != want {
		t.Fatalf("extractText() = %q, want %q", got, want)
	}
}

func TestA2AResultEnvelopeDetectsPausedHITLTask(t *testing.T) {
	raw := json.RawMessage(`{
		"kind": "task",
		"status": {
			"state": "input-required",
			"message": {
				"role": "agent",
				"parts": [{"kind": "data", "data": {"question": "какая валюта?"}}]
			}
		}
	}`)

	var envelope a2aResultEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.Status.State != "input-required" {
		t.Fatalf("Status.State = %q, want input-required", envelope.Status.State)
	}
	if got := extractText(raw); got != "" {
		t.Fatalf("extractText() = %q, want empty (question lives under a data.question key, not text) - this is exactly why status.state must be checked first", got)
	}
}

func TestA2AResultEnvelopeReadsCompletedTask(t *testing.T) {
	raw := json.RawMessage(`{
		"status": {
			"state": "completed",
			"message": {"role": "agent", "parts": [{"kind": "text", "text": "verdict json"}]}
		}
	}`)

	var envelope a2aResultEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.Status.State != "completed" {
		t.Fatalf("Status.State = %q, want completed", envelope.Status.State)
	}
}

func TestA2AResultEnvelopeReadsFailedTaskState(t *testing.T) {
	raw := json.RawMessage(`{
		"status": {"state": "failed"},
		"artifacts": [{"parts": [{"kind": "text", "text": "Error code: 429 - {\"error\": {\"code\": \"1310\", \"message\": \"Weekly/Monthly Limit Exhausted\"}}"}]}]
	}`)

	var envelope a2aResultEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if envelope.Status.State != "failed" {
		t.Fatalf("Status.State = %q, want failed - a failed task's artifacts must never be read as a reply", envelope.Status.State)
	}
}
