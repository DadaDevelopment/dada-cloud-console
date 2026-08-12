package mcp

import (
	"net/http"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolResultText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range res.Content {
		tc, ok := c.(*sdkmcp.TextContent)
		if !ok {
			t.Fatalf("unexpected content type %T", c)
		}
		sb.WriteString(tc.Text)
	}
	return sb.String()
}

// TestMapResponse_FailuresAreFramedAsToolFailures guards the 2026-08-11
// regression where the assistant relayed a backend 409 to a live user as the
// cause of their outage. Every non-2xx body the model sees must carry the
// preamble that says the text is about the tool call, not about the user's app.
func TestMapResponse_FailuresAreFramedAsToolFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"client error", http.StatusConflict, `{"error":"not applicable to this environment"}`},
		{"not found", http.StatusNotFound, `{"error":"not found"}`},
		{"forbidden", http.StatusForbidden, `{"error":"forbidden"}`},
		{"server error", http.StatusBadGateway, `{"error":"failed to fetch logs: dial tcp: timeout"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := mapResponse(tc.status, []byte(tc.body))
			if !res.IsError {
				t.Fatalf("status %d: IsError = false, want true", tc.status)
			}
			text := toolResultText(t, res)
			if !strings.HasPrefix(text, toolFailurePreamble) {
				t.Errorf("status %d: text does not start with the tool-failure preamble:\n%s", tc.status, text)
			}
			if !strings.Contains(text, tc.body) {
				t.Errorf("status %d: original body was dropped:\n%s", tc.status, text)
			}
		})
	}
}

func TestMapResponse_SuccessIsNotFramedAsFailure(t *testing.T) {
	res := mapResponse(http.StatusOK, []byte(`{"online":true}`))
	if res.IsError {
		t.Fatal("200 response marked as error")
	}
	if text := toolResultText(t, res); strings.Contains(text, toolFailurePreamble) {
		t.Errorf("200 response carries the failure preamble:\n%s", text)
	}
}

func TestErrResult_CarriesToolFailurePreamble(t *testing.T) {
	res := errResult("backend error (transient), retry: dial tcp: connection refused")
	if !res.IsError {
		t.Fatal("errResult: IsError = false, want true")
	}
	if text := toolResultText(t, res); !strings.HasPrefix(text, toolFailurePreamble) {
		t.Errorf("errResult text does not start with the tool-failure preamble:\n%s", text)
	}
}
