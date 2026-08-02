package api

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dada-tuda/console/backend/internal/agentchat"
)

const agentChatSecretFixture = "sk_live_51NpXqLEAK"

// TestAgentChatCardArgs_RedactsBeforeLeavingTheProcess covers the confirm_request
// SSE frame, which shipped the pending action's raw ArgsJSON: a bulkSetEnvVars
// proposal put every plaintext value into the browser DOM (and into anything
// recording the stream) before the user had approved anything.
func TestAgentChatCardArgs_RedactsBeforeLeavingTheProcess(t *testing.T) {
	cases := []string{
		`{"appName":"api","key":"STRIPE_KEY","value":"` + agentChatSecretFixture + `"}`,
		`{"appName":"api","vars":[{"key":"STRIPE_KEY","value":"` + agentChatSecretFixture + `"}]}`,
		`{"repo":{"nested":{"deep":{"token":"` + agentChatSecretFixture + `"}}}}`,
	}
	for _, raw := range cases {
		frame, err := json.Marshal(map[string]any{"args": agentChatCardArgs(raw)})
		if err != nil {
			t.Fatalf("marshal frame: %v", err)
		}
		if strings.Contains(string(frame), agentChatSecretFixture) {
			t.Errorf("confirm_request frame carries the plaintext secret: %s", frame)
		}
		if !strings.Contains(string(frame), agentchat.RedactedMarker) {
			t.Errorf("confirm_request frame dropped the redaction marker instead of substituting it: %s", frame)
		}
		if !strings.Contains(string(frame), "STRIPE_KEY") && strings.Contains(raw, "STRIPE_KEY") {
			t.Errorf("redaction ate the variable NAME too, so the card cannot say what is being set: %s", frame)
		}
	}
}

func TestAgentChatCardArgs_EmptyArgsStayValidJSON(t *testing.T) {
	for _, raw := range []string{"", "   ", "{}", "null"} {
		got := string(agentChatCardArgs(raw))
		var into any
		if err := json.Unmarshal([]byte(got), &into); err != nil {
			t.Errorf("agentChatCardArgs(%q) = %q, which is not valid JSON for the SSE frame: %v", raw, got, err)
		}
	}
}

// TestAgentChatAuditArgs_RedactNested pins the audit path: audit_events.metadata
// is read by support and by admins, so a nested secret landing there outlives
// the chat itself.
func TestAgentChatAuditArgs_RedactNested(t *testing.T) {
	args := agentchat.RedactArgs(`{"vars":[{"key":"STRIPE_KEY","value":"` + agentChatSecretFixture + `"}]}`)
	blob, err := json.Marshal(map[string]any{"args": args})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if strings.Contains(string(blob), agentChatSecretFixture) {
		t.Errorf("audit metadata carries a nested plaintext secret: %s", blob)
	}
}

// TestAgentChatTranscriptToolResult_StripsMintedAndPresignedSecrets covers what
// is persisted into agent_chat_messages.content. createDeployHook returns a
// bearer token exactly once and it was stored verbatim; a backup download link
// works without a login for anyone who later reads the transcript.
func TestAgentChatTranscriptToolResult_StripsMintedAndPresignedSecrets(t *testing.T) {
	cases := []struct {
		tool string
		text string
		leak string
	}{
		{"createDeployHook", `{"token":"dh_live_9f2b1c","name":"ci"}`, "dh_live_9f2b1c"},
		{"downloadDatabaseBackup", `{"url":"https://s3.dada/dump.sql?X-Amz-Signature=DEADBEEFCAFE"}`, "DEADBEEFCAFE"},
		{"downloadSourceArchive", `{"url":"https://s3.dada/src.zip?X-Amz-Signature=DEADBEEFCAFE"}`, "DEADBEEFCAFE"},
	}
	for _, tc := range cases {
		got := agentChatTranscriptToolResult(tc.tool, tc.text)
		if strings.Contains(got, tc.leak) {
			t.Errorf("%s: transcript row keeps the credential %q: %s", tc.tool, tc.leak, got)
		}
	}
}

// TestAgentChatTranscriptToolResult_IgnoresTheErrorFlag documents that
// redaction is unconditional. The tool-result path used to consult isError, but
// a presigned URL leaks just as completely from an error body as from a success
// body.
func TestAgentChatTranscriptToolResult_IgnoresTheErrorFlag(t *testing.T) {
	text := `error: minted token dh_live_9f2b1c was rejected`
	if got := agentChatTranscriptToolResult("createDeployHook", text); strings.Contains(got, "dh_live_9f2b1c") {
		t.Errorf("error-path transcript keeps the minted token: %s", got)
	}
}

// TestTruncateForTranscript_CutsOnRuneBoundaries pins the Postgres failure: a
// byte-sliced cut splits a multi-byte rune roughly half the time on Cyrillic
// text, and the INSERT then fails with 22021 invalid byte sequence for encoding
// "UTF8", losing the whole transcript row.
func TestTruncateForTranscript_CutsOnRuneBoundaries(t *testing.T) {
	for max := 1; max <= 40; max++ {
		got := truncateForTranscript(strings.Repeat("я", 40), max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d produced invalid UTF-8 %q; Postgres would reject the row", max, got)
		}
	}
	if got := truncateForTranscript("short", 100); got != "short" {
		t.Errorf("under the cap the text must pass through unchanged, got %q", got)
	}
}
