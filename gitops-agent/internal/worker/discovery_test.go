package worker

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// crWithConditions builds a minimal unstructured CR carrying the given
// status.conditions slice, mirroring the shape a Crossplane provider writes.
func crWithConditions(conds []map[string]any) *unstructured.Unstructured {
	raw := make([]any, len(conds))
	for i, c := range conds {
		raw[i] = c
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"conditions": raw,
		},
	}}
}

func TestCrProvisionError(t *testing.T) {
	cases := []struct {
		name       string
		conds      []map[string]any
		wantMsg    string
		wantReason string
		wantOK     bool
	}{
		{
			name: "single blocking condition",
			conds: []map[string]any{
				{"type": "Synced", "status": "False", "reason": "ReconcileError", "message": "Attribute description string length must be at most 45, got: 70"},
			},
			wantMsg:    "Attribute description string length must be at most 45, got: 70",
			wantReason: "ReconcileError",
			wantOK:     true,
		},
		{
			name: "synced takes priority over ready",
			conds: []map[string]any{
				{"type": "Ready", "status": "False", "reason": "Creating", "message": "waiting for resource to become ready"},
				{"type": "Synced", "status": "False", "reason": "ReconcileError", "message": "provider rejected the spec"},
			},
			wantMsg:    "provider rejected the spec",
			wantReason: "ReconcileError",
			wantOK:     true,
		},
		{
			name: "ready used when synced absent",
			conds: []map[string]any{
				{"type": "Ready", "status": "False", "reason": "Unavailable", "message": "backing resource not found"},
			},
			wantMsg:    "backing resource not found",
			wantReason: "Unavailable",
			wantOK:     true,
		},
		{
			name: "falls back to any other False condition with a message",
			conds: []map[string]any{
				{"type": "SomeOther", "status": "False", "reason": "Blocked", "message": "quota exceeded"},
			},
			wantMsg:    "quota exceeded",
			wantReason: "Blocked",
			wantOK:     true,
		},
		{
			name: "empty when everything is True",
			conds: []map[string]any{
				{"type": "Synced", "status": "True", "reason": "ReconcileSuccess", "message": "resource is up to date"},
				{"type": "Ready", "status": "True", "reason": "Available", "message": "resource is available"},
			},
			wantOK: false,
		},
		{
			name: "empty when False condition has no message",
			conds: []map[string]any{
				{"type": "Synced", "status": "False", "reason": "ReconcileError", "message": ""},
			},
			wantOK: false,
		},
		{
			name:   "empty when no conditions at all",
			conds:  nil,
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := crWithConditions(tc.conds)
			msg, reason, ok := crProvisionError(o)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if msg != tc.wantMsg {
				t.Errorf("message = %q, want %q", msg, tc.wantMsg)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}
