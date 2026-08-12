package api

import "github.com/gin-gonic/gin"

// liveErrorNote frames a partial-failure field for the model that reads this
// response. Unlike a failed tool call, these responses are HTTP 200, so the
// MCP layer's failure preamble (internal/mcp/proxy.go) never reaches them and
// the raw upstream text would otherwise be indistinguishable from a finding
// about the user's application.
const liveErrorNote = "This reports a failure of the platform's own live-data lookup — the query this endpoint made to its data source (Portainer, Prometheus). It is not a diagnosis of the user's application, not a cause of any outage, and must never be relayed to the user as a finding about their app. Read the live fields of this response as unknown, not as broken."

// setLiveError records a non-fatal upstream lookup failure on an otherwise
// successful response, together with the note that says what the failure is
// about. The human-facing message stays in live_error; the framing rides in a
// sibling field so UI consumers are unaffected.
func setLiveError(resp gin.H, msg string) {
	if msg == "" {
		return
	}
	resp["live_error"] = msg
	resp["live_error_scope"] = "platform_data_source"
	resp["live_error_note"] = liveErrorNote
}
