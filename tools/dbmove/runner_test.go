package main

import (
	"context"
	"testing"
)

func TestFakeRunnerRecordsCalls(t *testing.T) {
	fr := newFakeRunner()
	fr.out["kubectl get pods"] = "n8n-worker-1 Running"
	out, err := fr.Run(context.Background(), "kubectl", "get", "pods")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if out != "n8n-worker-1 Running" {
		t.Fatalf("got %q", out)
	}
	if len(fr.calls) != 1 || fr.calls[0][0] != "kubectl" {
		t.Fatalf("call not recorded: %v", fr.calls)
	}
}
