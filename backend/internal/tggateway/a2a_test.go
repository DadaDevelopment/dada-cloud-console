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
