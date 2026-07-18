package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

const clientErrorMaxBody = 32 * 1024

type clientErrorReport struct {
	Message        string `json:"message"`
	Stack          string `json:"stack"`
	ComponentStack string `json:"component_stack"`
	URL            string `json:"url"`
	Kind           string `json:"kind"`
}

// clampLen truncates s to at most n bytes so a hostile or huge client payload
// cannot bloat a single log line.
func clampLen(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReportClientError records a browser-side crash into the server logs, so
// client-side React/render errors surface in `kubectl logs` instead of dying
// silently in the user's browser console. It is intentionally unauthenticated
// (crashes happen on any page, including before login) and fire-and-forget:
// bad or empty payloads still return 204, and the body is size-capped.
//
// @ID          reportClientError
// @Summary     Report a client-side crash
// @Description Records a browser-side error (React error boundary or an unhandled window error/rejection) into the server logs. Unauthenticated; the browser posts message/stack/url. Always returns 204.
// @Tags        telemetry
// @Accept      json
// @Param       body body     clientErrorReport true "Client error"
// @Success     204  "no content"
// @Router      /client-errors [post]
func (h *Handler) ReportClientError(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, clientErrorMaxBody)

	var r clientErrorReport
	if err := c.ShouldBindJSON(&r); err != nil || r.Message == "" {
		c.Status(http.StatusNoContent)
		return
	}
	kind := r.Kind
	if kind == "" {
		kind = "react"
	}
	log.Warn().
		Str("kind", clampLen(kind, 40)).
		Str("url", clampLen(r.URL, 300)).
		Str("ua", clampLen(c.Request.UserAgent(), 200)).
		Str("component_stack", clampLen(r.ComponentStack, 2000)).
		Str("stack", clampLen(r.Stack, 4000)).
		Msg("client crash: " + clampLen(r.Message, 500))

	c.Status(http.StatusNoContent)
}
