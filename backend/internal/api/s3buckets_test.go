package api

import (
	"testing"
	"time"
)

func TestDeclaredS3ConnectionSecret(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		wantNs  string
		wantSec string
	}{
		{
			name:    "adopted bucket with explicit ref honored",
			summary: `{"spec":{"connectionSecret":{"name":"mimir-s3-credentials","namespace":"monitoring"}}}`,
			wantNs:  "monitoring",
			wantSec: "mimir-s3-credentials",
		},
		{
			name:    "explicit ref without namespace falls to default at resolve time",
			summary: `{"spec":{"connectionSecret":{"name":"media-s3-credentials"}}}`,
			wantNs:  "",
			wantSec: "media-s3-credentials",
		},
		{
			name:    "console-created bucket has no connectionSecret so convention is used",
			summary: `{"spec":{"bucketName":"media","region":"ru1"}}`,
			wantNs:  "",
			wantSec: "",
		},
		{
			name:    "unrelated secret name rejected by the -s3-credentials guard",
			summary: `{"spec":{"connectionSecret":{"name":"beget-credentials","namespace":"crossplane-system"}}}`,
			wantNs:  "",
			wantSec: "",
		},
		{
			name:    "tfstate secret name rejected by the guard",
			summary: `{"spec":{"connectionSecret":{"name":"tfstate-beget-s3-x-providerconfig","namespace":"crossplane-system"}}}`,
			wantNs:  "",
			wantSec: "",
		},
		{
			name:    "empty summary yields no ref",
			summary: ``,
			wantNs:  "",
			wantSec: "",
		},
		{
			name:    "malformed json yields no ref",
			summary: `{"spec":`,
			wantNs:  "",
			wantSec: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns, sec := declaredS3ConnectionSecret([]byte(tc.summary))
			if ns != tc.wantNs || sec != tc.wantSec {
				t.Errorf("declaredS3ConnectionSecret(%q) = (%q, %q), want (%q, %q)", tc.summary, ns, sec, tc.wantNs, tc.wantSec)
			}
		})
	}
}

func TestS3ProvisionError(t *testing.T) {
	cases := []struct {
		name       string
		summary    string
		wantMsg    string
		wantReason string
	}{
		{
			name:       "provider rejection is surfaced verbatim",
			summary:    `{"provision_error":"Attribute description string length must be at most 45, got: 70","provision_error_reason":"ReconcileError"}`,
			wantMsg:    "Attribute description string length must be at most 45, got: 70",
			wantReason: "ReconcileError",
		},
		{
			name:       "healthy bucket carries no error",
			summary:    `{"status":"Ready","conditions":{"Ready":"True","Synced":"True"}}`,
			wantMsg:    "",
			wantReason: "",
		},
		{
			name:       "whitespace-only message counts as no error",
			summary:    `{"provision_error":"   ","provision_error_reason":"ReconcileError"}`,
			wantMsg:    "",
			wantReason: "ReconcileError",
		},
		{
			name:       "message without reason still reaches the caller",
			summary:    `{"provision_error":"create failed: API Error"}`,
			wantMsg:    "create failed: API Error",
			wantReason: "",
		},
		{
			name:       "empty summary yields nothing",
			summary:    ``,
			wantMsg:    "",
			wantReason: "",
		},
		{
			name:       "malformed json yields nothing",
			summary:    `{"provision_error":`,
			wantMsg:    "",
			wantReason: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, reason := s3ProvisionError([]byte(tc.summary))
			if msg != tc.wantMsg || reason != tc.wantReason {
				t.Errorf("s3ProvisionError(%q) = (%q, %q), want (%q, %q)", tc.summary, msg, reason, tc.wantMsg, tc.wantReason)
			}
		})
	}
}

func TestResolveProvisioningSince(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	auditTime := time.Date(2026, 8, 2, 20, 10, 31, 0, time.UTC)
	firstSeen := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)

	t.Run("audit row present wins over first_seen_at", func(t *testing.T) {
		got, ok := resolveProvisioningSince(&auditTime, firstSeen, now)
		if !ok {
			t.Fatal("resolveProvisioningSince() ok = false, want true")
		}
		if !got.Equal(auditTime) {
			t.Errorf("resolveProvisioningSince() = %v, want the audit timestamp %v", got, auditTime)
		}
	})

	t.Run("no audit row falls back to first_seen_at for adopted buckets", func(t *testing.T) {
		got, ok := resolveProvisioningSince(nil, firstSeen, now)
		if !ok {
			t.Fatal("resolveProvisioningSince() ok = false, want true")
		}
		if !got.Equal(firstSeen) {
			t.Errorf("resolveProvisioningSince() = %v, want first_seen_at %v", got, firstSeen)
		}
	})

	t.Run("stale first_seen_at is refused so adopted buckets keep the browser clock", func(t *testing.T) {
		stale := now.Add(-maxSnapshotProvisioningAge - time.Minute)
		if _, ok := resolveProvisioningSince(nil, stale, now); ok {
			t.Error("resolveProvisioningSince() ok = true, want false for a first_seen_at older than the freshness bound")
		}
	})

	t.Run("neither audit row nor first_seen_at yields nothing", func(t *testing.T) {
		_, ok := resolveProvisioningSince(nil, time.Time{}, now)
		if ok {
			t.Error("resolveProvisioningSince() ok = true, want false when both sources are empty")
		}
	})
}
